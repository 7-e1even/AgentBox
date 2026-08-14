package httpapi

const workerSessionDaemon = `#!/usr/bin/env python3
import base64
import codecs
import errno
import fcntl
import hashlib
import json
import os
import posixpath
import pty
import select
import signal
import socket
import ssl
import struct
import subprocess
import sys
import termios
import threading
import time
from urllib.parse import urlsplit

VERSION = "2"
MAX_FILE_SIZE = 512 * 1024
MAX_UPLOAD_CHUNK_SIZE = 192 * 1024
MAX_UPLOAD_SIZE = 50 * 1024 * 1024
WEBSOCKET_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
RUNTIME_DRIVER = "/usr/local/lib/agentbox/runtime_driver.py"
RUNTIME_PYTHON = "/opt/agentbox/runtime/bin/python"


class WebSocket:
    def __init__(self, url, credential):
        self.url = url
        self.credential = credential
        self.sock = None
        self.send_lock = threading.Lock()

    def connect(self):
        parsed = urlsplit(self.url)
        if parsed.scheme not in ("ws", "wss"):
            raise RuntimeError("invalid websocket URL")
        host = parsed.hostname
        port = parsed.port or (443 if parsed.scheme == "wss" else 80)
        raw = socket.create_connection((host, port), timeout=15)
        if parsed.scheme == "wss":
            raw = ssl.create_default_context().wrap_socket(raw, server_hostname=host)
        raw.settimeout(15)
        key = base64.b64encode(os.urandom(16)).decode("ascii")
        path = parsed.path or "/"
        if parsed.query:
            path += "?" + parsed.query
        host_header = host if port in (80, 443) else "%s:%d" % (host, port)
        request = (
            "GET %s HTTP/1.1\r\n"
            "Host: %s\r\n"
            "Upgrade: websocket\r\n"
            "Connection: Upgrade\r\n"
            "Sec-WebSocket-Key: %s\r\n"
            "Sec-WebSocket-Version: 13\r\n"
            "Authorization: Bearer %s\r\n"
            "User-Agent: AgentBox-Session-Worker/%s\r\n\r\n"
        ) % (path, host_header, key, self.credential, VERSION)
        raw.sendall(request.encode("ascii"))
        response = b""
        while b"\r\n\r\n" not in response:
            chunk = raw.recv(4096)
            if not chunk:
                raise RuntimeError("websocket handshake closed")
            response += chunk
            if len(response) > 65536:
                raise RuntimeError("websocket handshake is too large")
        head, remaining = response.split(b"\r\n\r\n", 1)
        lines = head.decode("iso-8859-1").split("\r\n")
        if " 101 " not in lines[0]:
            raise RuntimeError("websocket handshake failed: " + lines[0])
        headers = {}
        for line in lines[1:]:
            if ":" in line:
                name, value = line.split(":", 1)
                headers[name.strip().lower()] = value.strip()
        expected = base64.b64encode(hashlib.sha1((key + WEBSOCKET_GUID).encode("ascii")).digest()).decode("ascii")
        if headers.get("sec-websocket-accept") != expected:
            raise RuntimeError("invalid websocket handshake response")
        if remaining:
            raise RuntimeError("unexpected websocket handshake payload")
        raw.settimeout(None)
        self.sock = raw

    def close(self):
        sock = self.sock
        self.sock = None
        if sock is not None:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            sock.close()

    def _recv_exact(self, length):
        data = b""
        while len(data) < length:
            chunk = self.sock.recv(length - len(data))
            if not chunk:
                raise EOFError("websocket closed")
            data += chunk
        return data

    def _send_frame(self, opcode, payload=b""):
        if isinstance(payload, str):
            payload = payload.encode("utf-8")
        mask = os.urandom(4)
        length = len(payload)
        if length < 126:
            header = bytes((0x80 | opcode, 0x80 | length))
        elif length <= 65535:
            header = bytes((0x80 | opcode, 0x80 | 126)) + struct.pack("!H", length)
        else:
            header = bytes((0x80 | opcode, 0x80 | 127)) + struct.pack("!Q", length)
        masked = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
        with self.send_lock:
            if self.sock is None:
                raise EOFError("websocket closed")
            self.sock.sendall(header + mask + masked)

    def send_json(self, value):
        self._send_frame(0x1, json.dumps(value, separators=(",", ":"), ensure_ascii=False))

    def receive_json(self):
        fragments = []
        message_opcode = None
        while True:
            first, second = self._recv_exact(2)
            final = bool(first & 0x80)
            opcode = first & 0x0F
            masked = bool(second & 0x80)
            length = second & 0x7F
            if length == 126:
                length = struct.unpack("!H", self._recv_exact(2))[0]
            elif length == 127:
                length = struct.unpack("!Q", self._recv_exact(8))[0]
            if length > 1024 * 1024:
                raise RuntimeError("websocket message exceeds 1 MiB")
            mask = self._recv_exact(4) if masked else None
            payload = self._recv_exact(length)
            if mask:
                payload = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
            if opcode == 0x8:
                raise EOFError("websocket closed")
            if opcode == 0x9:
                self._send_frame(0xA, payload)
                continue
            if opcode == 0xA:
                continue
            if opcode in (0x1, 0x2):
                message_opcode = opcode
                fragments = [payload]
            elif opcode == 0x0 and message_opcode is not None:
                fragments.append(payload)
            else:
                continue
            if not final:
                continue
            if message_opcode != 0x1:
                fragments = []
                message_opcode = None
                continue
            return json.loads(b"".join(fragments).decode("utf-8"))


def resize_pty(fd, columns, rows):
    columns = max(2, min(1000, int(columns or 120)))
    rows = max(1, min(500, int(rows or 30)))
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))


class TerminalSession:
    def __init__(self, manager, session_id, driver, target, columns, rows):
        self.manager = manager
        self.session_id = session_id
        self.driver = driver
        self.target = target
        self.master_fd, slave_fd = pty.openpty()
        resize_pty(self.master_fd, columns, rows)
        if driver == "docker":
            command = [
                "docker", "exec", "-it", "-u", "0:0", target,
                "env", "HOME=/root", "USER=root", "LOGNAME=root", "TERM=xterm-256color",
                "sh", "-c", "cd /root 2>/dev/null || cd /; if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi",
            ]
        else:
            command = [runtime_python(), RUNTIME_DRIVER, driver, "terminal", target]
        try:
            self.process = subprocess.Popen(
                command, stdin=slave_fd, stdout=slave_fd, stderr=slave_fd,
                close_fds=True, start_new_session=True,
            )
        finally:
            os.close(slave_fd)
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")
        self.closed = threading.Event()
        threading.Thread(target=self._pump_output, name="terminal-" + session_id, daemon=True).start()

    def _pump_output(self):
        try:
            while not self.closed.is_set():
                readable, _, _ = select.select([self.master_fd], [], [], 1)
                if not readable:
                    if self.process.poll() is not None:
                        break
                    continue
                try:
                    chunk = os.read(self.master_fd, 32768)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not chunk:
                    break
                data = self.decoder.decode(chunk)
                if data:
                    self.manager.send({"type": "output", "sessionId": self.session_id, "data": data})
        except Exception as error:
            self.manager.send({"type": "error", "sessionId": self.session_id, "error": str(error)})
        finally:
            self.close(False)
            self.manager.session_closed(self.session_id)

    def input(self, data):
        if not self.closed.is_set():
            os.write(self.master_fd, data.encode("utf-8"))

    def resize(self, columns, rows):
        if not self.closed.is_set():
            resize_pty(self.master_fd, columns, rows)

    def close(self, notify=True):
        if self.closed.is_set():
            return
        self.closed.set()
        try:
            os.close(self.master_fd)
        except OSError:
            pass
        if self.process.poll() is None:
            try:
                os.killpg(self.process.pid, signal.SIGTERM)
                self.process.wait(timeout=2)
            except Exception:
                try:
                    os.killpg(self.process.pid, signal.SIGKILL)
                except OSError:
                    pass
        if notify:
            self.manager.send({"type": "closed", "sessionId": self.session_id})


def valid_path(value):
    if not isinstance(value, str) or not value.startswith("/") or "\x00" in value or len(value) > 4096:
        raise RuntimeError("path must be an absolute container path")
    return posixpath.normpath(value)


def runtime_python():
    return RUNTIME_PYTHON if os.path.isfile(RUNTIME_PYTHON) else sys.executable


def runtime_inspect(driver, target):
    if driver == "docker":
        command = ["docker", "inspect", target]
    else:
        command = [runtime_python(), RUNTIME_DRIVER, driver, "inspect", target]
    subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True, timeout=15)


def runtime_run(driver, target, script, path, input_bytes=None, timeout=15, extra_args=None):
    if driver == "docker":
        command = ["docker", "exec", "-u", "0:0"]
        if input_bytes is not None:
            command.append("-i")
        command.extend([target, "sh", "-c", script, "sh", path])
    else:
        command = [runtime_python(), RUNTIME_DRIVER, driver, "exec"]
        if input_bytes is not None:
            command.append("--stdin")
        command.extend([target, "--", "sh", "-c", script, "sh", path])
    if extra_args:
        command.extend(extra_args)
    completed = subprocess.run(command, input=input_bytes, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    if completed.returncode != 0:
        raise RuntimeError(completed.stdout.decode("utf-8", "replace").strip() or "sandbox operation failed")
    return completed.stdout


def rpc_list(driver, target, path):
    output = runtime_run(driver, target, 'set -eu; test -d "$1" || { echo "directory not found: $1" >&2; exit 1; }; find "$1" -mindepth 1 -maxdepth 1 -printf "%y\\t%s\\t%T@\\t%p\\n" | sed -n "1,1000p"', path)
    entries = []
    for raw_line in output.decode("utf-8", "replace").splitlines():
        fields = raw_line.split("\t")
        if len(fields) < 4:
            continue
        full_path = "\t".join(fields[3:])
        entries.append({
            "type": "directory" if fields[0] == "d" else "file",
            "size": int(fields[1]) if fields[1].isdigit() else 0,
            "modifiedAt": float(fields[2]) if fields[2] else 0,
            "path": full_path,
            "name": posixpath.basename(full_path),
        })
    entries.sort(key=lambda item: (0 if item["type"] == "directory" else 1, item["name"].lower()))
    return entries


def rpc_read(driver, target, path):
    output = runtime_run(driver, target, 'set -eu; test -f "$1" || { echo "file not found: $1" >&2; exit 1; }; size=$(wc -c < "$1"); [ "$size" -le 524288 ] || { echo "file exceeds the 512 KiB editor limit" >&2; exit 1; }; cat "$1"', path)
    if b"\x00" in output:
        raise RuntimeError("binary files cannot be opened in the text editor")
    try:
        return output.decode("utf-8")
    except UnicodeDecodeError:
        raise RuntimeError("file is not valid UTF-8 text")


def rpc_write(driver, target, path, content):
    encoded = content.encode("utf-8")
    if len(encoded) > MAX_FILE_SIZE:
        raise RuntimeError("file exceeds the 512 KiB editor limit")
    runtime_run(driver, target, 'set -eu; target=$1; parent=${target%/*}; [ -n "$parent" ] || parent=/; mkdir -p "$parent"; temp="$target.agentbox.$$"; trap "rm -f \\$temp" EXIT; cat > "$temp"; mv "$temp" "$target"; trap - EXIT', path, encoded)
    return "saved"


def upload_temp_path(path, upload_id):
    if not isinstance(upload_id, str) or len(upload_id) != 36 or any(character not in "0123456789abcdef-" for character in upload_id.lower()):
        raise RuntimeError("invalid upload id")
    return path + ".agentbox-upload-" + upload_id


def rpc_upload_start(driver, target, path, upload_id):
    temp_path = upload_temp_path(path, upload_id)
    runtime_run(
        driver,
        target,
        'set -eu; final=$1; temp=$2; [ ! -e "$final" ] || { echo "file already exists: $final" >&2; exit 1; }; parent=${final%/*}; [ -n "$parent" ] || parent=/; mkdir -p "$parent"; rm -f "$temp"; : > "$temp"',
        path,
        extra_args=[temp_path],
    )
    return "started"


def rpc_upload_chunk(driver, target, path, upload_id, content):
    temp_path = upload_temp_path(path, upload_id)
    if not isinstance(content, str):
        raise RuntimeError("upload chunk is invalid")
    try:
        chunk = base64.b64decode(content, validate=True)
    except Exception:
        raise RuntimeError("upload chunk is not valid base64")
    if len(chunk) > MAX_UPLOAD_CHUNK_SIZE:
        raise RuntimeError("upload chunk exceeds 192 KiB")
    script = 'set -eu; target=$1; test -f "$target" || { echo "upload is not initialized" >&2; exit 1; }; cat >> "$target"; size=$(wc -c < "$target"); [ "$size" -le %d ] || { rm -f "$target"; echo "upload exceeds 50 MiB" >&2; exit 1; }' % MAX_UPLOAD_SIZE
    runtime_run(driver, target, script, temp_path, chunk, timeout=30)
    return "received"


def rpc_upload_finish(driver, target, path, upload_id):
    temp_path = upload_temp_path(path, upload_id)
    runtime_run(
        driver,
        target,
        'set -eu; final=$1; temp=$2; test -f "$temp" || { echo "upload is not initialized" >&2; exit 1; }; [ ! -e "$final" ] || { rm -f "$temp"; echo "file already exists: $final" >&2; exit 1; }; mv "$temp" "$final"',
        path,
        extra_args=[temp_path],
    )
    return "uploaded"


def rpc_upload_cancel(driver, target, path, upload_id):
    temp_path = upload_temp_path(path, upload_id)
    runtime_run(driver, target, 'rm -f "$1"', temp_path)
    return "cancelled"


class SessionManager:
    def __init__(self, websocket):
        self.websocket = websocket
        self.sessions = {}
        self.lock = threading.Lock()

    def send(self, message):
        try:
            self.websocket.send_json(message)
        except Exception:
            pass

    def open(self, message):
        session_id = message.get("sessionId", "")
        driver = message.get("driver", "")
        target = message.get("externalId", "")
        if not session_id or driver not in ("docker", "boxlite", "microsandbox") or not target:
            self.send({"type": "error", "sessionId": session_id, "error": "invalid sandbox session target"})
            return
        try:
            runtime_inspect(driver, target)
            session = TerminalSession(self, session_id, driver, target, message.get("cols"), message.get("rows"))
            with self.lock:
                previous = self.sessions.pop(session_id, None)
                self.sessions[session_id] = session
            if previous:
                previous.close(False)
            self.send({"type": "ready", "sessionId": session_id})
        except Exception as error:
            self.send({"type": "error", "sessionId": session_id, "error": str(error)})

    def session_closed(self, session_id):
        with self.lock:
            self.sessions.pop(session_id, None)
        self.send({"type": "closed", "sessionId": session_id})

    def handle_rpc(self, message):
        session_id = message.get("sessionId", "")
        request_id = message.get("requestId", "")
        with self.lock:
            session = self.sessions.get(session_id)
        if not session:
            self.send({"type": "rpc-result", "sessionId": session_id, "requestId": request_id, "error": "terminal session is not ready"})
            return
        try:
            path = valid_path(message.get("path"))
            operation = message.get("operation")
            if operation == "list":
                result = rpc_list(session.driver, session.target, path)
            elif operation == "read":
                result = rpc_read(session.driver, session.target, path)
            elif operation == "write":
                result = rpc_write(session.driver, session.target, path, message.get("content", ""))
            elif operation == "upload-start":
                result = rpc_upload_start(session.driver, session.target, path, message.get("uploadId", ""))
            elif operation == "upload-chunk":
                result = rpc_upload_chunk(session.driver, session.target, path, message.get("uploadId", ""), message.get("content", ""))
            elif operation == "upload-finish":
                result = rpc_upload_finish(session.driver, session.target, path, message.get("uploadId", ""))
            elif operation == "upload-cancel":
                result = rpc_upload_cancel(session.driver, session.target, path, message.get("uploadId", ""))
            else:
                raise RuntimeError("unsupported file operation")
            self.send({"type": "rpc-result", "sessionId": session_id, "requestId": request_id, "ok": True, "result": result})
        except Exception as error:
            self.send({"type": "rpc-result", "sessionId": session_id, "requestId": request_id, "error": str(error)[:4000]})

    def handle(self, message):
        message_type = message.get("type")
        if message_type == "open":
            self.open(message)
            return
        session_id = message.get("sessionId", "")
        with self.lock:
            session = self.sessions.get(session_id)
        if message_type == "rpc":
            threading.Thread(target=self.handle_rpc, args=(message,), daemon=True).start()
        elif message_type == "input" and session:
            session.input(message.get("data", ""))
        elif message_type == "resize" and session:
            session.resize(message.get("cols"), message.get("rows"))
        elif message_type == "close" and session:
            with self.lock:
                self.sessions.pop(session_id, None)
            session.close(False)

    def close_all(self):
        with self.lock:
            sessions = list(self.sessions.values())
            self.sessions.clear()
        for session in sessions:
            session.close(False)


def load_config(path):
    with open(path, "r", encoding="utf-8") as config:
        lines = [line.strip() for line in config.readlines()]
    if len(lines) < 3 or not all(lines[:3]):
        raise RuntimeError("worker configuration is invalid")
    return lines[0].rstrip("/"), lines[1], lines[2]


def websocket_url(server_url, server_id):
    if server_url.startswith("https://"):
        return "wss://" + server_url[8:] + "/api/servers/" + server_id + "/sessions/connect"
    if server_url.startswith("http://"):
        return "ws://" + server_url[7:] + "/api/servers/" + server_id + "/sessions/connect"
    raise RuntimeError("worker server URL must use http or https")


def main():
    config_path = sys.argv[1] if len(sys.argv) > 1 else "/etc/agentbox-worker.conf"
    delay = 1
    while True:
        websocket = None
        manager = None
        try:
            server_url, server_id, credential = load_config(config_path)
            websocket = WebSocket(websocket_url(server_url, server_id), credential)
            websocket.connect()
            manager = SessionManager(websocket)
            delay = 1
            while True:
                manager.handle(websocket.receive_json())
        except Exception as error:
            print("session connection:", error, file=sys.stderr, flush=True)
        finally:
            if manager:
                manager.close_all()
            if websocket:
                websocket.close()
        time.sleep(delay)
        delay = min(delay * 2, 10)


if __name__ == "__main__":
    main()
`
