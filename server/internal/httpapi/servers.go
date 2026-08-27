package httpapi

import (
	"net/http"
	"strings"

	"agentbox/internal/platform"
)

func (s *Server) listServers(w http.ResponseWriter, request *http.Request) {
	servers, err := s.store.ListServers(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.observeServerStatuses(servers)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"servers":       servers,
		"workerVersion": s.workerVersion,
	})
}

// trackServerStatus 跟踪服务器在线状态，仅在状态变化时返回 true。
// 首次观察到某台服务器只登记状态，避免进程重启后误报一轮上线/离线。
func (s *Server) trackServerStatus(serverID, status string) bool {
	s.serverStatesMu.Lock()
	defer s.serverStatesMu.Unlock()
	if s.serverStates == nil {
		return false
	}
	previous, known := s.serverStates[serverID]
	s.serverStates[serverID] = status
	return known && previous != status
}

// observeServerStatuses 在服务器列表刷新时记录上线/离线事件（系统事件，无操作者）。
func (s *Server) observeServerStatuses(servers []platform.ManagedServer) {
	for _, server := range servers {
		if !s.trackServerStatus(server.ID, server.Status) {
			continue
		}
		entry := platform.LogEntry{
			Level: platform.LogLevelInfo, Category: platform.LogCategoryServer, Action: "online",
			Message:      "服务器上线：" + server.Name,
			ResourceKind: "server", ResourceID: server.ID, ResourceName: server.Name,
		}
		if server.Status != "online" {
			entry.Level = platform.LogLevelWarn
			entry.Action = "offline"
			entry.Message = "服务器离线：" + server.Name
		}
		s.recordLog(nil, entry)
	}
}

func (s *Server) createServerPairing(w http.ResponseWriter, request *http.Request) {
	pairing, err := s.store.CreateServerPairing(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"pairing": pairing})
}

func (s *Server) getServerPairing(w http.ResponseWriter, request *http.Request) {
	pairing, err := s.store.GetServerPairing(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"pairing": pairing})
}

func (s *Server) registerServer(w http.ResponseWriter, request *http.Request) {
	var input platform.ServerRegistration
	if !s.decodeJSON(w, request, &input) {
		return
	}
	server, credential, err := s.store.RegisterServer(request.Context(), input)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryServer, Action: "register",
			Message: "注册服务器 " + input.Name + " 失败", Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.trackServerStatus(server.ID, "online")
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryServer, Action: "register",
		Message:      "注册服务器 " + server.Name,
		ResourceKind: "server", ResourceID: server.ID, ResourceName: server.Name,
		Detail: map[string]any{"hostname": server.Hostname, "os": server.OS, "arch": server.Arch},
	})
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"credential": credential,
		"server":     server,
	})
}

func (s *Server) heartbeatServer(w http.ResponseWriter, request *http.Request) {
	credential := authBearer(request)
	var capabilities []string
	var inventory *platform.ServerInventory
	workerVersion := ""
	if request.ContentLength != 0 {
		var body struct {
			Capabilities  []string                  `json:"capabilities"`
			Inventory     *platform.ServerInventory `json:"inventory"`
			WorkerVersion string                    `json:"workerVersion"`
		}
		if !s.decodeJSON(w, request, &body) {
			return
		}
		if len(body.Capabilities) > 32 {
			s.writeError(w, http.StatusBadRequest, "服务器能力数量过多")
			return
		}
		capabilities = body.Capabilities
		inventory = body.Inventory
		workerVersion = strings.TrimSpace(body.WorkerVersion)
		if len(workerVersion) > 64 {
			s.writeError(w, http.StatusBadRequest, "Worker 版本过长")
			return
		}
		if inventory != nil {
			platform.NormalizeServerInventory(inventory)
			if err := platform.ValidateServerInventory(*inventory); err != nil {
				s.handleError(w, err)
				return
			}
		}
	}
	if err := s.store.HeartbeatServer(request.Context(), request.PathValue("id"), credential, capabilities, inventory, workerVersion); err != nil {
		s.handleError(w, err)
		return
	}
	// 心跳本身不记日志，仅在离线后恢复上线时记录一次。
	if s.trackServerStatus(request.PathValue("id"), "online") {
		s.recordLog(nil, platform.LogEntry{
			Category: platform.LogCategoryServer, Action: "online",
			Message:      "服务器恢复上线",
			ResourceKind: "server", ResourceID: request.PathValue("id"),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateWorker(w http.ResponseWriter, request *http.Request) {
	var body struct {
		Version string `json:"version"`
	}
	if request.ContentLength != 0 && !s.decodeJSON(w, request, &body) {
		return
	}
	targetVersion := strings.TrimSpace(body.Version)
	if targetVersion == "" {
		targetVersion = s.workerVersion
	}
	if !workerVersionPattern.MatchString(targetVersion) || !strings.HasPrefix(targetVersion, "v") {
		s.writeError(w, http.StatusServiceUnavailable, "Server 未配置可发布的 Worker 版本")
		return
	}
	if targetVersion != s.workerVersion {
		s.writeError(w, http.StatusBadRequest, "只能更新到当前 Server 配套的 Worker 版本")
		return
	}
	if err := s.store.EnqueueWorkerUpdate(request.Context(), request.PathValue("id"), targetVersion); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryServer, Action: "update-worker",
			Message: "下发 Worker 更新失败", Status: platform.LogStatusFailed,
			ResourceKind: "server", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error(), "version": targetVersion},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryServer, Action: "update-worker",
		Message:      "下发 Worker 更新：" + targetVersion,
		ResourceKind: "server", ResourceID: request.PathValue("id"),
		Detail: map[string]any{"version": targetVersion},
	})
	s.writeJSON(w, http.StatusAccepted, map[string]string{"version": targetVersion})
}

func (s *Server) deleteServer(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteServer(request.Context(), request.PathValue("id")); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryServer, Action: "delete",
			Message: "删除服务器 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "server", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryServer, Action: "delete",
		Message:      "删除服务器 " + request.PathValue("id"),
		ResourceKind: "server", ResourceID: request.PathValue("id"),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workerInstallScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(workerInstall))
}

const workerInstall = `#!/bin/sh
set -eu

SERVER_URL=${1:-}
case "$SERVER_URL" in
  http://*|https://*) ;;
  *) echo "usage: install.sh <agentbox-url>" >&2; exit 2 ;;
esac
case "$SERVER_URL" in
  https://*|http://localhost*|http://127.0.0.1*|http://\[::1\]*) ;;
  http://*)
    echo "============================================================================" >&2
    echo "WARNING: $SERVER_URL uses plain HTTP." >&2
    echo "The Worker binary, server credentials, and sandbox secrets will cross the" >&2
    echo "network unencrypted. Use HTTPS (or an SSH tunnel) for any real deployment." >&2
    echo "============================================================================" >&2
    ;;
esac

verify_worker_checksum() {
  BINARY=$1
  HEADERS=$2
  EXPECTED_SHA256=$(tr -d '\r' < "$HEADERS" |
    sed -n 's/^[Xx]-[Cc]hecksum-[Ss]ha256:[[:space:]]*\([0-9A-Fa-f]\{1,\}\).*/\1/p' | head -n 1)
  if [ -z "$EXPECTED_SHA256" ]; then
    echo "warning: the server did not provide a Worker checksum; skipping integrity verification" >&2
    return 0
  fi
  if ! command -v sha256sum >/dev/null; then
    echo "sha256sum is required to verify the downloaded Worker binary" >&2
    exit 1
  fi
  ACTUAL_SHA256=$(sha256sum "$BINARY" | cut -d ' ' -f 1)
  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    echo "Worker binary checksum mismatch (expected $EXPECTED_SHA256, got $ACTUAL_SHA256); refusing to install" >&2
    exit 1
  fi
}

worker_tmp=$(mktemp)
worker_headers_tmp=$(mktemp)
microsandbox_source_tmp=$(mktemp)
microsandbox_build_tmp=$(mktemp -d)
boxlite_tmp=$(mktemp -d)
trap 'rm -f "$worker_tmp" "$worker_headers_tmp" "$microsandbox_source_tmp"; rm -rf "$microsandbox_build_tmp" "$boxlite_tmp"' EXIT
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64; BOXLITE_ARCH=x86_64 ;;
  aarch64|arm64) ARCH=arm64; BOXLITE_ARCH=aarch64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
curl -fsSL -D "$worker_headers_tmp" "$SERVER_URL/api/worker/agentbox-worker?arch=$ARCH" -o "$worker_tmp"
verify_worker_checksum "$worker_tmp" "$worker_headers_tmp"
curl -fsSL "$SERVER_URL/api/worker/agentbox-microsandbox-driver.go" -o "$microsandbox_source_tmp"

install_host_dependencies() {
  if command -v jq >/dev/null &&
     { ! command -v docker >/dev/null || command -v skopeo >/dev/null; }; then
    return 0
  fi
  if ! command -v apt-get >/dev/null; then
    echo "jq is required; automatic installation currently supports apt-based Linux" >&2
    exit 1
  fi
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  PACKAGES="ca-certificates jq"
  command -v docker >/dev/null && PACKAGES="$PACKAGES skopeo"
  apt-get install -y $PACKAGES
}

install_host_dependencies
install -m 0755 "$worker_tmp" /usr/local/bin/agentbox-worker
install -d -m 0755 /usr/local/lib/agentbox
install -d -m 0700 /var/lib/agentbox-worker
if [ ! -e /etc/agentbox-worker-runtime.env ]; then
  install -m 0600 /dev/null /etc/agentbox-worker-runtime.env
fi
rm -f /usr/local/bin/agentbox-session-worker
rm -f /usr/local/lib/agentbox/session_worker.py /usr/local/lib/agentbox/runtime_driver.py
rm -rf /opt/agentbox/runtime

migrate_existing_config() {
  CONFIG=/etc/agentbox-worker.conf
  [ -s "$CONFIG" ] || return 0
  SERVER_ID=$(sed -n '2p' "$CONFIG")
  CREDENTIAL=$(sed -n '3p' "$CONFIG")
  if [ -z "$SERVER_ID" ] || [ -z "$CREDENTIAL" ]; then
    echo "warning: existing Worker config is invalid; endpoint was not changed" >&2
    return 0
  fi
  umask 077
  printf '%s\n%s\n%s\n' "$SERVER_URL" "$SERVER_ID" "$CREDENTIAL" > "$CONFIG"
}

install_microvm_build_dependencies() {
  [ -c /dev/kvm ] || return 0
  if command -v go >/dev/null && command -v gcc >/dev/null; then
    return 0
  fi
  if ! command -v apt-get >/dev/null; then
    echo "Go 1.22+ and a C compiler are required to build the Microsandbox driver" >&2
    exit 1
  fi
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y golang-go build-essential
}

install_boxlite_cli() {
  [ -c /dev/kvm ] || return 0
  if ! command -v tar >/dev/null || ! command -v sha256sum >/dev/null; then
    if ! command -v apt-get >/dev/null; then
      echo "warning: tar and sha256sum are required for BoxLite; skipping BoxLite" >&2
      return 0
    fi
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y ca-certificates coreutils tar
  fi
  BOXLITE_VERSION=0.9.7
  BOXLITE_ASSET="boxlite-cli-v${BOXLITE_VERSION}-${BOXLITE_ARCH}-unknown-linux-gnu.tar.gz"
  BOXLITE_URL="https://github.com/boxlite-ai/boxlite/releases/download/v${BOXLITE_VERSION}/${BOXLITE_ASSET}"
  if ! (
    set -eu
    curl -fsSL "$BOXLITE_URL" -o "$boxlite_tmp/$BOXLITE_ASSET"
    curl -fsSL "$BOXLITE_URL.sha256" -o "$boxlite_tmp/$BOXLITE_ASSET.sha256"
    cd "$boxlite_tmp"
    sha256sum -c "$BOXLITE_ASSET.sha256"
    tar -xzf "$BOXLITE_ASSET"
    BOXLITE_BINARY=$(find . -type f -name boxlite | head -n 1)
    [ -n "$BOXLITE_BINARY" ]
    install -m 0755 "$BOXLITE_BINARY" /usr/local/bin/boxlite
    /usr/local/bin/boxlite --version
  ); then
    echo "warning: BoxLite CLI installation failed; Docker remains available" >&2
  fi
}

install_microsandbox_driver() {
  [ -c /dev/kvm ] || return 0
  cp "$microsandbox_source_tmp" "$microsandbox_build_tmp/main.go"
  cat > "$microsandbox_build_tmp/go.mod" <<'EOF'
module agentbox/microsandbox-driver

go 1.22

require github.com/superradcompany/microsandbox/sdk/go v0.6.8
EOF
  (
    cd "$microsandbox_build_tmp"
    if ! go mod tidy; then
      GOPROXY="${AGENTBOX_GOPROXY:-https://goproxy.cn,direct}" go mod tidy
    fi
    CGO_ENABLED=1 go build -tags agentbox_driver -trimpath -ldflags '-s -w' -o agentbox-microsandbox-driver .
  )
  install -m 0755 "$microsandbox_build_tmp/agentbox-microsandbox-driver" /usr/local/bin/agentbox-microsandbox-driver
  /usr/local/bin/agentbox-microsandbox-driver probe >/dev/null
}

install_microvm_build_dependencies
install_boxlite_cli
install_microsandbox_driver
migrate_existing_config
if command -v systemctl >/dev/null && systemctl is-active --quiet agentbox-worker.service; then
  systemctl restart agentbox-worker.service
fi
echo "AgentBox Worker installed. Runtime capabilities will be published after self-test."
`

// WorkerDaemonScript returns the job runner embedded in the Go Worker binary.
func WorkerDaemonScript() string {
	return workerDaemon
}

const workerDaemon = `#!/bin/sh
set -eu

CONFIG=/etc/agentbox-worker.conf
STATE_DIR=/var/lib/agentbox-worker
BOXLITE_CONFIG=$STATE_DIR/boxlite.json
BOXLITE_URL=http://127.0.0.1:48100
BOXLITE_LOG=$STATE_DIR/boxlite-serve.log
BOXLITE_IMAGES_FILE=$STATE_DIR/boxlite-images.json
WORKER_OCI_IMAGE_DIR=$STATE_DIR/oci-images
BOXLITE_SERVER_PID_FILE=$STATE_DIR/boxlite-serve.pid

usage() {
  echo "usage: agentbox-worker setup --server <url> --token <pairing-token>" >&2
  echo "       agentbox-worker run|status" >&2
  exit 2
}

setup_worker() {
  [ "$(id -u)" = 0 ] || { echo "setup must run as root" >&2; exit 1; }
  SERVER_URL=
  TOKEN=
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --server) SERVER_URL=${2:-}; shift 2 ;;
      --token) TOKEN=${2:-}; shift 2 ;;
      *) usage ;;
    esac
  done
  case "$SERVER_URL" in http://*|https://*) ;; *) usage ;; esac
  [ -n "$TOKEN" ] || usage
  command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
  command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
  command -v systemctl >/dev/null || { echo "systemd is required" >&2; exit 1; }

  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
  if [ ! -s "$STATE_DIR/server.id" ]; then
    cat /proc/sys/kernel/random/uuid > "$STATE_DIR/server.id"
    chmod 600 "$STATE_DIR/server.id"
  fi
  SERVER_ID=$(tr -d '\r\n' < "$STATE_DIR/server.id")
  HOST=$(hostname 2>/dev/null | tr -cd 'A-Za-z0-9._-' | cut -c1-255)
  [ -n "$HOST" ] || HOST=linux-server
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  CAPS=$(worker_capabilities)
  PREVIOUS_CREDENTIAL=
  if [ -s "$CONFIG" ]; then
    PREVIOUS_CREDENTIAL=$(sed -n '3p' "$CONFIG" | tr -d '\r\n')
  fi
  BODY=$(jq -n --arg pairingToken "$TOKEN" --arg serverId "$SERVER_ID" \
    --arg name "$HOST" --arg hostname "$HOST" --arg arch "$ARCH" --argjson capabilities "$CAPS" \
    --arg previousCredential "$PREVIOUS_CREDENTIAL" \
    '{pairingToken:$pairingToken,serverId:$serverId,name:$name,hostname:$hostname,os:"linux",arch:$arch,capabilities:$capabilities}
     + (if $previousCredential == "" then {} else {previousCredential:$previousCredential} end)')
  RESPONSE=$(curl -fsS -X POST "$SERVER_URL/api/servers/register" -H 'Content-Type: application/json' --data "$BODY")
  CREDENTIAL=$(printf '%s' "$RESPONSE" | sed -n 's/.*"credential":"\([^"]*\)".*/\1/p')
  [ -n "$CREDENTIAL" ] || { echo "registration response was invalid" >&2; exit 1; }

  printf '%s\n%s\n%s\n' "$SERVER_URL" "$SERVER_ID" "$CREDENTIAL" > "$CONFIG"
  chmod 600 "$CONFIG"
  cat > /etc/systemd/system/agentbox-worker.service <<'UNIT'
[Unit]
Description=AgentBox physical server worker
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-/etc/agentbox-worker-runtime.env
ExecStart=/usr/local/bin/agentbox-worker run
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable --now agentbox-worker.service
  echo "Server connected to AgentBox."
}

prepare_boxlite_config() {
  mkdir -p "$STATE_DIR"
  REGISTRY_FILE=$(mktemp "$STATE_DIR/boxlite-registries.XXXXXX")
  CONFIG_FILE=$(mktemp "$STATE_DIR/boxlite-config.XXXXXX")
  {
    printf '%s' "${AGENTBOX_IMAGE_REGISTRIES:-}" | tr ',' '\n'
    if [ -r /etc/docker/daemon.json ]; then
      jq -r '."registry-mirrors"[]? // empty' /etc/docker/daemon.json 2>/dev/null || true
    fi
    printf '%s\n' docker.io
  } | while IFS= read -r REGISTRY; do
    REGISTRY=$(printf '%s' "$REGISTRY" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' \
      -e 's#^https\{0,1\}://##' -e 's#/*$##')
    case "$REGISTRY" in ''|*/*|*[!A-Za-z0-9.:-]*) continue ;; esac
    printf '%s\n' "$REGISTRY"
  done > "$REGISTRY_FILE"
  jq -Rn '[inputs | select(length > 0)] | reduce .[] as $host ([]; if index($host) then . else . + [$host] end) | {image_registries: map({host: ., search: true})}' \
    < "$REGISTRY_FILE" > "$CONFIG_FILE"
  chmod 600 "$CONFIG_FILE"
  mv "$CONFIG_FILE" "$BOXLITE_CONFIG"
  rm -f "$REGISTRY_FILE"
}

boxlite_server_ready() {
  curl -fsS "$BOXLITE_URL/v1/config" 2>/dev/null |
    jq -e '.capabilities.snapshots_enabled == true' >/dev/null 2>&1
}

stop_boxlite_server() {
  BOXLITE_SERVER_PID=
  if [ -s "$BOXLITE_SERVER_PID_FILE" ]; then
    BOXLITE_SERVER_PID=$(tr -d '\r\n' < "$BOXLITE_SERVER_PID_FILE")
  fi
  case "$BOXLITE_SERVER_PID" in ''|*[!0-9]*) rm -f "$BOXLITE_SERVER_PID_FILE"; return 0 ;; esac
  if kill -0 "$BOXLITE_SERVER_PID" >/dev/null 2>&1 &&
     tr '\000' ' ' < "/proc/$BOXLITE_SERVER_PID/cmdline" 2>/dev/null |
       grep -Fq '/usr/local/bin/boxlite'; then
    kill "$BOXLITE_SERVER_PID" >/dev/null 2>&1 || true
    ATTEMPT=0
    while kill -0 "$BOXLITE_SERVER_PID" >/dev/null 2>&1 && [ "$ATTEMPT" -lt 20 ]; do
      ATTEMPT=$((ATTEMPT + 1))
      sleep 1
    done
    kill -9 "$BOXLITE_SERVER_PID" >/dev/null 2>&1 || true
    wait "$BOXLITE_SERVER_PID" >/dev/null 2>&1 || true
  fi
  rm -f "$BOXLITE_SERVER_PID_FILE"
}

ensure_boxlite_server() {
  [ -c /dev/kvm ] && [ -x /usr/local/bin/boxlite ] || return 0
  boxlite_server_ready && return 0
  stop_boxlite_server
  prepare_boxlite_config
  : > "$BOXLITE_LOG"
  /usr/local/bin/boxlite --config "$BOXLITE_CONFIG" serve \
    --host 127.0.0.1 --port 48100 >> "$BOXLITE_LOG" 2>&1 &
  BOXLITE_SERVER_PID=$!
  printf '%s\n' "$BOXLITE_SERVER_PID" > "$BOXLITE_SERVER_PID_FILE"
  ATTEMPT=0
  while [ "$ATTEMPT" -lt 20 ]; do
    boxlite_server_ready && return 0
    kill -0 "$BOXLITE_SERVER_PID" >/dev/null 2>&1 || break
    ATTEMPT=$((ATTEMPT + 1))
    sleep 1
  done
  rm -f "$BOXLITE_SERVER_PID_FILE"
  tail -c 3500 "$BOXLITE_LOG" >&2 || true
  return 1
}

boxlite_cli() {
  /usr/local/bin/boxlite --url "$BOXLITE_URL" "$@"
}

boxlite_local_cli() {
  [ -s "$BOXLITE_CONFIG" ] || prepare_boxlite_config
  /usr/local/bin/boxlite --config "$BOXLITE_CONFIG" "$@"
}

worker_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    aarch64|arm64) printf 'arm64' ;;
    *) uname -m ;;
  esac
}

refresh_boxlite_images() {
  IMAGES_FILE=$(mktemp "$STATE_DIR/boxlite-images.XXXXXX") || return 1
  CACHED_IMAGES_FILE=$(mktemp "$STATE_DIR/boxlite-cached-images.XXXXXX") || { rm -f "$IMAGES_FILE"; return 1; }
  LOCAL_IMAGES_FILE=$(mktemp "$STATE_DIR/worker-oci-images.XXXXXX") || {
    rm -f "$IMAGES_FILE" "$CACHED_IMAGES_FILE"
    return 1
  }
  printf '[]\n' > "$CACHED_IMAGES_FILE"
  printf '[]\n' > "$LOCAL_IMAGES_FILE"
  if ! [ -c /dev/kvm ] || ! [ -x /usr/local/bin/boxlite ]; then
    printf '[]\n' > "$IMAGES_FILE"
    mv "$IMAGES_FILE" "$BOXLITE_IMAGES_FILE"
    rm -f "$CACHED_IMAGES_FILE" "$LOCAL_IMAGES_FILE"
    return 0
  fi
  if boxlite_local_cli images --all --format json 2>/dev/null | jq -e --arg arch "$(worker_arch)" '
    def humanbytes:
      . as $raw | (try tonumber catch null) as $bytes |
      if $bytes == null then ($raw | tostring)
      elif $bytes < 1024 then (($bytes | tostring) + " B")
      elif $bytes < 1048576 then (((($bytes / 1024) * 10 | round) / 10 | tostring) + " KiB")
      elif $bytes < 1073741824 then (((($bytes / 1048576) * 10 | round) / 10 | tostring) + " MiB")
      else (((($bytes / 1073741824) * 10 | round) / 10 | tostring) + " GiB") end;
    if type != "array" then error("BoxLite image inventory is not an array") else
      (map({
        id: (.ID // ""),
        reference: (if (.Repository // "") == "" or .Repository == "<none>" then (.ID // "")
          elif (.Tag // "") == "" or .Tag == "<none>" then .Repository
          else (.Repository + ":" + .Tag) end),
        architecture: $arch,
        size: ((.size // .Size // "") | humanbytes),
        created: (.CreatedAt // ""),
        format: "oci",
        path: "",
        source: "registry-cache"
      }) | unique_by(.id, .reference))
    end
  ' > "$CACHED_IMAGES_FILE"; then
    if [ -d "$WORKER_OCI_IMAGE_DIR" ]; then
      find "$WORKER_OCI_IMAGE_DIR" -mindepth 2 -maxdepth 2 -type f -name '.agentbox-image.json' \
        -exec cat {} \; 2>/dev/null | jq -s '
          [ .[] | select(type == "object") | . as $item |
            ($item.references // [])[] | {
              id: ($item.id // ""),
              reference: .,
              architecture: ($item.architecture // ""),
              size: ($item.size // ""),
              created: ($item.created // ""),
              format: ($item.format // "oci"),
              path: ($item.path // ""),
              source: "worker-oci"
            }
          ]
        ' > "$LOCAL_IMAGES_FILE" || printf '[]\n' > "$LOCAL_IMAGES_FILE"
    fi
    if jq -s 'add | unique_by(.source, .id, .reference)' \
      "$CACHED_IMAGES_FILE" "$LOCAL_IMAGES_FILE" > "$IMAGES_FILE"; then
      mv "$IMAGES_FILE" "$BOXLITE_IMAGES_FILE"
      rm -f "$CACHED_IMAGES_FILE" "$LOCAL_IMAGES_FILE"
      return 0
    fi
  fi
  rm -f "$IMAGES_FILE" "$CACHED_IMAGES_FILE" "$LOCAL_IMAGES_FILE"
  return 1
}

worker_local_oci_image() {
  IMAGE=$1
  command -v docker >/dev/null || return 2
  timeout 10 docker info >/dev/null 2>&1 || return 2
  IMAGE_JSON=$(command docker image inspect "$IMAGE" 2>/dev/null) || return 2
  IMAGE_ID=$(printf '%s' "$IMAGE_JSON" | jq -r '.[0].Id // empty')
  IMAGE_OS=$(printf '%s' "$IMAGE_JSON" | jq -r '.[0].Os // empty')
  IMAGE_ARCH=$(printf '%s' "$IMAGE_JSON" | jq -r '.[0].Architecture // empty')
  [ "$IMAGE_OS" = linux ] || { echo "OCI sandbox runtimes require a Linux image; $IMAGE is $IMAGE_OS" >&2; return 1; }
  [ "$IMAGE_ARCH" = "$(worker_arch)" ] || {
    echo "OCI image architecture mismatch: $IMAGE is $IMAGE_ARCH, Worker is $(worker_arch)" >&2
    return 1
  }
  IMAGE_KEY=${IMAGE_ID#sha256:}
  case "$IMAGE_KEY" in ''|*[!0-9a-f]*) echo "Docker returned an invalid image ID for $IMAGE" >&2; return 1 ;; esac
  mkdir -p "$WORKER_OCI_IMAGE_DIR"
  chmod 700 "$WORKER_OCI_IMAGE_DIR"
  ROOTFS_PATH=$WORKER_OCI_IMAGE_DIR/$IMAGE_KEY
  if [ -s "$ROOTFS_PATH/oci-layout" ] && [ -s "$ROOTFS_PATH/index.json" ]; then
    /usr/local/bin/agentbox-worker image-to-oci \
      --output "$ROOTFS_PATH" --reference "$IMAGE" >/dev/null || return 1
    printf '%s' "$ROOTFS_PATH"
    return 0
  fi
  if ! command -v skopeo >/dev/null; then
    if ! command -v apt-get >/dev/null; then
      echo "skopeo is required to convert a local Docker image without a full temporary archive" >&2
      return 1
    fi
    export DEBIAN_FRONTEND=noninteractive
    apt-get update >/dev/null && apt-get install -y skopeo >/dev/null || return 1
  fi
  TEMP_ROOTFS=$(mktemp -d "$WORKER_OCI_IMAGE_DIR/.skopeo-XXXXXX") || return 1
  if skopeo copy --insecure-policy "docker-daemon:$IMAGE" "oci:$TEMP_ROOTFS:agentbox" >/dev/null &&
     rm -rf "$ROOTFS_PATH" &&
     mv "$TEMP_ROOTFS" "$ROOTFS_PATH" &&
     /usr/local/bin/agentbox-worker image-to-oci \
       --output "$ROOTFS_PATH" --reference "$IMAGE" >/dev/null; then
    printf '%s' "$ROOTFS_PATH"
    return 0
  else
    STATUS=$?
  fi
  rm -rf "$TEMP_ROOTFS" "$ROOTFS_PATH"
  return "$STATUS"
}

boxlite_start() {
  TARGET=$1
  START_LOG=$(mktemp "$STATE_DIR/boxlite-start.XXXXXX") || return 1
  if boxlite_cli start "$TARGET" 2>"$START_LOG"; then
    rm -f "$START_LOG"
    return 0
  fi
  if grep -Fq 'Handle invalidated after stop()' "$START_LOG"; then
    rm -f "$START_LOG"
    stop_boxlite_server
    ensure_boxlite_server
    boxlite_cli start "$TARGET"
    return
  fi
  cat "$START_LOG" >&2
  rm -f "$START_LOG"
  return 1
}

boxlite_prepare_image() {
  IMAGE=$1
  stop_boxlite_server
  if ROOTFS_PATH=$(worker_local_oci_image "$IMAGE"); then
    refresh_boxlite_images || true
    ensure_boxlite_server
    return
  else
    STATUS=$?
  fi
  if [ "$STATUS" -ne 2 ]; then
    ensure_boxlite_server || true
    return "$STATUS"
  fi
  if boxlite_local_cli pull "$IMAGE"; then
    refresh_boxlite_images || true
    ensure_boxlite_server
    return
  else
    STATUS=$?
  fi
  ensure_boxlite_server || true
  return "$STATUS"
}

microsandbox_prepare_image() {
  IMAGE=$1
  if runtime_call microsandbox images 2>/dev/null |
      jq -e --arg reference "$IMAGE" 'any(.[]; .reference == $reference)' >/dev/null 2>&1; then
    return 0
  fi
  if OCI_PATH=$(worker_local_oci_image "$IMAGE"); then
    OCI_ARCHIVE=$(mktemp "$STATE_DIR/microsandbox-oci.XXXXXX.tar") || return 1
    if tar -C "$OCI_PATH" -cf "$OCI_ARCHIVE" oci-layout index.json blobs &&
       runtime_call microsandbox prepare-image "$IMAGE" --archive "$OCI_ARCHIVE"; then
      STATUS=0
    else
      STATUS=$?
    fi
    rm -f "$OCI_ARCHIVE"
    return "$STATUS"
  else
    STATUS=$?
  fi
  [ "$STATUS" -eq 2 ] || return "$STATUS"
  runtime_call microsandbox prepare-image "$IMAGE"
}

boxlite_create_with_rootfs() {
  TARGET=$1; IMAGE=$2; ROOTFS_PATH=$3; CPUS=$4; MEMORY=$5; DISK_SIZE=$6; NETWORK=$7; ALLOW_NET=$8
  BODY=$(jq -n --arg name "$TARGET" --arg image "$IMAGE" --arg rootfsPath "$ROOTFS_PATH" \
    --argjson cpus "$CPUS" --argjson memory "$MEMORY" --argjson diskSize "$DISK_SIZE" \
    --arg network "$NETWORK" --argjson allowNet "$ALLOW_NET" '
      {
        name: $name,
        image: $image,
        rootfs_path: $rootfsPath,
        cpus: $cpus,
        memory_mib: $memory,
        disk_size_gb: $diskSize,
        working_dir: "/",
        network: {mode: $network, allow_net: $allowNet},
        detach: true
      }
    ')
  curl -fsS -X POST "$BOXLITE_URL/v1/boxes" -H 'Content-Type: application/json' --data "$BODY"
}

boxlite_inspect_status() {
  jq -r '
    if type == "array" then
      (.[0].Status // .[0].status // .[0].State.Status // .[0].state.status // "")
    else
      (.Status // .status // .State.Status // .state.status // "")
    end
  '
}

boxlite_inspect_usable() {
  BOX_JSON=$(boxlite_cli inspect "$1") || return 1
  BOX_STATUS=$(printf '%s' "$BOX_JSON" | boxlite_inspect_status)
  printf '%s\n' "$BOX_JSON"
  case "$BOX_STATUS" in
    failed|error) return 1 ;;
  esac
}

boxlite_delete() {
  TARGET=$1
  if ! HTTP_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' "$BOXLITE_URL/v1/boxes/$TARGET"); then
    echo "BoxLite service is unavailable" >&2
    return 1
  fi
  case "$HTTP_STATUS" in
    404) return 0 ;;
    200) ;;
    *) echo "BoxLite inspect returned HTTP $HTTP_STATUS" >&2; return 1 ;;
  esac
  boxlite_cli rm --force "$TARGET" >/dev/null || return 1
  if ! HTTP_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' "$BOXLITE_URL/v1/boxes/$TARGET"); then
    echo "BoxLite service is unavailable after deleting $TARGET" >&2
    return 1
  fi
  [ "$HTTP_STATUS" = 404 ] || {
    echo "BoxLite did not remove $TARGET (inspect returned HTTP $HTTP_STATUS)" >&2
    return 1
  }
}

install_boxlite_guest() {
  TARGET=$1
  WORKDIR=${2:-/workspace}
  if boxlite_cli exec -u 0:0 "$TARGET" -- \
      /opt/agentbox/agentbox-guest guest-fs exists /opt/agentbox/agentbox-guest >/dev/null 2>&1; then
    boxlite_cli exec -u 0:0 "$TARGET" -- \
      /opt/agentbox/agentbox-guest guest-fs mkdir "$WORKDIR"
    return
  fi

  # BoxLite 0.9.7's REST upload endpoint buffers only small request bodies.
  # Provision the self-contained guest helper through the local runtime, then
  # restore the shared server used by concurrent terminal and file sessions.
  stop_boxlite_server
  if boxlite_local_cli cp /usr/local/bin/agentbox-worker \
      "$TARGET:/opt/agentbox/agentbox-guest" &&
     boxlite_local_cli exec -u 0:0 "$TARGET" -- \
      /opt/agentbox/agentbox-guest guest-fs mkdir "$WORKDIR"; then
    ensure_boxlite_server
    return
  else
    STATUS=$?
  fi
  ensure_boxlite_server || true
  return "$STATUS"
}

boxlite_call() {
  ACTION=${1:-}
  [ "$#" -gt 0 ] && shift
  case "$ACTION" in
    probe)
      if [ "${AGENTBOX_BOXLITE_SERVER_MODE:-}" = 1 ]; then
        [ -c /dev/kvm ] && [ -x /usr/local/bin/boxlite ] && boxlite_server_ready
      else
        [ -c /dev/kvm ] && [ -x /usr/local/bin/boxlite ] && boxlite_local_cli info
      fi
      ;;
    create)
      TARGET=${1:-}; IMAGE=${2:-}; shift 2
      CPUS=2; MEMORY=4096; DISK_SIZE=${AGENTBOX_BOXLITE_DISK_SIZE_GB:-20}; WORKDIR=/workspace; NETWORK=enabled; ALLOW_NET='[]'
      while [ "$#" -gt 0 ]; do
        case "$1" in
          --cpus) CPUS=${2:-2}; shift 2 ;;
          --memory) MEMORY=${2:-4096}; shift 2 ;;
          --disk-size) DISK_SIZE=${2:-20}; shift 2 ;;
          --workdir) WORKDIR=${2:-/workspace}; shift 2 ;;
          --network) [ "${2:-}" = none ] && NETWORK=disabled || NETWORK=enabled; shift 2 ;;
          --allow-net) ALLOW_NET=$(printf '%s' "$ALLOW_NET" | jq --arg host "${2:-}" '. + [$host] | unique'); shift 2 ;;
          *) echo "unsupported BoxLite create option: $1" >&2; return 2 ;;
        esac
      done
      case "$DISK_SIZE" in ''|*[!0-9]*|0) echo "invalid BoxLite disk size: $DISK_SIZE" >&2; return 2 ;; esac
      BOX_EXISTS=false
      if BOX_JSON=$(boxlite_cli inspect "$TARGET" 2>/dev/null); then
        BOX_STATUS=$(printf '%s' "$BOX_JSON" | boxlite_inspect_status)
        case "$BOX_STATUS" in
          failed|error)
            boxlite_delete "$TARGET" || {
              echo "BoxLite could not remove failed sandbox $TARGET" >&2
              return 1
            }
            ;;
          *) BOX_EXISTS=true ;;
        esac
      fi
      if [ "$BOX_EXISTS" = false ]; then
        if ROOTFS_PATH=$(worker_local_oci_image "$IMAGE"); then
          boxlite_create_with_rootfs "$TARGET" "$IMAGE" "$ROOTFS_PATH" \
            "$CPUS" "$MEMORY" "$DISK_SIZE" "$NETWORK" "$ALLOW_NET" >/dev/null
        else
          STATUS=$?
          if [ "$STATUS" -ne 2 ]; then
            return "$STATUS"
          fi
          set -- boxlite_cli create --detach --name "$TARGET" --cpus "$CPUS" --memory "$MEMORY" \
            --disk-size "$DISK_SIZE" --workdir / --network "$NETWORK"
          for ALLOWED_HOST in $(printf '%s' "$ALLOW_NET" | jq -r '.[]'); do
            set -- "$@" --allow-net "$ALLOWED_HOST"
          done
          set -- "$@" "$IMAGE"
          "$@" >/dev/null
        fi
      fi
      boxlite_start "$TARGET" || return 1
      install_boxlite_guest "$TARGET" "$WORKDIR"
      ;;
    inspect) boxlite_inspect_usable "$1" ;;
    prepare-image) boxlite_prepare_image "$1" ;;
    start) boxlite_start "$1" ;;
    stop) boxlite_cli stop "$1" ;;
    delete) boxlite_delete "$1" ;;
    exec)
      USE_STDIN=false; WORKDIR=
      while [ "$#" -gt 0 ]; do
        case "$1" in
          --stdin) USE_STDIN=true; shift ;;
          --workdir) WORKDIR=${2:-}; shift 2 ;;
          *) break ;;
        esac
      done
      TARGET=${1:-}; [ -n "$TARGET" ] || { echo "BoxLite exec target is required" >&2; return 2; }; shift
      [ "${1:-}" = -- ] && shift
      if [ "$USE_STDIN" = true ] && [ -n "$WORKDIR" ]; then
        boxlite_cli exec -i -u 0:0 -w "$WORKDIR" "$TARGET" -- "$@"
      elif [ "$USE_STDIN" = true ]; then
        boxlite_cli exec -i -u 0:0 "$TARGET" -- "$@"
      elif [ -n "$WORKDIR" ]; then
        boxlite_cli exec -u 0:0 -w "$WORKDIR" "$TARGET" -- "$@"
      else
        boxlite_cli exec -u 0:0 "$TARGET" -- "$@"
      fi
      ;;
    fs-list|fs-read|fs-write|fs-mkdir|fs-stat|fs-exists|fs-remove)
      TARGET=${1:-}; PATH_VALUE=${2:-}
      [ -n "$TARGET" ] && [ -n "$PATH_VALUE" ] || { echo "$ACTION requires target and path" >&2; return 2; }
      HELPER_ACTION=${ACTION#fs-}
      if [ "$ACTION" = fs-write ]; then
        boxlite_cli exec -i -u 0:0 "$TARGET" -- /opt/agentbox/agentbox-guest guest-fs "$HELPER_ACTION" "$PATH_VALUE"
      else
        boxlite_cli exec -u 0:0 "$TARGET" -- /opt/agentbox/agentbox-guest guest-fs "$HELPER_ACTION" "$PATH_VALUE"
      fi
      ;;
    fs-rename)
      boxlite_cli exec -u 0:0 "$1" -- /opt/agentbox/agentbox-guest guest-fs rename "$2" "$3"
      ;;
    fs-copy-from-host)
      boxlite_cli cp "$2" "$1:$3"
      ;;
    *) echo "unsupported BoxLite action: $ACTION" >&2; return 2 ;;
  esac
}

runtime_call() {
  DRIVER=${1:-}
  [ "$#" -gt 0 ] && shift
  case "$DRIVER" in
    boxlite) boxlite_call "$@" ;;
    microsandbox)
      [ -x /usr/local/bin/agentbox-microsandbox-driver ] || { echo "AgentBox Microsandbox Go driver is unavailable" >&2; return 1; }
      /usr/local/bin/agentbox-microsandbox-driver "$@"
      ;;
    *) echo "unsupported runtime driver: $DRIVER" >&2; return 2 ;;
  esac
}

runtime_probe() {
  runtime_call "$1" probe >/dev/null 2>&1
}

docker() {
  if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = docker ]; then
    command docker "$@"
    return
  fi
  OPERATION=${1:-}
  [ "$#" -gt 0 ] && shift
  case "$OPERATION" in
    inspect)
      runtime_call "$AGENTBOX_RUNTIME_DRIVER" inspect "$1"
      ;;
    exec)
      USE_STDIN=false
      WORKDIR=
      while [ "$#" -gt 0 ]; do
        case "$1" in
          -i|-it|-ti) USE_STDIN=true; shift ;;
          -t) shift ;;
          -u|--user) shift 2 ;;
          -w|--workdir) WORKDIR=${2:-}; shift 2 ;;
          --) shift; break ;;
          -*) echo "unsupported runtime exec option: $1" >&2; return 2 ;;
          *) break ;;
        esac
      done
      TARGET=${1:-}
      [ -n "$TARGET" ] || { echo "runtime exec target is required" >&2; return 2; }
      shift
      if [ "$USE_STDIN" = true ] && [ -n "$WORKDIR" ]; then
        runtime_call "$AGENTBOX_RUNTIME_DRIVER" exec --stdin --workdir "$WORKDIR" "$TARGET" -- "$@"
      elif [ "$USE_STDIN" = true ]; then
        runtime_call "$AGENTBOX_RUNTIME_DRIVER" exec --stdin "$TARGET" -- "$@"
      elif [ -n "$WORKDIR" ]; then
        runtime_call "$AGENTBOX_RUNTIME_DRIVER" exec --workdir "$WORKDIR" "$TARGET" -- "$@"
      else
        runtime_call "$AGENTBOX_RUNTIME_DRIVER" exec "$TARGET" -- "$@"
      fi
      ;;
    cp)
      SOURCE=${1:-}
      DESTINATION=${2:-}
      case "$DESTINATION" in
        *:/*)
          TARGET=${DESTINATION%%:*}
          PATH_VALUE=${DESTINATION#*:}
          runtime_call "$AGENTBOX_RUNTIME_DRIVER" fs-copy-from-host "$TARGET" "$SOURCE" "$PATH_VALUE"
          ;;
        *) echo "only host-to-sandbox copy is supported" >&2; return 2 ;;
      esac
      ;;
    *) echo "unsupported compatibility operation for $AGENTBOX_RUNTIME_DRIVER: $OPERATION" >&2; return 2 ;;
  esac
}

worker_capabilities() {
  CAPS=
  command -v docker >/dev/null && timeout 10 docker info >/dev/null 2>&1 && CAPS='"docker"'
  if [ -c /dev/kvm ]; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"kvm-device\""
  fi
  if runtime_probe boxlite; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"boxlite\""
  fi
  if runtime_probe microsandbox; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"microsandbox\""
  fi
  if [ -x /usr/local/bin/agentbox-worker ]; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"interactive-session\""
  fi
  printf '[%s]' "$CAPS"
}

worker_inventory() {
	ARCH=$(worker_arch)
	DOCKER_IMAGES='[]'
  if command -v docker >/dev/null && timeout 10 docker info >/dev/null 2>&1; then
    DOCKER_IMAGES=$(docker image ls --no-trunc --format '{{json .}}' 2>/dev/null | jq -s --arg arch "$ARCH" '
      map(select((.Repository | startswith("agentbox/runtime-")) | not) | {
        id: .ID,
        reference: (if .Repository == "<none>" or .Tag == "<none>" then .ID else (.Repository + ":" + .Tag) end),
        architecture: $arch,
        size: .Size,
        created: .CreatedSince,
        format: "oci",
        path: "",
        source: "docker-local"
      })
    ' 2>/dev/null || printf '[]')
  fi

  BOXLITE_IMAGES='[]'
  if [ -s "$BOXLITE_IMAGES_FILE" ]; then
    BOXLITE_IMAGES=$(jq -c 'if type == "array" then . else [] end' "$BOXLITE_IMAGES_FILE" 2>/dev/null || printf '[]')
  fi

  MICROSANDBOX_IMAGES='[]'
  if [ -x /usr/local/bin/agentbox-microsandbox-driver ] && [ -c /dev/kvm ]; then
    MICROSANDBOX_IMAGES=$(runtime_call microsandbox images 2>/dev/null || printf '[]')
    MICROSANDBOX_IMAGES=$(printf '%s' "$MICROSANDBOX_IMAGES" | jq -c 'if type == "array" then . else [] end' 2>/dev/null || printf '[]')
  fi

  VM_DIR=${AGENTBOX_VM_IMAGE_DIR:-/var/lib/agentbox/vm-images}
  VM_FILE=$(mktemp)
  printf '[]\n' > "$VM_FILE"
  if [ -d "$VM_DIR" ]; then
    find "$VM_DIR" -maxdepth 1 -type f \( -name '*.qcow2' -o -name '*.raw' -o -name '*.img' \) -print | \
      while IFS= read -r PATH_VALUE; do
        [ -n "$PATH_VALUE" ] || continue
        NAME=$(basename "$PATH_VALUE")
        SIZE=$(du -h "$PATH_VALUE" | awk '{print $1}')
        FORMAT=${NAME##*.}
        CREATED=$(date -r "$PATH_VALUE" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || true)
        NEXT_FILE="$VM_FILE.next"
        jq --arg id "$PATH_VALUE" --arg reference "$NAME" --arg arch "$ARCH" \
          --arg size "$SIZE" --arg created "$CREATED" --arg format "$FORMAT" --arg path "$PATH_VALUE" \
          '. + [{id:$id,reference:$reference,architecture:$arch,size:$size,created:$created,format:$format,path:$path,source:"disk-local"}]' \
          "$VM_FILE" > "$NEXT_FILE"
        mv "$NEXT_FILE" "$VM_FILE"
      done
  fi
  VM_IMAGES=$(cat "$VM_FILE")
  rm -f "$VM_FILE"
  jq -n --argjson dockerImages "$DOCKER_IMAGES" --argjson boxliteImages "$BOXLITE_IMAGES" \
    --argjson microsandboxImages "$MICROSANDBOX_IMAGES" --argjson vmImages "$VM_IMAGES" \
    --arg vmImageDirectory "$VM_DIR" \
    '{dockerImages:$dockerImages,boxliteImages:$boxliteImages,microsandboxImages:$microsandboxImages,vmImages:$vmImages,vmImageDirectory:$vmImageDirectory}'
}

complete_job() {
  JOB_ID=$1
  SUCCESS=$2
  EXTERNAL_ID=$3
  MESSAGE=$4
  EXIT_CODE=${5:-}
  OUTPUT_FILE=${6:-/dev/null}
  OUTPUT_TRUNCATED=${7:-false}
  TIMED_OUT=${8:-false}
  BODY=$(jq -n \
    --argjson success "$SUCCESS" \
    --arg externalId "$EXTERNAL_ID" \
    --arg message "$MESSAGE" \
    --arg exitCode "$EXIT_CODE" \
    --rawfile output "$OUTPUT_FILE" \
    --argjson outputTruncated "$OUTPUT_TRUNCATED" \
    --argjson timedOut "$TIMED_OUT" \
    '{success:$success,externalId:$externalId,message:$message,
      exitCode:(if $exitCode == "" then null else ($exitCode | tonumber) end),
      output:$output,outputTruncated:$outputTruncated,timedOut:$timedOut}')
  curl -fsS -X POST \
    "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/complete" \
    -H "Authorization: Bearer $CREDENTIAL" \
    -H 'Content-Type: application/json' \
    --data "$BODY" >/dev/null
}

report_job_progress() {
  [ -n "${JOB_ID:-}" ] || return 0
  STAGE=$1
  MESSAGE=$2
  CACHE_STATUS=${3:-}
  CACHE_REASON=${4:-}
  AGENT_TOOL=${5:-}
  AGENT_TOOL_STATUS=${6:-}
  BODY=$(jq -n --arg stage "$STAGE" --arg message "$MESSAGE" \
    --arg cacheStatus "$CACHE_STATUS" --arg cacheReason "$CACHE_REASON" \
    --arg agentTool "$AGENT_TOOL" --arg agentToolStatus "$AGENT_TOOL_STATUS" \
    '{stage:$stage,message:$message,
      cacheStatus:(if $cacheStatus == "" then null else $cacheStatus end),
      cacheReason:(if $cacheReason == "" then null else $cacheReason end),
      agentTool:(if $agentTool == "" then null else $agentTool end),
      agentToolStatus:(if $agentToolStatus == "" then null else $agentToolStatus end)}') || return 0
  curl -fsS -X POST \
    "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/progress" \
    -H "Authorization: Bearer $CREDENTIAL" \
    -H 'Content-Type: application/json' \
    --data "$BODY" >/dev/null 2>&1 || true
}

report_agent_tool_progress() {
  TOOL=$1
  STATUS=$2
  MESSAGE=$3
  report_job_progress "$PROGRESS_STAGE" "$MESSAGE" "" "" "$TOOL" "$STATUS"
}

finalize_worker_update() {
  MARKER="$STATE_DIR/worker-update.json"
  [ -s "$MARKER" ] || return 0
  JOB_ID=$(jq -r '.jobId // empty' "$MARKER")
  TARGET_VERSION=$(jq -r '.targetVersion // empty' "$MARKER")
  ROLLBACK_SCRIPT=$(jq -r '.rollbackScript // empty' "$MARKER")
  [ -n "$JOB_ID" ] && [ -n "$TARGET_VERSION" ] || return 0
  CURRENT_VERSION=$(/usr/local/bin/agentbox-worker version 2>/dev/null || printf unknown)
  CONFIRMED="$STATE_DIR/worker-update-confirmed"
  if [ "$CURRENT_VERSION" = "$TARGET_VERSION" ]; then
    # Confirm only after the Server has accepted the completion report: if this
    # process is alive but cannot talk to the Server (protocol mismatch), the
    # pending rollback must still fire.
    if complete_job "$JOB_ID" true "" "Worker 已更新到 $TARGET_VERSION"; then
      # The rollback script may have been generated by an older Worker and can
      # race with this process. Once the Server accepts the new Worker, seal the
      # accepted binary as the fallback so a late rollback cannot downgrade it.
      install -m 0755 /usr/local/bin/agentbox-worker \
        /usr/local/lib/agentbox/agentbox-worker.previous || return 1
      printf '%s\n' "$JOB_ID" > "$CONFIRMED"
      if systemctl stop "agentbox-worker-update-$JOB_ID.timer" \
          "agentbox-worker-update-$JOB_ID.service" >/dev/null 2>&1; then
        rm -f "$MARKER" "$CONFIRMED"
        [ -z "$ROLLBACK_SCRIPT" ] || rm -f "$ROLLBACK_SCRIPT"
      else
        # If systemd could not cancel the scheduled check, keep the confirmed
        # marker until that check observes it. Removing only the job marker also
        # makes a not-yet-started check exit without restoring the old binary.
        rm -f "$MARKER"
      fi
    fi
  elif complete_job "$JOB_ID" false "" "Worker 更新已回滚，当前版本 $CURRENT_VERSION"; then
    rm -f "$MARKER" "$CONFIRMED"
    [ -z "$ROLLBACK_SCRIPT" ] || rm -f "$ROLLBACK_SCRIPT"
  fi
}

restore_worker_binary() {
  PREVIOUS=$1
  RESTORE_TMP=/usr/local/bin/.agentbox-worker-restore
  install -m 0755 "$PREVIOUS" "$RESTORE_TMP"
  mv -f "$RESTORE_TMP" /usr/local/bin/agentbox-worker
}

refresh_microsandbox_driver() {
  [ -c /dev/kvm ] || return 0
  command -v go >/dev/null || { echo "Go is required to update the Microsandbox driver" >&2; return 1; }
  command -v gcc >/dev/null || { echo "a C compiler is required to update the Microsandbox driver" >&2; return 1; }
  install -d -m 0700 "$STATE_DIR/go" "$STATE_DIR/go-mod-cache" "$STATE_DIR/go-build-cache"
  BUILD_DIR=$(mktemp -d "$STATE_DIR/microsandbox-driver.XXXXXX") || return 1
  if ! curl -fsSL "$SERVER_URL/api/worker/agentbox-microsandbox-driver.go" -o "$BUILD_DIR/main.go"; then
    rm -rf "$BUILD_DIR"
    return 1
  fi
  cat > "$BUILD_DIR/go.mod" <<'EOF'
module agentbox/microsandbox-driver

go 1.22

require github.com/superradcompany/microsandbox/sdk/go v0.6.8
EOF
  if ! (
    cd "$BUILD_DIR"
    export GOPATH="$STATE_DIR/go"
    export GOMODCACHE="$STATE_DIR/go-mod-cache"
    export GOCACHE="$STATE_DIR/go-build-cache"
    if ! go mod tidy; then
      GOPROXY="${AGENTBOX_GOPROXY:-https://goproxy.cn,direct}" go mod tidy
    fi
    CGO_ENABLED=1 go build -tags agentbox_driver -trimpath -ldflags '-s -w' -o agentbox-microsandbox-driver .
    ./agentbox-microsandbox-driver probe >/dev/null
  ); then
    rm -rf "$BUILD_DIR"
    return 1
  fi
  DRIVER_NEXT=/usr/local/bin/.agentbox-microsandbox-driver-next
  install -m 0755 "$BUILD_DIR/agentbox-microsandbox-driver" "$DRIVER_NEXT" || {
    rm -rf "$BUILD_DIR" "$DRIVER_NEXT"
    return 1
  }
  mv -f "$DRIVER_NEXT" /usr/local/bin/agentbox-microsandbox-driver
  rm -rf "$BUILD_DIR"
}

update_worker() {
  JOB_FILE=$1
  JOB_ID=$(jq -r '.job.id' "$JOB_FILE")
  TARGET_VERSION=$(jq -r '.job.payload.version // empty' "$JOB_FILE")
  case "$TARGET_VERSION" in
    v[0-9A-Za-z]*) ;;
    *) echo "invalid Worker target version" >&2; return 1 ;;
  esac
  case "$TARGET_VERSION" in
    *[!A-Za-z0-9._-]*) echo "invalid Worker target version" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; return 1 ;;
  esac

  WORKER_TMP=$(mktemp "$STATE_DIR/agentbox-worker.XXXXXX") || return 1
  WORKER_HEADERS=$(mktemp "$STATE_DIR/agentbox-worker-headers.XXXXXX") || { rm -f "$WORKER_TMP"; return 1; }
  if ! curl -fsSL -D "$WORKER_HEADERS" "$SERVER_URL/api/worker/agentbox-worker?arch=$ARCH&version=$TARGET_VERSION" -o "$WORKER_TMP"; then
    rm -f "$WORKER_TMP" "$WORKER_HEADERS"
    return 1
  fi
  # Verify integrity before executing anything from the downloaded binary.
  EXPECTED_SHA256=$(tr -d '\r' < "$WORKER_HEADERS" |
    sed -n 's/^[Xx]-[Cc]hecksum-[Ss]ha256:[[:space:]]*\([0-9A-Fa-f]\{1,\}\).*/\1/p' | head -n 1)
  rm -f "$WORKER_HEADERS"
  if [ -n "$EXPECTED_SHA256" ]; then
    command -v sha256sum >/dev/null || {
      echo "sha256sum is required to verify the downloaded Worker" >&2
      rm -f "$WORKER_TMP"
      return 1
    }
    ACTUAL_SHA256=$(sha256sum "$WORKER_TMP" | cut -d ' ' -f 1)
    if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
      echo "downloaded Worker checksum mismatch (expected $EXPECTED_SHA256, got $ACTUAL_SHA256)" >&2
      rm -f "$WORKER_TMP"
      return 1
    fi
  else
    echo "warning: the server did not provide a Worker checksum; skipping integrity verification" >&2
  fi
  chmod 0755 "$WORKER_TMP" || { rm -f "$WORKER_TMP"; return 1; }
  DOWNLOADED_VERSION=$("$WORKER_TMP" version 2>/dev/null || true)
  [ "$DOWNLOADED_VERSION" = "$TARGET_VERSION" ] || {
    echo "downloaded Worker version mismatch: $DOWNLOADED_VERSION" >&2
    rm -f "$WORKER_TMP"
    return 1
  }
  refresh_microsandbox_driver || {
    rm -f "$WORKER_TMP"
    echo "failed to refresh the Microsandbox driver" >&2
    return 1
  }

  install -d -m 0755 /usr/local/lib/agentbox
  PREVIOUS=/usr/local/lib/agentbox/agentbox-worker.previous
  PREVIOUS_TMP=/usr/local/lib/agentbox/.agentbox-worker.previous
  install -m 0755 /usr/local/bin/agentbox-worker "$PREVIOUS_TMP" || { rm -f "$WORKER_TMP"; return 1; }
  mv -f "$PREVIOUS_TMP" "$PREVIOUS" || { rm -f "$WORKER_TMP"; return 1; }
  NEXT=/usr/local/bin/.agentbox-worker-next
  install -m 0755 "$WORKER_TMP" "$NEXT" || { rm -f "$WORKER_TMP"; return 1; }
  mv -f "$NEXT" /usr/local/bin/agentbox-worker || { rm -f "$WORKER_TMP" "$NEXT"; return 1; }
  rm -f "$STATE_DIR/worker-update-confirmed"

  MARKER="$STATE_DIR/worker-update.json"
  ROLLBACK_SCRIPT="$STATE_DIR/worker-update-rollback-$JOB_ID.sh"
  MARKER_TMP="$STATE_DIR/.worker-update.json"
  jq -n --arg jobId "$JOB_ID" --arg targetVersion "$TARGET_VERSION" \
    --arg rollbackScript "$ROLLBACK_SCRIPT" \
    '{jobId:$jobId,targetVersion:$targetVersion,rollbackScript:$rollbackScript}' > "$MARKER_TMP" || {
      restore_worker_binary "$PREVIOUS"
      rm -f "$WORKER_TMP"
      return 1
    }
  mv -f "$MARKER_TMP" "$MARKER" || {
    restore_worker_binary "$PREVIOUS"
    rm -f "$WORKER_TMP" "$MARKER_TMP"
    return 1
  }
  cat > "$ROLLBACK_SCRIPT" <<'ROLLBACK'
#!/bin/sh
set -eu
MARKER=$1
PREVIOUS=$2
TARGET_VERSION=$3
STATE_DIR=/var/lib/agentbox-worker
JOB_ID=$(jq -r '.jobId // empty' "$MARKER" 2>/dev/null || true)
[ -n "$JOB_ID" ] || { rm -f "$STATE_DIR/worker-update-confirmed" "$0"; exit 0; }
if [ -s "$STATE_DIR/worker-update-confirmed" ] &&
   [ "$(cat "$STATE_DIR/worker-update-confirmed")" = "$JOB_ID" ] &&
   systemctl is-active --quiet agentbox-worker.service &&
   [ "$(/usr/local/bin/agentbox-worker version 2>/dev/null || true)" = "$TARGET_VERSION" ]; then
  rm -f "$MARKER" "$STATE_DIR/worker-update-confirmed" "$0"
  exit 0
fi
RESTORE_TMP=/usr/local/bin/.agentbox-worker-restore
install -m 0755 "$PREVIOUS" "$RESTORE_TMP"
mv -f "$RESTORE_TMP" /usr/local/bin/agentbox-worker
systemctl restart agentbox-worker.service
rm -f "$0"
ROLLBACK
  chmod 0700 "$ROLLBACK_SCRIPT" || {
    restore_worker_binary "$PREVIOUS"
    rm -f "$WORKER_TMP" "$MARKER" "$ROLLBACK_SCRIPT"
    return 1
  }
  if ! systemd-run --quiet --unit="agentbox-worker-update-$JOB_ID" --on-active=20s \
      "$ROLLBACK_SCRIPT" "$MARKER" "$PREVIOUS" "$TARGET_VERSION"; then
    restore_worker_binary "$PREVIOUS"
    rm -f "$WORKER_TMP" "$MARKER" "$ROLLBACK_SCRIPT"
    echo "failed to schedule Worker rollback" >&2
    return 1
  fi
  if ! systemd-run --quiet --unit="agentbox-worker-restart-$JOB_ID" --on-active=1s \
      /bin/systemctl restart agentbox-worker.service; then
    restore_worker_binary "$PREVIOUS"
    rm -f "$WORKER_TMP" "$MARKER"
    echo "failed to schedule Worker restart" >&2
    return 1
  fi
  rm -f "$WORKER_TMP"
}

agent_tool_command() {
  case "$1" in
    antigravity) printf '%s' agy ;;
    claude-code) printf '%s' claude ;;
    codebuddy) printf '%s' codebuddy ;;
    codex) printf '%s' codex ;;
    deepseek-harness) printf '%s' dsh ;;
    copilot-cli) printf '%s' copilot ;;
    cursor) printf '%s' cursor-agent ;;
    deveco) printf '%s' deveco ;;
    gemini-cli) printf '%s' gemini ;;
    grok) printf '%s' grok ;;
    kimi) printf '%s' kimi ;;
    omp) printf '%s' omp ;;
    openclaw) printf '%s' openclaw ;;
    opencode) printf '%s' opencode ;;
    pi) printf '%s' pi ;;
    qoder-cli) printf '%s' qodercli ;;
    qoder-cn) printf '%s' qoderclicn ;;
    qwen-code) printf '%s' qwen ;;
    qwenpaw) printf '%s' qwenpaw ;;
    reasonix) printf '%s' reasonix ;;
    trae-cli) printf '%s' traecli ;;
    *) return 1 ;;
  esac
}

recover_boxlite_agent_tool_install() {
  CONTAINER=$1
  [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ] || return 1
  echo "BoxLite portal disconnected while installing Agent tools; restarting the sandbox and retrying once" >&2
  RESTART_LOG=$(mktemp "$STATE_DIR/boxlite-agent-tool-restart.XXXXXX") || return 1
  if boxlite_cli restart "$CONTAINER" >/dev/null 2>"$RESTART_LOG"; then
    rm -f "$RESTART_LOG"
    return 0
  fi
  if grep -Fq 'Handle invalidated after stop()' "$RESTART_LOG"; then
    rm -f "$RESTART_LOG"
    stop_boxlite_server
    ensure_boxlite_server || return 1
    boxlite_cli start "$CONTAINER" >/dev/null
    return
  fi
  cat "$RESTART_LOG" >&2
  rm -f "$RESTART_LOG"
  return 1
}

agent_tool_exec() {
  CONTAINER=$1
  shift
  if docker exec "$CONTAINER" "$@"; then
    return 0
  fi
  recover_boxlite_agent_tool_install "$CONTAINER" || return 1
  docker exec "$CONTAINER" "$@"
}

agent_tool_exec_stdin() {
  CONTAINER=$1
  INPUT_FILE=$2
  shift 2
  if docker exec -i "$CONTAINER" "$@" < "$INPUT_FILE"; then
    return 0
  fi
  recover_boxlite_agent_tool_install "$CONTAINER" || return 1
  docker exec -i "$CONTAINER" "$@" < "$INPUT_FILE"
}

agent_tool_exec_logged() {
  CONTAINER=$1
  LABEL=$2
  shift 2
  if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" != boxlite ]; then
    agent_tool_exec "$CONTAINER" "$@"
    return
  fi
  LOG_FILE="/tmp/agentbox-$LABEL-install.log"
  STATUS_FILE="$LOG_FILE.status"
  PID_FILE="$LOG_FILE.pid"
  INSTALL_ATTEMPT=1
  while [ "$INSTALL_ATTEMPT" -le 2 ]; do
    if ! agent_tool_exec "$CONTAINER" sh -lc \
        'LOG_FILE=$1; STATUS_FILE=$2; PID_FILE=$3; shift 3; rm -f "$LOG_FILE" "$STATUS_FILE" "$PID_FILE"; (trap "" HUP; "$@"; CODE=$?; printf "%s\n" "$CODE" >"$STATUS_FILE") >"$LOG_FILE" 2>&1 </dev/null & printf "%s\n" "$!" >"$PID_FILE"' \
        agentbox "$LOG_FILE" "$STATUS_FILE" "$PID_FILE" "$@"; then
      return 1
    fi
    DEADLINE=$(($(date +%s) + ${AGENTBOX_AGENT_TOOL_INSTALL_TIMEOUT_SECONDS:-1800}))
    PORTAL_FAILED=false
    while [ "$(date +%s)" -lt "$DEADLINE" ]; do
      if ! STATUS=$(docker exec "$CONTAINER" sh -lc 'test -f "$1" && cat "$1" || true' agentbox "$STATUS_FILE"); then
        PORTAL_FAILED=true
        break
      fi
      case "$STATUS" in
        0)
          docker exec "$CONTAINER" rm -f "$LOG_FILE" "$STATUS_FILE" "$PID_FILE" >/dev/null 2>&1 || true
          return 0
          ;;
        '' ) sleep "${AGENTBOX_AGENT_TOOL_POLL_SECONDS:-15}" ;;
        * )
          docker exec "$CONTAINER" sh -lc 'test ! -f "$1" || tail -c 3000 "$1"' agentbox "$LOG_FILE" >&2 || true
          return 1
          ;;
      esac
    done
    if [ "$PORTAL_FAILED" = true ]; then
      recover_boxlite_agent_tool_install "$CONTAINER" || return 1
      if [ "$INSTALL_ATTEMPT" -lt 2 ]; then
        INSTALL_ATTEMPT=$((INSTALL_ATTEMPT + 1))
        echo "Retrying Agent tool installation from the sandbox cache: $LABEL" >&2
        continue
      fi
      return 1
    fi
    docker exec "$CONTAINER" sh -lc 'test ! -s "$1" || kill "$(cat "$1")" 2>/dev/null || true; test ! -f "$2" || tail -c 3000 "$2"' \
      agentbox "$PID_FILE" "$LOG_FILE" >&2 || true
    echo "Agent tool installation timed out after ${AGENTBOX_AGENT_TOOL_INSTALL_TIMEOUT_SECONDS:-1800} seconds: $LABEL" >&2
    return 1
  done
  return 1
}

install_boxlite_opencode() {
  CONTAINER=$1
  case "$(uname -m)" in
    x86_64|amd64) PLATFORM_PACKAGE=opencode-linux-x64 ;;
    aarch64|arm64) PLATFORM_PACKAGE=opencode-linux-arm64 ;;
    *) echo "OpenCode does not provide a BoxLite binary for $(uname -m)" >&2; return 1 ;;
  esac
  if docker exec "$CONTAINER" sh -lc 'ldd --version 2>&1 || true' | grep -qi musl; then
    PLATFORM_PACKAGE="$PLATFORM_PACKAGE-musl"
  fi
  TMP_DIR=$(mktemp -d "$STATE_DIR/opencode.XXXXXX") || return 1
  META_FILE="$TMP_DIR/metadata.json"
  ARCHIVE="$TMP_DIR/opencode.tgz"
  if ! curl --connect-timeout 15 --max-time 60 -fsSL \
      "https://registry.npmjs.org/$PLATFORM_PACKAGE/latest" -o "$META_FILE"; then
    rm -rf "$TMP_DIR"
    return 1
  fi
  PACKAGE_NAME=$(jq -r '.name // empty' "$META_FILE")
  TARBALL_URL=$(jq -r '.dist.tarball // empty' "$META_FILE")
  INTEGRITY=$(jq -r '.dist.integrity // empty' "$META_FILE")
  if [ "$PACKAGE_NAME" != "$PLATFORM_PACKAGE" ] || [ -z "$TARBALL_URL" ] ||
     [ "${INTEGRITY#sha512-}" = "$INTEGRITY" ]; then
    echo "OpenCode npm metadata is incomplete for $PLATFORM_PACKAGE" >&2
    rm -rf "$TMP_DIR"
    return 1
  fi
  if ! curl --connect-timeout 15 --max-time 300 -fsSL "$TARBALL_URL" -o "$ARCHIVE"; then
    rm -rf "$TMP_DIR"
    return 1
  fi
  EXPECTED_SHA512=$(printf '%s' "${INTEGRITY#sha512-}" | base64 -d | od -An -tx1 | tr -d ' \n')
  ACTUAL_SHA512=$(sha512sum "$ARCHIVE" | cut -d ' ' -f 1)
  if [ -z "$EXPECTED_SHA512" ] || [ "$ACTUAL_SHA512" != "$EXPECTED_SHA512" ]; then
    echo "OpenCode npm package integrity check failed" >&2
    rm -rf "$TMP_DIR"
    return 1
  fi
  if ! tar -xzf "$ARCHIVE" -C "$TMP_DIR" package/bin/opencode ||
     ! chmod 0755 "$TMP_DIR/package/bin/opencode"; then
    rm -rf "$TMP_DIR"
    return 1
  fi
  stop_boxlite_server
  if boxlite_local_cli cp "$TMP_DIR/package/bin/opencode" "$CONTAINER:/usr/local/bin/opencode"; then
    STATUS=0
  else
    STATUS=$?
  fi
  rm -rf "$TMP_DIR"
  ensure_boxlite_server || { [ "$STATUS" -ne 0 ] || STATUS=$?; }
  [ "$STATUS" -eq 0 ] || return "$STATUS"
  agent_tool_exec "$CONTAINER" test -x /usr/local/bin/opencode
}

install_agent_tools() {
  CONTAINER=$1
  JOB_FILE=$2
  PROGRESS_STAGE=${AGENTBOX_AGENT_TOOL_PROGRESS_STAGE:-agent-tools}
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE" | sort -u)
  [ -n "$TOOLS" ] || return 0
  report_job_progress "$PROGRESS_STAGE" "正在安装 Agent 工具依赖"
  if ! agent_tool_exec_logged "$CONTAINER" prerequisites sh -lc \
      'export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get install -y curl ca-certificates git jq unzip python3' >&2; then
    echo "Agent tool prerequisite installation failed" >&2
    return 1
  fi
  INSTALL_PI=false
  INSTALL_OPENCODE=false
  set --
  for TOOL in $TOOLS; do
    COMMAND=$(agent_tool_command "$TOOL") || { echo "unsupported Agent tool: $TOOL" >&2; return 1; }
    if [ "$TOOL" != pi ] && docker exec "$CONTAINER" sh -lc "command -v $COMMAND >/dev/null"; then
      report_agent_tool_progress "$TOOL" cached "已复用 Agent 工具缓存：$TOOL"
      continue
    fi
    case "$TOOL" in
      antigravity|cursor|qwenpaw) continue ;;
      codex) PACKAGE='@openai/codex' ;;
      claude-code) PACKAGE='@anthropic-ai/claude-code' ;;
      codebuddy) PACKAGE='@tencent-ai/codebuddy-code' ;;
      deepseek-harness) PACKAGE='@deepseek-ai/dsh@0.1.0-rc.7' ;;
      gemini-cli) PACKAGE='@google/gemini-cli' ;;
      grok) continue ;;
      kimi) PACKAGE='@moonshot-ai/kimi-code' ;;
      omp) PACKAGE='@oh-my-pi/pi-coding-agent' ;;
      openclaw) PACKAGE='openclaw' ;;
      opencode)
        if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ]; then
          INSTALL_OPENCODE=true
          continue
        fi
        PACKAGE='opencode-ai'
        ;;
      pi) INSTALL_PI=true; continue ;;
      copilot-cli) PACKAGE='@github/copilot' ;;
      qoder-cli) PACKAGE='@qoder-ai/qodercli' ;;
      qoder-cn) PACKAGE='@qodercn-ai/qoderclicn' ;;
      qwen-code) PACKAGE='@qwen-code/qwen-code' ;;
      reasonix) PACKAGE='reasonix' ;;
      deveco) echo "DevEco Code does not support Linux sandboxes; use a custom prebuilt environment" >&2; return 1 ;;
      trae-cli) echo "TRAE CLI has no verified unattended Linux installer; use a custom prebuilt environment" >&2; return 1 ;;
      *) echo "unsupported Agent tool: $TOOL" >&2; return 1 ;;
    esac
    case "$TOOL" in
      deepseek-harness) PACKAGE_SPEC=$PACKAGE ;;
      *) PACKAGE_SPEC="$PACKAGE@latest" ;;
    esac
    set -- "$@" "$TOOL" "$PACKAGE_SPEC"
  done

  if printf '%s\n' "$TOOLS" | grep -qx omp &&
     ! docker exec "$CONTAINER" sh -lc 'command -v bun >/dev/null'; then
    report_job_progress "$PROGRESS_STAGE" "正在安装 Agent 工具运行时：Bun"
    if ! agent_tool_exec_logged "$CONTAINER" bun sh -lc \
        'set -eu; INSTALLER=$(mktemp); trap '\''rm -f "$INSTALLER"'\'' EXIT; curl --connect-timeout 15 --max-time 300 -fsSL https://bun.sh/install -o "$INSTALLER"; bash "$INSTALLER" >/dev/null; test -x /root/.bun/bin/bun; ln -sf /root/.bun/bin/bun /usr/bin/bun' >&2; then
      echo "Agent tool runtime installation failed: bun" >&2
      return 1
    fi
  fi
  if [ "$#" -gt 0 ] || [ "$INSTALL_PI" = true ]; then
    report_job_progress "$PROGRESS_STAGE" "正在安装 Agent 工具运行时：Node.js"
    if ! agent_tool_exec_logged "$CONTAINER" node sh -lc \
        'if ! node -e '\''const [major, minor] = process.versions.node.split(".").map(Number); process.exit(major > 22 || (major === 22 && minor >= 19) ? 0 : 1)'\'' 2>/dev/null; then curl -fsSL https://deb.nodesource.com/setup_22.x | sh; export DEBIAN_FRONTEND=noninteractive; apt-get install -y nodejs; fi; node -e '\''const [major, minor] = process.versions.node.split(".").map(Number); process.exit(major > 22 || (major === 22 && minor >= 19) ? 0 : 1)'\''' >&2; then
      echo "Agent tool runtime installation failed: Node.js 22.19 or newer is required" >&2
      return 1
    fi
  fi
  if [ "$#" -gt 0 ]; then
    NPM_PLAN=$(mktemp) || return 1
    while [ "$#" -gt 0 ]; do
      TOOL=$1
      PACKAGE_SPEC=$2
      shift 2
      printf '%s\t%s\n' "$TOOL" "$PACKAGE_SPEC" >> "$NPM_PLAN"
      report_agent_tool_progress "$TOOL" running "正在批量安装 Agent 工具包：$PACKAGE_SPEC"
    done
    set --
    TAB=$(printf '\t')
    while IFS="$TAB" read -r TOOL PACKAGE_SPEC; do
      set -- "$@" "$PACKAGE_SPEC"
    done < "$NPM_PLAN"
    if ! agent_tool_exec_logged "$CONTAINER" npm-packages sh -lc \
        'set -eu; find /usr/local/lib/node_modules -maxdepth 1 -type d -name '\''.opencode-ai-*'\'' -exec rm -rf -- {} +; npm install -g "$@"' \
        agentbox "$@" >&2; then
      while IFS="$TAB" read -r TOOL PACKAGE_SPEC; do
        report_agent_tool_progress "$TOOL" failed "Agent 工具包安装失败：$PACKAGE_SPEC"
      done < "$NPM_PLAN"
      rm -f "$NPM_PLAN"
      echo "Agent tool package batch installation failed" >&2
      return 1
    fi
    while IFS="$TAB" read -r TOOL PACKAGE_SPEC; do
      report_agent_tool_progress "$TOOL" installed "Agent 工具包已安装，等待验证：$PACKAGE_SPEC"
    done < "$NPM_PLAN"
    rm -f "$NPM_PLAN"
  fi
  if [ "$INSTALL_PI" = true ]; then
    report_agent_tool_progress pi running "正在安装 Agent 工具：Pi"
    if ! agent_tool_exec_logged "$CONTAINER" pi-cleanup npm uninstall -g @mariozechner/pi-coding-agent >&2; then
      report_agent_tool_progress pi failed "Agent 工具包清理失败：Pi"
      echo "Agent tool package cleanup failed: pi" >&2
      return 1
    fi
    if ! agent_tool_exec_logged "$CONTAINER" pi npm install -g --force @earendil-works/pi-coding-agent@latest >&2; then
      report_agent_tool_progress pi failed "Agent 工具安装失败：Pi"
      echo "Agent tool package installation failed: pi" >&2
      return 1
    fi
    report_agent_tool_progress pi installed "Agent 工具已安装，等待验证：Pi"
  fi
  if [ "$INSTALL_OPENCODE" = true ]; then
    report_agent_tool_progress opencode running "正在安装 Agent 工具：OpenCode"
    if ! install_boxlite_opencode "$CONTAINER" >&2; then
      report_agent_tool_progress opencode failed "Agent 工具安装失败：OpenCode"
      echo "Agent tool package installation failed: opencode" >&2
      return 1
    fi
    report_agent_tool_progress opencode installed "Agent 工具已安装，等待验证：OpenCode"
  fi
  for TOOL in $TOOLS; do
    COMMAND=$(agent_tool_command "$TOOL") || return 1
    if docker exec "$CONTAINER" sh -lc "command -v $COMMAND >/dev/null"; then
      continue
    fi
    report_agent_tool_progress "$TOOL" running "正在安装 Agent 工具：$TOOL"
    case "$TOOL" in
      antigravity)
        agent_tool_exec_logged "$CONTAINER" "$TOOL" sh -lc \
          'set -eu; case "$(uname -m)" in x86_64|amd64) PLATFORM=linux_amd64 ;; aarch64|arm64) PLATFORM=linux_arm64 ;; *) exit 1 ;; esac; TMP_DIR=$(mktemp -d); trap '\''rm -rf "$TMP_DIR"'\'' EXIT; MANIFEST=$(curl --connect-timeout 15 --max-time 60 -fsSL "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/$PLATFORM.json"); URL=$(printf "%s" "$MANIFEST" | jq -r .url); SHA512=$(printf "%s" "$MANIFEST" | jq -r .sha512); curl --connect-timeout 15 --max-time 300 -fsSL "$URL" -o "$TMP_DIR/agy.tar.gz"; printf "%s  %s\n" "$SHA512" "$TMP_DIR/agy.tar.gz" | sha512sum -c -; tar -xzf "$TMP_DIR/agy.tar.gz" -C "$TMP_DIR" antigravity; install -m 0755 "$TMP_DIR/antigravity" /usr/bin/agy' >&2 || { report_agent_tool_progress "$TOOL" failed "Agent 工具安装失败：$TOOL"; echo "Agent tool package installation failed: $TOOL" >&2; return 1; }
        ;;
      cursor)
        agent_tool_exec_logged "$CONTAINER" "$TOOL" sh -lc \
          'set -eu; INSTALLER=$(mktemp); trap '\''rm -f "$INSTALLER"'\'' EXIT; curl --connect-timeout 15 --max-time 300 -fsSL https://cursor.com/install -o "$INSTALLER"; bash "$INSTALLER"; if [ -x /root/.local/bin/cursor-agent ]; then ln -sf /root/.local/bin/cursor-agent /usr/bin/cursor-agent; elif [ -x /root/.cursor/bin/cursor-agent ]; then ln -sf /root/.cursor/bin/cursor-agent /usr/bin/cursor-agent; else exit 1; fi' >&2 || { report_agent_tool_progress "$TOOL" failed "Agent 工具安装失败：$TOOL"; echo "Agent tool package installation failed: $TOOL" >&2; return 1; }
        ;;
      grok)
        agent_tool_exec_logged "$CONTAINER" "$TOOL" sh -lc \
          'set -eu; INSTALLER=$(mktemp); trap '\''rm -f "$INSTALLER"'\'' EXIT; curl --connect-timeout 15 --max-time 300 -fsSL https://x.ai/cli/install.sh -o "$INSTALLER"; GROK_CHANNEL=stable GROK_BIN_DIR=/usr/local/bin bash "$INSTALLER"; test -x /usr/local/bin/grok' >&2 || { report_agent_tool_progress "$TOOL" failed "Agent 工具安装失败：$TOOL"; echo "Agent tool package installation failed: $TOOL" >&2; return 1; }
        ;;
      qwenpaw)
        agent_tool_exec_logged "$CONTAINER" "$TOOL" sh -lc \
          'set -eu; INSTALLER=$(mktemp); trap '\''rm -f "$INSTALLER"'\'' EXIT; curl --connect-timeout 15 --max-time 300 -LsSf https://astral.sh/uv/install.sh -o "$INSTALLER"; sh "$INSTALLER" >/dev/null; timeout 3600 /root/.local/bin/uv tool install --python 3.12 qwenpaw; test -x /root/.local/bin/qwenpaw; ln -sf /root/.local/bin/qwenpaw /usr/bin/qwenpaw' >&2 || { report_agent_tool_progress "$TOOL" failed "Agent 工具安装失败：$TOOL"; echo "Agent tool package installation failed: $TOOL" >&2; return 1; }
        ;;
    esac
    report_agent_tool_progress "$TOOL" installed "Agent 工具已安装，等待验证：$TOOL"
  done

  VERSION_FILE=$(mktemp)
  printf '{}\n' > "$VERSION_FILE"
  for TOOL in $TOOLS; do
    report_agent_tool_progress "$TOOL" verifying "正在验证 Agent 工具：$TOOL"
    COMMAND=$(agent_tool_command "$TOOL") || { rm -f "$VERSION_FILE"; return 1; }
    docker exec "$CONTAINER" sh -lc "command -v $COMMAND >/dev/null" || {
      report_agent_tool_progress "$TOOL" failed "Agent 工具验证失败：$TOOL"
      echo "Agent tool $TOOL was installed but command $COMMAND is unavailable" >&2
      rm -f "$VERSION_FILE"
      return 1
    }
    VERSION=$(docker exec "$CONTAINER" sh -lc "timeout 20 $COMMAND --version 2>&1 | head -n 1" 2>/dev/null || true)
    [ -n "$VERSION" ] || VERSION=installed
    NEXT_VERSION_FILE=$(mktemp)
    jq --arg tool "$TOOL" --arg version "$VERSION" '. + {($tool): $version}' \
      "$VERSION_FILE" > "$NEXT_VERSION_FILE"
    mv "$NEXT_VERSION_FILE" "$VERSION_FILE"
    report_agent_tool_progress "$TOOL" succeeded "Agent 工具已就绪：$TOOL"
  done
  if ! agent_tool_exec "$CONTAINER" mkdir -p /opt/agentbox ||
     ! agent_tool_exec "$CONTAINER" rm -rf /opt/agentbox/agent-versions.json ||
     ! agent_tool_exec_stdin "$CONTAINER" "$VERSION_FILE" sh -c 'cat > /opt/agentbox/agent-versions.json'; then
    echo "Agent tool version metadata could not be written" >&2
    rm -f "$VERSION_FILE"
    return 1
  fi
  rm -f "$VERSION_FILE"
}

best_cached_agent_base() {
  BASE_IMAGE=$1
  REQUESTED_TOOLS=$2
  NOW=$3
  TTL_SECONDS=$4
  INSTALLER_REVISION=$5
  DESKTOP=$6
	REQUESTED_COUNT=$(printf '%s\n' "$REQUESTED_TOOLS" | awk 'NF { count++ } END { print count + 0 }')
  BEST_IMAGE=$BASE_IMAGE
  BEST_COUNT=0
  for IMAGE_ID in $(docker image ls -q --filter label=agentbox.runtime.cache=true | sort -u); do
    CANDIDATE_BASE=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.base" }}' "$IMAGE_ID" 2>/dev/null || true)
    [ "$CANDIDATE_BASE" = "$BASE_IMAGE" ] || continue
    CANDIDATE_INSTALLER_REVISION=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.installer-revision" }}' "$IMAGE_ID" 2>/dev/null || true)
    [ "$CANDIDATE_INSTALLER_REVISION" = "$INSTALLER_REVISION" ] || continue
    CANDIDATE_DESKTOP=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.desktop" }}' "$IMAGE_ID" 2>/dev/null || true)
    [ "$CANDIDATE_DESKTOP" = "$DESKTOP" ] || continue
    CANDIDATE_REFRESHED=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.refreshed" }}' "$IMAGE_ID" 2>/dev/null || true)
    case "$CANDIDATE_REFRESHED" in ''|*[!0-9]*) continue ;; esac
    CANDIDATE_AGE=$((NOW - CANDIDATE_REFRESHED))
    [ "$CANDIDATE_AGE" -ge 0 ] && [ "$CANDIDATE_AGE" -lt "$TTL_SECONDS" ] || continue
    CANDIDATE_TOOLS=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.tools" }}' "$IMAGE_ID" 2>/dev/null || true)
    [ -n "$CANDIDATE_TOOLS" ] || continue
    CANDIDATE_COUNT=0
    COMPATIBLE=true
    for CANDIDATE_TOOL in $(printf '%s' "$CANDIDATE_TOOLS" | tr ',' ' '); do
      if ! printf '%s\n' "$REQUESTED_TOOLS" | grep -qx "$CANDIDATE_TOOL"; then
        COMPATIBLE=false
        break
      fi
      CANDIDATE_COUNT=$((CANDIDATE_COUNT + 1))
    done
    [ "$COMPATIBLE" = true ] || continue
    [ "$CANDIDATE_COUNT" -lt "$REQUESTED_COUNT" ] || continue
    if [ "$CANDIDATE_COUNT" -gt "$BEST_COUNT" ]; then
      BEST_IMAGE=$IMAGE_ID
      BEST_COUNT=$CANDIDATE_COUNT
    fi
  done
  printf '%s' "$BEST_IMAGE"
}

install_desktop_runtime() {
  CONTAINER=$1
  JOB_FILE=$2
  jq -e '.job.payload.desktop == true' "$JOB_FILE" >/dev/null || return 0
  report_job_progress desktop-install "正在安装沙箱图形桌面"
  if ! docker exec "$CONTAINER" sh -lc 'command -v apt-get >/dev/null 2>&1'; then
    echo "Desktop mode requires a Debian or Ubuntu image with apt-get" >&2
    return 1
  fi
	if ! docker exec -e DEBIAN_FRONTEND=noninteractive "$CONTAINER" sh -lc '
    apt-get update &&
    apt-get install -y --no-install-recommends xfce4 xfce4-terminal xvfb x11vnc dbus-x11 socat xauth x11-utils ca-certificates fonts-dejavu-core &&
    rm -rf /var/lib/apt/lists/*
	' >&2; then
    echo "Desktop packages could not be installed" >&2
    return 1
  fi
  DESKTOP_START=$(mktemp)
  cat > "$DESKTOP_START" <<'AGENTBOX_DESKTOP_START'
#!/bin/sh
set -eu

DESKTOP_DIR=/opt/agentbox/desktop
DISPLAY_NUMBER=:0
mkdir -p "$DESKTOP_DIR" /run/user/0
chmod 0700 /run/user/0
export DISPLAY=$DISPLAY_NUMBER
export XDG_RUNTIME_DIR=/run/user/0
LISTEN_ADDRESS=$(cat "$DESKTOP_DIR/listen-address" 2>/dev/null || printf '127.0.0.1')
case "$LISTEN_ADDRESS" in 127.0.0.1|0.0.0.0) ;; *) LISTEN_ADDRESS=127.0.0.1 ;; esac

process_alive() {
  PID_FILE=$1
  EXPECTED_COMMAND=$2
  [ -s "$PID_FILE" ] || return 1
  PROCESS_PID=$(cat "$PID_FILE")
  case "$PROCESS_PID" in ''|*[!0-9]*) return 1 ;; esac
  [ -r "/proc/$PROCESS_PID/cmdline" ] &&
    tr '\000' ' ' < "/proc/$PROCESS_PID/cmdline" | grep -Fq "$EXPECTED_COMMAND"
}

if ! process_alive "$DESKTOP_DIR/xvfb.pid" Xvfb; then
  rm -f /tmp/.X0-lock /tmp/.X11-unix/X0
  nohup Xvfb "$DISPLAY_NUMBER" -screen 0 1440x900x24 -nolisten tcp -ac \
    >"$DESKTOP_DIR/xvfb.log" 2>&1 &
  echo $! > "$DESKTOP_DIR/xvfb.pid"
fi

READY=false
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if xdpyinfo -display "$DISPLAY_NUMBER" >/dev/null 2>&1; then
    READY=true
    break
  fi
  sleep 0.2
done
[ "$READY" = true ] || { echo "Xvfb did not become ready" >&2; exit 1; }

if ! process_alive "$DESKTOP_DIR/xfce.pid" dbus-run-session; then
  nohup dbus-run-session -- sh -lc '
    export DISPLAY=:0 XDG_RUNTIME_DIR=/run/user/0 HOME=/root USER=root LOGNAME=root
    exec startxfce4
  ' >"$DESKTOP_DIR/xfce.log" 2>&1 &
  echo $! > "$DESKTOP_DIR/xfce.pid"
fi

if ! process_alive "$DESKTOP_DIR/x11vnc.pid" x11vnc; then
  nohup x11vnc -display "$DISPLAY_NUMBER" -rfbport 5900 -listen "$LISTEN_ADDRESS" \
    -nopw -forever -shared -noxdamage -repeat \
    >"$DESKTOP_DIR/x11vnc.log" 2>&1 &
  echo $! > "$DESKTOP_DIR/x11vnc.pid"
fi

for _ in 1 2 3 4 5 6 7 8 9 10; do
  if socat -u OPEN:/dev/null TCP:127.0.0.1:5900 >/dev/null 2>&1; then
    exit 0
  fi
  sleep 0.2
done
echo "x11vnc did not become ready" >&2
exit 1
AGENTBOX_DESKTOP_START
  if ! docker exec "$CONTAINER" mkdir -p /opt/agentbox/desktop ||
     ! docker exec -i "$CONTAINER" sh -c 'cat > /opt/agentbox/desktop/start.sh && chmod 0755 /opt/agentbox/desktop/start.sh' < "$DESKTOP_START"; then
    rm -f "$DESKTOP_START"
    echo "Desktop startup script could not be written" >&2
    return 1
  fi
  rm -f "$DESKTOP_START"
}

ensure_desktop() {
  TARGET=$1
  JOB_FILE=$2
  jq -e '.job.payload.desktop == true' "$JOB_FILE" >/dev/null || return 0
  report_job_progress desktop-start "正在启动沙箱图形桌面"
  if ! docker exec "$TARGET" /opt/agentbox/desktop/start.sh; then
    echo "stage desktop-start failed: sandbox desktop could not be started" >&2
    return 1
  fi
}

prepare_agent_image() {
  BASE_IMAGE=$1
  JOB_FILE=$2
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE" | sort -u)
	DESKTOP=$(jq -r 'if .job.payload.desktop == true then "true" else "false" end' "$JOB_FILE")
	[ -n "$TOOLS" ] || [ "$DESKTOP" = true ] || {
    report_job_progress agent-image "未选择 Agent 工具，使用基础镜像" not-needed no-agent-tools
    printf '%s' "$BASE_IMAGE"
    return 0
  }
  command -v sha256sum >/dev/null || { echo "sha256sum is required for Agent runtime caching" >&2; return 1; }
  command -v paste >/dev/null || { echo "paste is required for Agent runtime caching" >&2; return 1; }

  TOOL_SET=$(printf '%s\n' "$TOOLS" | paste -sd, -)
  INSTALLER_REVISION=10
	CACHE_KEY=$(printf '%s\n%s\n%s\n%s\n' "$BASE_IMAGE" "$TOOL_SET" "$DESKTOP" "$INSTALLER_REVISION" | sha256sum | cut -c1-16)
  CACHE_IMAGE="agentbox/runtime-$CACHE_KEY:latest"
  REFRESH_IMAGE="agentbox/runtime-$CACHE_KEY:refresh-$$"
  BUILD_CONTAINER="agentbox-runtime-build-$CACHE_KEY"
  NOW=$(date +%s)
  TTL_HOURS=${AGENTBOX_AGENT_IMAGE_TTL_HOURS:-168}
  case "$TTL_HOURS" in ''|*[!0-9]*) TTL_HOURS=168 ;; esac
  TTL_SECONDS=$((TTL_HOURS * 3600))

  CACHE_MISS_REASON=not-found
  CACHED_AT=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.refreshed" }}' "$CACHE_IMAGE" 2>/dev/null || true)
  case "$CACHED_AT" in
    ''|*[!0-9]*)
      if docker image inspect "$CACHE_IMAGE" >/dev/null 2>&1; then
        CACHE_MISS_REASON=invalid-metadata
      fi
      ;;
    *)
      AGE=$((NOW - CACHED_AT))
      if [ "$AGE" -ge 0 ] && [ "$AGE" -lt "$TTL_SECONDS" ]; then
        report_job_progress agent-image "已命中 Agent 工具镜像缓存" hit exact-cache
        printf '%s' "$CACHE_IMAGE"
        return 0
      fi
      CACHE_MISS_REASON=expired
      ;;
  esac

  report_job_progress agent-image "Agent 工具镜像缓存未命中，正在构建" miss "$CACHE_MISS_REASON"
	BUILD_BASE_IMAGE=$(best_cached_agent_base "$BASE_IMAGE" "$TOOLS" "$NOW" "$TTL_SECONDS" "$INSTALLER_REVISION" "$DESKTOP")
  if [ "$BUILD_BASE_IMAGE" != "$BASE_IMAGE" ]; then
    report_job_progress agent-image "正在复用兼容的 Agent 工具缓存" partial compatible-subset
  fi

  if docker inspect "$BUILD_CONTAINER" >/dev/null 2>&1; then
    docker start "$BUILD_CONTAINER" >/dev/null
  else
    docker run -d --name "$BUILD_CONTAINER" \
      --label "agentbox.runtime.build=$CACHE_KEY" \
      "$BUILD_BASE_IMAGE" sh -lc 'trap : TERM INT; sleep infinity & wait' >/dev/null
  fi
  if ! configure_proxy "$BUILD_CONTAINER" "$JOB_FILE"; then
    echo "Agent runtime build could not apply the selected network proxy" >&2
    docker stop --time 10 "$BUILD_CONTAINER" >/dev/null 2>&1 || true
    return 1
  fi
  if AGENTBOX_AGENT_TOOL_PROGRESS_STAGE=agent-image install_agent_tools "$BUILD_CONTAINER" "$JOB_FILE" &&
     install_desktop_runtime "$BUILD_CONTAINER" "$JOB_FILE" &&
     remove_proxy "$BUILD_CONTAINER" &&
     docker commit \
       --change "LABEL agentbox.runtime.cache=true" \
       --change "LABEL agentbox.runtime.refreshed=$NOW" \
       --change "LABEL agentbox.runtime.base=$BASE_IMAGE" \
       --change "LABEL agentbox.runtime.tools=$TOOL_SET" \
	     --change "LABEL agentbox.runtime.desktop=$DESKTOP" \
       --change "LABEL agentbox.runtime.installer-revision=$INSTALLER_REVISION" \
       "$BUILD_CONTAINER" "$REFRESH_IMAGE" >/dev/null &&
     docker tag "$REFRESH_IMAGE" "$CACHE_IMAGE"; then
    docker rm -f "$BUILD_CONTAINER" >/dev/null 2>&1 || true
    docker image rm "$REFRESH_IMAGE" >/dev/null 2>&1 || true
    report_job_progress agent-image "Agent 工具镜像缓存已更新" refreshed cache-created
    printf '%s' "$CACHE_IMAGE"
    return 0
  fi

  remove_proxy "$BUILD_CONTAINER"
  docker stop --time 10 "$BUILD_CONTAINER" >/dev/null 2>&1 || true
  docker image rm "$REFRESH_IMAGE" >/dev/null 2>&1 || true
  if docker image inspect "$CACHE_IMAGE" >/dev/null 2>&1; then
    echo "Agent runtime refresh failed; using the last working cached image" >&2
    report_job_progress agent-image "缓存刷新失败，使用上次可用镜像" fallback refresh-failed
    printf '%s' "$CACHE_IMAGE"
    return 0
  fi
  echo "Agent runtime image build failed" >&2
  return 1
}

append_env() {
  ENV_FILE=$1
  KEY=$2
  VALUE=$3
  ESCAPED=$(printf '%s' "$VALUE" | sed "s/'/'\"'\"'/g")
  printf "%s='%s'\n" "$KEY" "$ESCAPED" >> "$ENV_FILE"
}

remove_env() {
  ENV_FILE=$1
  KEY=$2
  FILTERED=$(mktemp)
  awk -v key="$KEY" 'index($0, key "=") != 1' "$ENV_FILE" > "$FILTERED"
  cat "$FILTERED" > "$ENV_FILE"
  rm -f "$FILTERED"
}

replace_env() {
  ENV_FILE=$1
  KEY=$2
  VALUE=$3
  remove_env "$ENV_FILE" "$KEY"
  append_env "$ENV_FILE" "$KEY" "$VALUE"
}

remove_proxy() {
  CONTAINER=$1
  docker exec "$CONTAINER" rm -f \
    /opt/agentbox/secrets/proxy.env \
    /etc/profile.d/agentbox-proxy.sh >/dev/null 2>&1 || true
}

server_url_host() {
  AUTHORITY=${SERVER_URL#*://}
  AUTHORITY=${AUTHORITY%%/*}
  case "$AUTHORITY" in
    \[*\]*) HOST=${AUTHORITY#\[}; HOST=${HOST%%\]*} ;;
    *) HOST=${AUTHORITY%%:*} ;;
  esac
  printf '%s' "$HOST" | tr '[:upper:]' '[:lower:]'
}

sandbox_runtime_base_url() {
  DRIVER=$1
  HOST=$(server_url_host)
  case "$HOST" in
    localhost|127.*|::1)
      if [ "$DRIVER" = boxlite ]; then
        printf '%s' "$SERVER_URL" | sed -E \
          's#^(https?://)(localhost|127(\.[0-9]+){3}|\[::1\])#\1host.boxlite.internal#I'
        return
      fi
      ;;
  esac
  printf '%s' "$SERVER_URL"
}

sandbox_control_plane_host() {
  DRIVER=$1
  HOST=$(server_url_host)
  case "$HOST" in
    localhost|127.*|::1) [ "$DRIVER" = boxlite ] && HOST=192.168.127.254 ;;
  esac
  printf '%s' "$HOST"
}

configure_proxy() {
  CONTAINER=$1
  JOB_FILE=$2
  if ! jq -e '.job.payload.proxy? and (.job.payload.proxy.url // "") != ""' "$JOB_FILE" >/dev/null; then
    remove_proxy "$CONTAINER"
    return 0
  fi

  PROXY_URL_B64=$(jq -r '.job.payload.proxy.url | @base64' "$JOB_FILE")
  PROXY_URL=$(printf '%s' "$PROXY_URL_B64" | base64 -d)
  NO_PROXY=$(jq -r '.job.payload.proxy.noProxy // [] | join(",")' "$JOB_FILE")
  PROXY_ENV=$(mktemp)
  PROXY_LOADER=$(mktemp)
  chmod 600 "$PROXY_ENV"
  for NAME in HTTP_PROXY HTTPS_PROXY http_proxy https_proxy; do
    append_env "$PROXY_ENV" "$NAME" "$PROXY_URL"
  done
  append_env "$PROXY_ENV" NO_PROXY "$NO_PROXY"
  append_env "$PROXY_ENV" no_proxy "$NO_PROXY"
  append_env "$PROXY_ENV" NODE_USE_ENV_PROXY 1
  append_env "$PROXY_ENV" CLAUDE_CODE_PROXY_RESOLVES_HOSTS 1
  printf '%s\n' '[ -r /opt/agentbox/secrets/proxy.env ] && . /opt/agentbox/secrets/proxy.env' > "$PROXY_LOADER"

  if docker exec "$CONTAINER" mkdir -p /opt/agentbox/secrets /etc/profile.d &&
     docker exec "$CONTAINER" rm -rf /opt/agentbox/secrets/proxy.env /etc/profile.d/agentbox-proxy.sh &&
     docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /opt/agentbox/secrets/proxy.env' < "$PROXY_ENV" &&
     docker exec -i "$CONTAINER" sh -c 'cat > /etc/profile.d/agentbox-proxy.sh; chmod 644 /etc/profile.d/agentbox-proxy.sh' < "$PROXY_LOADER"; then
    rm -f "$PROXY_ENV" "$PROXY_LOADER"
    return 0
  fi
  rm -f "$PROXY_ENV" "$PROXY_LOADER"
  return 1
}

configure_credentials() {
  CONTAINER=$1
  ORIGINAL_JOB_FILE=$2
  DRIVER=$(jq -r '.job.payload.driver // empty' "$ORIGINAL_JOB_FILE")
  RUNTIME_BASE=$(sandbox_runtime_base_url "$DRIVER")
  JOB_FILE=$(mktemp)
  jq --arg runtimeBase "${RUNTIME_BASE%/}" '
    .job.payload.credentials |= map(
      . + {
        endpoint: ($runtimeBase + .facadePath +
          (if .protocol == "anthropic" then "/anthropic"
           elif .protocol == "gemini" then "/gemini"
           else "/openai/v1" end)),
        anthropicEndpoint: ($runtimeBase + .facadePath + "/anthropic"),
        openaiEndpoint: ($runtimeBase + .facadePath + "/openai/v1"),
        chatEndpoint: ($runtimeBase + .facadePath + "/openai/v1")
      }
    )
  ' "$ORIGINAL_JOB_FILE" > "$JOB_FILE"

  ENV_FILE=$(mktemp)
  chmod 600 "$ENV_FILE"
  jq -r '.job.payload.environmentVariables[]? | [
    .name, ((.value // "") | @base64)
  ] | @tsv' "$JOB_FILE" | while IFS="$(printf '\t')" read -r NAME VALUE_B64; do
    VALUE=$(printf '%s' "$VALUE_B64" | base64 -d)
    append_env "$ENV_FILE" "$NAME" "$VALUE"
  done
  jq -r '.job.payload.credentials[] | [
    .id, .providerId, .protocol, (.endpoint // ""), (.modelId // ""), (.secret | @base64)
  ] | @tsv' "$JOB_FILE" | while IFS="$(printf '\t')" read -r ID PROVIDER PROTOCOL ENDPOINT MODEL SECRET_B64; do
    SECRET=$(printf '%s' "$SECRET_B64" | base64 -d)
    ENV_ID=$(printf '%s' "$ID" | tr '[:lower:]-' '[:upper:]_')
    append_env "$ENV_FILE" "AGENTBOX_KEY_$ENV_ID" "$SECRET"
    case "$PROTOCOL" in
      openai-responses|openai-chat)
        grep -q '^OPENAI_API_KEY=' "$ENV_FILE" || {
          append_env "$ENV_FILE" OPENAI_API_KEY "$SECRET"
          [ -z "$ENDPOINT" ] || append_env "$ENV_FILE" OPENAI_BASE_URL "$ENDPOINT"
          [ -z "$MODEL" ] || append_env "$ENV_FILE" OPENAI_MODEL "$MODEL"
        }
        ;;
      anthropic)
        grep -q '^ANTHROPIC_API_KEY=' "$ENV_FILE" || {
          append_env "$ENV_FILE" ANTHROPIC_API_KEY "$SECRET"
          [ -z "$ENDPOINT" ] || append_env "$ENV_FILE" ANTHROPIC_BASE_URL "$ENDPOINT"
          [ -z "$MODEL" ] || append_env "$ENV_FILE" ANTHROPIC_MODEL "$MODEL"
        }
        ;;
      gemini)
        grep -q '^GEMINI_API_KEY=' "$ENV_FILE" || {
          append_env "$ENV_FILE" GEMINI_API_KEY "$SECRET"
          [ -z "$ENDPOINT" ] || append_env "$ENV_FILE" GOOGLE_GEMINI_BASE_URL "$ENDPOINT"
          [ -z "$MODEL" ] || append_env "$ENV_FILE" GEMINI_MODEL "$MODEL"
        }
        ;;
    esac
    if [ "$PROVIDER" = deepseek ] && ! grep -q '^DEEPSEEK_API_KEY=' "$ENV_FILE"; then
      append_env "$ENV_FILE" DEEPSEEK_API_KEY "$SECRET"
      [ -z "$ENDPOINT" ] || append_env "$ENV_FILE" DEEPSEEK_BASE_URL "$ENDPOINT"
      [ -z "$MODEL" ] || append_env "$ENV_FILE" DEEPSEEK_MODEL "$MODEL"
    fi
    if [ "$PROVIDER" = cursor ] && ! grep -q '^CURSOR_API_KEY=' "$ENV_FILE"; then
      append_env "$ENV_FILE" CURSOR_API_KEY "$SECRET"
    fi
    if { [ "$PROVIDER" = xai ] || [ "$PROVIDER" = grok ]; } && ! grep -q '^XAI_API_KEY=' "$ENV_FILE"; then
      append_env "$ENV_FILE" XAI_API_KEY "$SECRET"
      [ -z "$ENDPOINT" ] || append_env "$ENV_FILE" XAI_BASE_URL "$ENDPOINT"
      [ -z "$MODEL" ] || append_env "$ENV_FILE" XAI_MODEL "$MODEL"
    fi
    if { [ "$PROVIDER" = qodercn ] || [ "$PROVIDER" = qoder-cn ]; } && ! grep -q '^QODERCN_PERSONAL_ACCESS_TOKEN=' "$ENV_FILE"; then
      append_env "$ENV_FILE" QODERCN_PERSONAL_ACCESS_TOKEN "$SECRET"
    fi
  done

  if jq -e '.job.payload.agentTools | index("copilot-cli")' "$JOB_FILE" >/dev/null; then
    COPILOT_CREDENTIAL=$(jq -c '[.job.payload.credentials[] |
      select((.protocol == "anthropic" or .protocol == "openai-chat") and
        (.endpoint // "") != "" and (.modelId // "") != "")][0] // empty' "$JOB_FILE")
    if [ -n "$COPILOT_CREDENTIAL" ]; then
      COPILOT_PROTOCOL=$(printf '%s' "$COPILOT_CREDENTIAL" | jq -r '.protocol')
      COPILOT_SECRET=$(printf '%s' "$COPILOT_CREDENTIAL" | jq -r '.secret')
      COPILOT_ENDPOINT=$(printf '%s' "$COPILOT_CREDENTIAL" | jq -r '.endpoint')
      COPILOT_MODEL=$(printf '%s' "$COPILOT_CREDENTIAL" | jq -r '.modelId')
      [ "$COPILOT_PROTOCOL" = anthropic ] && COPILOT_TYPE=anthropic || COPILOT_TYPE=openai
      append_env "$ENV_FILE" COPILOT_PROVIDER_TYPE "$COPILOT_TYPE"
      append_env "$ENV_FILE" COPILOT_PROVIDER_API_KEY "$COPILOT_SECRET"
      append_env "$ENV_FILE" COPILOT_PROVIDER_BASE_URL "$COPILOT_ENDPOINT"
      append_env "$ENV_FILE" COPILOT_MODEL "$COPILOT_MODEL"
    fi
  fi

  if jq -e '.job.payload.agentTools | index("kimi")' "$JOB_FILE" >/dev/null; then
    KIMI_CREDENTIAL=$(jq -c '[.job.payload.credentials[] |
      select((.protocol == "anthropic" or .protocol == "openai-chat" or
        .protocol == "openai-responses") and
        (.endpoint // "") != "" and (.modelId // "") != "")][0] // empty' "$JOB_FILE")
    if [ -n "$KIMI_CREDENTIAL" ]; then
      KIMI_PROVIDER=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.providerId')
      KIMI_PROTOCOL=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.protocol')
      KIMI_SECRET=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.secret')
      KIMI_MODEL=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.modelId')
      if [ "$KIMI_PROVIDER" = kimi ] || [ "$KIMI_PROVIDER" = moonshot ]; then
        KIMI_PROVIDER_TYPE=kimi
        KIMI_ENDPOINT=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.chatEndpoint')
      elif [ "$KIMI_PROTOCOL" = anthropic ]; then
        KIMI_PROVIDER_TYPE=anthropic
        KIMI_ENDPOINT=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.anthropicEndpoint')
      else
        KIMI_PROVIDER_TYPE=openai
        KIMI_ENDPOINT=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.chatEndpoint')
      fi
      append_env "$ENV_FILE" KIMI_MODEL_NAME "$KIMI_MODEL"
      append_env "$ENV_FILE" KIMI_MODEL_API_KEY "$KIMI_SECRET"
      append_env "$ENV_FILE" KIMI_MODEL_PROVIDER_TYPE "$KIMI_PROVIDER_TYPE"
      append_env "$ENV_FILE" KIMI_MODEL_BASE_URL "$KIMI_ENDPOINT"
    fi
  fi

  if jq -e '.job.payload.agentTools | index("claude-code")' "$JOB_FILE" >/dev/null; then
    CLAUDE_CREDENTIAL=$(jq -c '
      ([.job.payload.credentials[] | select(.protocol == "anthropic")][0] //
       [.job.payload.credentials[] | select(.protocol == "openai-responses")][0] //
       [.job.payload.credentials[] | select(.protocol == "openai-chat")][0] // empty)
    ' "$JOB_FILE")
    if [ -n "$CLAUDE_CREDENTIAL" ]; then
      CLAUDE_SECRET=$(printf '%s' "$CLAUDE_CREDENTIAL" | jq -r '.secret')
      CLAUDE_ENDPOINT=$(printf '%s' "$CLAUDE_CREDENTIAL" | jq -r '.anthropicEndpoint')
      CLAUDE_MODEL=$(printf '%s' "$CLAUDE_CREDENTIAL" | jq -r '.modelId')
      remove_env "$ENV_FILE" ANTHROPIC_API_KEY
      replace_env "$ENV_FILE" ANTHROPIC_AUTH_TOKEN "$CLAUDE_SECRET"
      replace_env "$ENV_FILE" ANTHROPIC_BASE_URL "$CLAUDE_ENDPOINT"
      replace_env "$ENV_FILE" ANTHROPIC_MODEL "$CLAUDE_MODEL"
      if [ "$CLAUDE_MODEL" = k3-256k ]; then
        # Claude Code caps unknown model IDs at 200K unless K3's real window is explicit.
        for KEY in ANTHROPIC_DEFAULT_FABLE_MODEL ANTHROPIC_DEFAULT_OPUS_MODEL \
          ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL \
          CLAUDE_CODE_SUBAGENT_MODEL; do
          replace_env "$ENV_FILE" "$KEY" "$CLAUDE_MODEL"
        done
        for KEY in CLAUDE_CODE_MAX_CONTEXT_TOKENS CLAUDE_CODE_AUTO_COMPACT_WINDOW; do
          grep -q "^$KEY=" "$ENV_FILE" || append_env "$ENV_FILE" "$KEY" 262144
        done
      fi
    fi
  fi

  if jq -e '.job.payload.agentTools | index("deepseek-harness")' "$JOB_FILE" >/dev/null; then
    grep -q '^DSH_PERMISSION_MODE=' "$ENV_FILE" || \
      append_env "$ENV_FILE" DSH_PERMISSION_MODE danger-full-access
  fi

  docker exec "$CONTAINER" mkdir -p /opt/agentbox/secrets /etc/profile.d
  docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /opt/agentbox/secrets/agentbox.env' < "$ENV_FILE"
  printf '%s\n' 'if [ -r /opt/agentbox/secrets/agentbox.env ]; then' \
    '  set -a' \
    '  . /opt/agentbox/secrets/agentbox.env' \
    '  set +a' \
    'fi' | docker exec -i "$CONTAINER" sh -c \
      'cat > /etc/profile.d/agentbox-env.sh && chmod 0644 /etc/profile.d/agentbox-env.sh'
  rm -f "$ENV_FILE"

  if jq -e '.job.payload.agentTools | index("claude-code")' "$JOB_FILE" >/dev/null; then
    CLAUDE_CURRENT=$(mktemp)
    CLAUDE_CONFIG=$(mktemp)
    docker exec "$CONTAINER" sh -c \
      'test ! -s /root/.claude.json || cat /root/.claude.json' > "$CLAUDE_CURRENT"
    [ -s "$CLAUDE_CURRENT" ] || printf '{}\n' > "$CLAUDE_CURRENT"
    jq '.hasCompletedOnboarding = true' "$CLAUDE_CURRENT" > "$CLAUDE_CONFIG"
    docker exec "$CONTAINER" mkdir -p /root/.claude
    docker exec -i "$CONTAINER" sh -c \
      'umask 077; cat > /root/.claude.json' < "$CLAUDE_CONFIG"
    rm -f "$CLAUDE_CURRENT" "$CLAUDE_CONFIG"
  fi

  CODEX_CREDENTIAL=$(jq -c '
    ([.job.payload.credentials[] | select(.protocol == "openai-responses")][0] //
     [.job.payload.credentials[] | select(.protocol == "anthropic")][0] //
     [.job.payload.credentials[] | select(.protocol == "openai-chat")][0] // empty)
  ' "$JOB_FILE")
  if [ -n "$CODEX_CREDENTIAL" ]; then
    CODEX_ID=$(printf '%s' "$CODEX_CREDENTIAL" | jq -r '.id')
    CODEX_ENV_ID=$(printf '%s' "$CODEX_ID" | tr '[:lower:]-' '[:upper:]_')
    CODEX_ENDPOINT=$(printf '%s' "$CODEX_CREDENTIAL" | jq -r '.openaiEndpoint // empty')
    CODEX_MODEL=$(printf '%s' "$CODEX_CREDENTIAL" | jq -r '.modelId // empty')
    CODEX_CONFIG=$(mktemp)
    [ -z "$CODEX_MODEL" ] || printf 'model = %s\n' "$(printf '%s' "$CODEX_MODEL" | jq -Rs .)" >> "$CODEX_CONFIG"
    [ -n "$CODEX_ENDPOINT" ] || CODEX_ENDPOINT=https://api.openai.com/v1
    printf 'model_provider = "agentbox"\n\n[model_providers.agentbox]\n' >> "$CODEX_CONFIG"
    printf 'name = "AgentBox"\nbase_url = %s\nenv_key = "AGENTBOX_KEY_%s"\nwire_api = "responses"\n' \
      "$(printf '%s' "$CODEX_ENDPOINT" | jq -Rs .)" "$CODEX_ENV_ID" >> "$CODEX_CONFIG"
    docker exec "$CONTAINER" mkdir -p /root/.codex
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.codex/config.toml' < "$CODEX_CONFIG"
    rm -f "$CODEX_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("grok")' "$JOB_FILE" >/dev/null; then
    GROK_CREDENTIAL=$(jq -c '
      ([.job.payload.credentials[] | select(.protocol == "openai-responses")][0] //
       [.job.payload.credentials[] | select(.protocol == "anthropic")][0] //
       [.job.payload.credentials[] | select(.protocol == "openai-chat")][0] // empty)
    ' "$JOB_FILE")
    if [ -n "$GROK_CREDENTIAL" ]; then
      GROK_ID=$(printf '%s' "$GROK_CREDENTIAL" | jq -r '.id')
      GROK_ENV_ID=$(printf '%s' "$GROK_ID" | tr '[:lower:]-' '[:upper:]_')
      GROK_ENDPOINT=$(printf '%s' "$GROK_CREDENTIAL" | jq -r '.openaiEndpoint')
      GROK_MODEL=$(printf '%s' "$GROK_CREDENTIAL" | jq -r '.modelId')
      GROK_CONFIG=$(mktemp)
      printf '[model.agentbox]\nmodel = %s\nbase_url = %s\nname = "AgentBox"\nenv_key = "AGENTBOX_KEY_%s"\n\n[models]\ndefault = "agentbox"\n\n[cli]\ninstaller = "internal"\n' \
        "$(printf '%s' "$GROK_MODEL" | jq -Rs .)" \
        "$(printf '%s' "$GROK_ENDPOINT" | jq -Rs .)" "$GROK_ENV_ID" > "$GROK_CONFIG"
      docker exec "$CONTAINER" mkdir -p /root/.grok
      docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.grok/config.toml' < "$GROK_CONFIG"
      rm -f "$GROK_CONFIG"
    fi
  fi

  if jq -e '.job.payload.agentTools | index("gemini-cli")' "$JOB_FILE" >/dev/null &&
     jq -e '.job.payload.credentials | any(.protocol == "gemini")' "$JOB_FILE" >/dev/null; then
    docker exec "$CONTAINER" mkdir -p /root/.gemini
    docker exec "$CONTAINER" sh -c 'umask 077; cp /opt/agentbox/secrets/agentbox.env /root/.gemini/.env'
    printf '{"security":{"auth":{"selectedType":"gemini-api-key"}}}\n' | \
      docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.gemini/settings.json'
  fi

  if jq -e '.job.payload.agentTools | index("opencode")' "$JOB_FILE" >/dev/null; then
    OPENCODE_CONFIG=$(mktemp)
    jq '{
      "$schema": "https://opencode.ai/config.json",
      provider: (reduce .job.payload.credentials[] as $credential ({};
        ($credential.id | ascii_upcase | gsub("-"; "_")) as $envId |
        (($credential.endpoint // "") | rtrimstr("/") |
          if $credential.protocol == "anthropic" and . != "" and (endswith("/v1") | not)
          then . + "/v1" else . end) as $baseURL |
        .[$credential.id] = ({
          npm: (if $credential.protocol == "anthropic" then "@ai-sdk/anthropic"
                elif $credential.protocol == "gemini" then "@ai-sdk/google"
                elif $credential.protocol == "openai-responses" then "@ai-sdk/openai"
                else "@ai-sdk/openai-compatible" end),
          name: $credential.id,
          options: ({apiKey: ("{env:AGENTBOX_KEY_" + $envId + "}")}
            + (if $baseURL == "" then {} else {baseURL: $baseURL} end)),
          models: (if ($credential.modelId // "") == "" then {}
            else {($credential.modelId): {name: $credential.modelId}} end)
        })
      ))
    } | . + (first(.provider | to_entries[] | select((.value.models | length) > 0)
      | {model: (.key + "/" + (.value.models | keys[0]))}) // {})' "$JOB_FILE" > "$OPENCODE_CONFIG"
    docker exec "$CONTAINER" mkdir -p /root/.config/opencode
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.config/opencode/opencode.json' < "$OPENCODE_CONFIG"
    rm -f "$OPENCODE_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("pi")' "$JOB_FILE" >/dev/null; then
    PI_CONFIG=$(mktemp)
    jq '
      def env_id: ascii_upcase | gsub("-"; "_");
      def pi_api:
        if . == "anthropic" then "anthropic-messages"
        elif . == "openai-responses" then "openai-responses"
        elif . == "openai-chat" then "openai-completions"
        elif . == "gemini" then "google-generative-ai"
        else empty end;
      [.job.payload.credentials[] |
        select((.protocol == "anthropic" or .protocol == "openai-responses" or
          .protocol == "openai-chat" or .protocol == "gemini") and
          (.endpoint // "") != "" and (.modelId // "") != "") |
        {provider: ("agentbox-" + .id), model: .modelId,
         value: {baseUrl: (.endpoint | rtrimstr("/")), api: (.protocol | pi_api),
           apiKey: ("$AGENTBOX_KEY_" + (.id | env_id)),
           models: [{id: .modelId, name: .modelId}]}}] as $entries |
      {providers: ($entries | map({key: .provider, value: .value}) | from_entries),
       defaultProvider: ($entries[0].provider // ""), defaultModel: ($entries[0].model // "")}
    ' "$JOB_FILE" > "$PI_CONFIG"
    if [ "$(jq '.providers | length' "$PI_CONFIG")" -gt 0 ]; then
      docker exec "$CONTAINER" mkdir -p /root/.pi/agent
      jq '{providers}' "$PI_CONFIG" | docker exec -i "$CONTAINER" sh -c \
        'umask 077; cat > /root/.pi/agent/models.json'
      jq '{defaultProvider,defaultModel}' "$PI_CONFIG" | docker exec -i "$CONTAINER" sh -c \
        'umask 077; cat > /root/.pi/agent/settings.json'
    fi
    rm -f "$PI_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("deepseek-harness")' "$JOB_FILE" >/dev/null; then
    DSH_SETTINGS=$(mktemp)
    jq '
      def env_id: ascii_upcase | gsub("-"; "_");
      [.job.payload.credentials[] |
        select((.chatEndpoint // "") != "" and (.modelId // "") != "") |
        {provider: ("agentbox-" + .id), model: .modelId,
         value: {
           displayName: ("agentbox-" + .id),
           apiKeyEnv: ("AGENTBOX_KEY_" + (.id | env_id)),
           api: "openai-completions",
           baseURL: (.chatEndpoint | rtrimstr("/")),
           models: [{id: .modelId, name: .modelId}]
         }}] as $entries |
      if ($entries | length) == 0 then empty else
        {"agent-default-model": {
           provider: $entries[0].provider,
           model: $entries[0].model
         },
         "llm-pi-ai": {
           providers: ($entries | map({key: .provider, value: .value}) | from_entries)
         }}
      end
    ' "$JOB_FILE" > "$DSH_SETTINGS"
    if [ -s "$DSH_SETTINGS" ]; then
      docker exec "$CONTAINER" mkdir -p /root/.dsh
      docker exec -i "$CONTAINER" sh -c \
        'umask 077; cat > /root/.dsh/settings.yaml' < "$DSH_SETTINGS"
    fi
    rm -f "$DSH_SETTINGS"
  fi

  if jq -e '.job.payload.agentTools | index("reasonix")' "$JOB_FILE" >/dev/null; then
    REASONIX_CONFIG=$(mktemp)
    jq -r '
      def env_id: ascii_upcase | gsub("-"; "_");
      def reasonix_kind:
        if . == "openai-responses" then "responses"
        elif . == "openai-chat" then "openai"
        elif . == "anthropic" then "anthropic"
        else empty end;
      [.job.payload.credentials[] |
        select((.protocol == "anthropic" or .protocol == "openai-responses" or
          .protocol == "openai-chat") and (.endpoint // "") != "" and
          (.modelId // "") != "")] as $credentials |
      if ($credentials | length) == 0 then empty else
        "config_version = 1",
        ("default_model = " + (($credentials[0] |
          "agentbox-\(.id)/\(.modelId)") | @json)),
        "",
        "[sandbox]",
        "bash = \"off\"",
        "",
        ($credentials[] |
          "[[providers]]",
          ("name = " + ("agentbox-\(.id)" | @json)),
          ("kind = " + ((.protocol | reasonix_kind) | @json)),
          ("base_url = " + ((.endpoint | rtrimstr("/")) | @json)),
          ("models = [" + (.modelId | @json) + "]"),
          ("default = " + (.modelId | @json)),
          ("api_key_env = " + ("AGENTBOX_KEY_\(.id | env_id)" | @json)),
          "")
      end
    ' "$JOB_FILE" > "$REASONIX_CONFIG"
    if [ -s "$REASONIX_CONFIG" ]; then
      docker exec "$CONTAINER" mkdir -p /root/.reasonix
      docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.reasonix/config.toml' < "$REASONIX_CONFIG"
      docker exec "$CONTAINER" sh -c \
        'umask 077; cp /opt/agentbox/secrets/agentbox.env /root/.reasonix/.env'
    fi
    rm -f "$REASONIX_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("qwen-code")' "$JOB_FILE" >/dev/null; then
    QWEN_CONFIG=$(mktemp)
    jq '
      def env_id: ascii_upcase | gsub("-"; "_");
      [.job.payload.credentials[] |
        select((.protocol == "anthropic" or .protocol == "openai-chat" or
          .protocol == "gemini") and (.endpoint // "") != "" and (.modelId // "") != "") |
        {type: (if .protocol == "openai-chat" then "openai" else .protocol end),
         model: .modelId,
         value: {id: .modelId, name: .modelId,
           envKey: ("AGENTBOX_KEY_" + (.id | env_id)),
           baseUrl: (.endpoint | rtrimstr("/"))}}] as $entries |
      {security: {auth: {selectedType: ($entries[0].type // "qwen-oauth")}},
       model: {name: ($entries[0].model // "")},
       modelProviders: {
         openai: [$entries[] | select(.type == "openai") | .value],
         anthropic: [$entries[] | select(.type == "anthropic") | .value],
         gemini: [$entries[] | select(.type == "gemini") | .value]}}
    ' "$JOB_FILE" > "$QWEN_CONFIG"
    if [ "$(jq '[.modelProviders[] | length] | add' "$QWEN_CONFIG")" -gt 0 ]; then
      docker exec "$CONTAINER" mkdir -p /root/.qwen
      docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.qwen/settings.json' < "$QWEN_CONFIG"
    fi
    rm -f "$QWEN_CONFIG"
  fi

  rm -f "$JOB_FILE"
  JOB_FILE=$ORIGINAL_JOB_FILE
}

configure_skills() {
  CONTAINER=$1
  JOB_FILE=$2
  SKILL_COUNT=$(jq '.job.payload.skills | length' "$JOB_FILE")
  [ "$SKILL_COUNT" -gt 0 ] || return 0

  docker exec "$CONTAINER" mkdir -p /opt/agentbox/skills
  jq -c '.job.payload.skills[]' "$JOB_FILE" | while IFS= read -r SKILL; do
    ID=$(printf '%s' "$SKILL" | jq -r '.id')
    NAME=$(printf '%s' "$SKILL" | jq -r '.name | @json')
    DESCRIPTION=$(printf '%s' "$SKILL" | jq -r '.description | @json')
    INSTRUCTIONS=$(printf '%s' "$SKILL" | jq -r '.spec.instructions // empty')
    [ -n "$INSTRUCTIONS" ] || INSTRUCTIONS=$(printf '%s' "$SKILL" | jq -r '.description')
    SKILL_FILE=$(mktemp)
    printf '%s\n' '---' "name: $NAME" "description: $DESCRIPTION" '---' '' "$INSTRUCTIONS" > "$SKILL_FILE"
    docker exec "$CONTAINER" mkdir -p "/opt/agentbox/skills/$ID"
    docker exec -i "$CONTAINER" sh -c 'cat > "$1"' agentbox "/opt/agentbox/skills/$ID/SKILL.md" < "$SKILL_FILE"
    for SKILL_TARGET in \
      "/root/.agents/skills/$ID" \
      "/root/.gemini/antigravity-cli/skills/$ID" \
      "/root/.codex/skills/$ID" \
      "/root/.claude/skills/$ID" \
      "/root/.codebuddy/skills/$ID" \
      "/root/.gemini/skills/$ID" \
      "/root/.config/opencode/skills/$ID" \
      "/root/.config/deveco/skills/$ID" \
      "/root/.openclaw/skills/$ID" \
      "/root/.pi/agent/skills/$ID" \
      "/root/.omp/agent/skills/$ID" \
      "/root/.copilot/skills/$ID" \
      "/root/.cursor/skills/$ID" \
      "/root/.kimi/skills/$ID" \
      "/root/.reasonix/skills/$ID" \
      "/root/.qoder/skills/$ID" \
      "/root/.qoder-cn/skills/$ID" \
      "/root/.traecli/skills/$ID" \
      "/root/.grok/skills/$ID" \
      "/root/.qwen/skills/$ID" \
      "/root/.qwenpaw/skill_pool/$ID"; do
      docker exec "$CONTAINER" mkdir -p "$SKILL_TARGET"
      docker exec -i "$CONTAINER" sh -c 'cat > "$1"' agentbox "$SKILL_TARGET/SKILL.md" < "$SKILL_FILE"
    done
    rm -f "$SKILL_FILE"
  done
}

configure_mcp_servers() {
  CONTAINER=$1
  JOB_FILE=$2
  MCP_COUNT=$(jq '.job.payload.mcpServers | length' "$JOB_FILE")
  [ "$MCP_COUNT" -gt 0 ] || return 0

  MCP_MANIFEST=$(mktemp)
  jq '{mcpServers: (.job.payload.mcpServers | map({key: .id, value: .spec}) | from_entries)}' \
    "$JOB_FILE" > "$MCP_MANIFEST"
  docker exec "$CONTAINER" mkdir -p /opt/agentbox
  docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /opt/agentbox/mcp.json' < "$MCP_MANIFEST"
  rm -f "$MCP_MANIFEST"

  if jq -e '.job.payload.agentTools | index("codex")' "$JOB_FILE" >/dev/null; then
    CODEX_CONFIG=$(mktemp)
    docker exec "$CONTAINER" sh -c 'test ! -f /root/.codex/config.toml || cat /root/.codex/config.toml' > "$CODEX_CONFIG"
    printf '\n' >> "$CODEX_CONFIG"
    jq -r '.job.payload.mcpServers[] |
      "[mcp_servers." + .id + "]\n" +
      (if .spec.transport == "stdio" then
        "type = \"stdio\"\ncommand = " + (.spec.command | @json) + "\nargs = " +
          (((.spec.args // "") | [scan("[^[:space:]]+")]) | tojson) + "\n"
      else
        "type = \"http\"\nurl = " + (.spec.url | @json) + "\n"
      end)' "$JOB_FILE" >> "$CODEX_CONFIG"
    docker exec "$CONTAINER" mkdir -p /root/.codex
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.codex/config.toml' < "$CODEX_CONFIG"
    rm -f "$CODEX_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("claude-code")' "$JOB_FILE" >/dev/null; then
    CLAUDE_CURRENT=$(mktemp)
    CLAUDE_MCP=$(mktemp)
    CLAUDE_CONFIG=$(mktemp)
    docker exec "$CONTAINER" sh -c \
      'test ! -s /root/.claude.json || cat /root/.claude.json' > "$CLAUDE_CURRENT"
    [ -s "$CLAUDE_CURRENT" ] || printf '{}\n' > "$CLAUDE_CURRENT"
    jq '{mcpServers: (.job.payload.mcpServers | map(
      . as $server | {key: .id, value:
        (if .spec.transport == "stdio" then
          {type: "stdio", command: .spec.command,
           args: ((.spec.args // "") | [scan("[^[:space:]]+")])}
        else {type: "http", url: .spec.url} end)}
    ) | from_entries)}' "$JOB_FILE" > "$CLAUDE_MCP"
    jq -s '.[0] * .[1] | .hasCompletedOnboarding = true' \
      "$CLAUDE_CURRENT" "$CLAUDE_MCP" > "$CLAUDE_CONFIG"
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.claude.json' < "$CLAUDE_CONFIG"
    rm -f "$CLAUDE_CURRENT" "$CLAUDE_MCP" "$CLAUDE_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("gemini-cli")' "$JOB_FILE" >/dev/null; then
    GEMINI_CURRENT=$(mktemp)
    GEMINI_MCP=$(mktemp)
    docker exec "$CONTAINER" sh -c 'test ! -f /root/.gemini/settings.json || cat /root/.gemini/settings.json' > "$GEMINI_CURRENT"
    [ -s "$GEMINI_CURRENT" ] || printf '{}\n' > "$GEMINI_CURRENT"
    jq '{mcpServers: (.job.payload.mcpServers | map(
      {key: .id, value:
        (if .spec.transport == "stdio" then
          {command: .spec.command, args: ((.spec.args // "") | [scan("[^[:space:]]+")])}
        else {httpUrl: .spec.url} end)}
    ) | from_entries)}' "$JOB_FILE" > "$GEMINI_MCP"
    GEMINI_CONFIG=$(mktemp)
    jq -s '.[0] * .[1]' "$GEMINI_CURRENT" "$GEMINI_MCP" > "$GEMINI_CONFIG"
    docker exec "$CONTAINER" mkdir -p /root/.gemini
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.gemini/settings.json' < "$GEMINI_CONFIG"
    rm -f "$GEMINI_CURRENT" "$GEMINI_MCP" "$GEMINI_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("opencode")' "$JOB_FILE" >/dev/null; then
    OPENCODE_CURRENT=$(mktemp)
    OPENCODE_MCP=$(mktemp)
    docker exec "$CONTAINER" sh -c 'test ! -f /root/.config/opencode/opencode.json || cat /root/.config/opencode/opencode.json' > "$OPENCODE_CURRENT"
    [ -s "$OPENCODE_CURRENT" ] || printf '{}\n' > "$OPENCODE_CURRENT"
    jq '{mcp: (.job.payload.mcpServers | map(
      {key: .id, value:
        (if .spec.transport == "stdio" then
          {type: "local", command: ([.spec.command] + ((.spec.args // "") | [scan("[^[:space:]]+")])), enabled: true}
        else {type: "remote", url: .spec.url, enabled: true} end)}
    ) | from_entries)}' "$JOB_FILE" > "$OPENCODE_MCP"
    OPENCODE_CONFIG=$(mktemp)
    jq -s '.[0] * .[1]' "$OPENCODE_CURRENT" "$OPENCODE_MCP" > "$OPENCODE_CONFIG"
    docker exec "$CONTAINER" mkdir -p /root/.config/opencode
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.config/opencode/opencode.json' < "$OPENCODE_CONFIG"
    rm -f "$OPENCODE_CURRENT" "$OPENCODE_MCP" "$OPENCODE_CONFIG"
  fi
}

install_agent_wrappers() {
  CONTAINER=$1
  JOB_FILE=$2
  docker exec "$CONTAINER" test -f /opt/agentbox/secrets/agentbox.env || return 0
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE")
  printf '%s\n' "$TOOLS" | while IFS= read -r TOOL; do
    COMMAND=$(agent_tool_command "$TOOL") || continue
    docker exec "$CONTAINER" test -x "/usr/bin/$COMMAND" || continue
    WRAPPER=$(mktemp)
    printf '#!/bin/sh\nset -a\n. /opt/agentbox/secrets/agentbox.env\n[ ! -r /opt/agentbox/secrets/proxy.env ] || . /opt/agentbox/secrets/proxy.env\nset +a\nexec /usr/bin/%s "$@"\n' "$COMMAND" > "$WRAPPER"
    docker exec -i "$CONTAINER" sh -c "cat > /usr/local/bin/$COMMAND && chmod 0755 /usr/local/bin/$COMMAND" < "$WRAPPER"
    rm -f "$WRAPPER"
  done
}

memory_mib() {
  VALUE=$(printf '%s' "$1" | tr -d ' ')
  case "$VALUE" in
    *GiB) NUMBER=${VALUE%GiB}; awk -v value="$NUMBER" 'BEGIN { printf "%d", value * 1024 }' ;;
    *MiB) NUMBER=${VALUE%MiB}; awk -v value="$NUMBER" 'BEGIN { printf "%d", value }' ;;
    *g|*G) NUMBER=${VALUE%?}; awk -v value="$NUMBER" 'BEGIN { printf "%d", value * 1024 }' ;;
    *m|*M) NUMBER=${VALUE%?}; awk -v value="$NUMBER" 'BEGIN { printf "%d", value }' ;;
    ''|*[!0-9]*) printf '4096' ;;
    *) printf '%s' "$VALUE" ;;
  esac
}

create_sandbox() {
  JOB_FILE=$1
  SANDBOX_ID=$(jq -r '.job.payload.sandboxId' "$JOB_FILE")
  DRIVER=$(jq -r '.job.payload.driver' "$JOB_FILE")
  case "$DRIVER" in docker|boxlite|microsandbox) ;; *) echo "unsupported runtime driver: $DRIVER" >&2; return 1 ;; esac
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  IMAGE=$(jq -r '.job.payload.image' "$JOB_FILE")
  WORKDIR=$(jq -r '.job.payload.workdir // "/workspace"' "$JOB_FILE")
  CPU=$(jq -r '.job.payload.cpu // empty' "$JOB_FILE")
  MEMORY_VALUE=$(jq -r '.job.payload.memory // empty' "$JOB_FILE")
  NETWORK=$(jq -r '.job.payload.network // "restricted"' "$JOB_FILE")
  SETUP=$(jq -r '.job.payload.setup // empty' "$JOB_FILE")
  TARGET="agentbox-$SANDBOX_ID"
  VOLUME="agentbox-$SANDBOX_ID-workspace"
  SANDBOX_PREEXISTED=false

  report_job_progress runtime-check "正在检查 $DRIVER 运行时"

  cleanup_failed_create() {
    [ "$SANDBOX_PREEXISTED" = false ] || return 0
    if [ "$DRIVER" = docker ]; then
      command docker rm -f "$TARGET" >/dev/null 2>&1 || true
      command docker volume rm "$VOLUME" >/dev/null 2>&1 || true
    else
      runtime_call "$DRIVER" delete "$TARGET" >/dev/null 2>&1 || true
    fi
  }

  if [ "$DRIVER" = docker ]; then
    command -v docker >/dev/null || { echo "Docker is not installed" >&2; return 1; }
    timeout 10 docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable" >&2; return 1; }
    MEMORY=$(printf '%s' "$MEMORY_VALUE" | tr -d ' ' | sed -e 's/GiB$/g/' -e 's/MiB$/m/')
    if docker inspect "$TARGET" >/dev/null 2>&1; then
      SANDBOX_PREEXISTED=true
      OWNER=$(docker inspect -f '{{ index .Config.Labels "agentbox.sandbox" }}' "$TARGET")
      [ "$OWNER" = "$SANDBOX_ID" ] || { echo "container name collision: $TARGET" >&2; return 1; }
      report_job_progress runtime-create "正在启动已有沙箱实例"
      docker start "$TARGET" >/dev/null
    else
      RUNTIME_IMAGE=$(prepare_agent_image "$IMAGE" "$JOB_FILE")
      report_job_progress runtime-create "正在创建 Docker 沙箱实例"
      set -- docker run -d --name "$TARGET" \
        --label "agentbox.sandbox=$SANDBOX_ID" \
        --restart unless-stopped --user 0:0 --workdir "$WORKDIR" \
        -v "$VOLUME:$WORKDIR"
      [ -n "$CPU" ] && set -- "$@" --cpus "$CPU"
      [ -n "$MEMORY" ] && set -- "$@" --memory "$MEMORY"
      [ "$NETWORK" = none ] && set -- "$@" --network none
      set -- "$@" "$RUNTIME_IMAGE" sh -lc 'trap : TERM INT; sleep infinity & wait'
      "$@" >/dev/null
    fi
    if ! configure_proxy "$TARGET" "$JOB_FILE"; then
      cleanup_failed_create
      echo "stage proxy-config failed: image $IMAGE could not accept the selected network proxy" >&2
      return 1
    fi
  else
    runtime_probe "$DRIVER" || { echo "stage runtime-probe failed: $DRIVER self-test failed" >&2; return 1; }
    if runtime_call "$DRIVER" inspect "$TARGET" >/dev/null 2>&1; then
      SANDBOX_PREEXISTED=true
    fi
    CPU_COUNT=$(printf '%s' "${CPU:-2}" | sed 's/\..*$//')
    case "$CPU_COUNT" in ''|*[!0-9]*) CPU_COUNT=2 ;; esac
    MEMORY_MIB=$(memory_mib "$MEMORY_VALUE")
    RUNTIME_IMAGE=$IMAGE
    AGENT_TOOLS_PREINSTALLED=false
    if command -v docker >/dev/null &&
       timeout 10 docker info >/dev/null 2>&1 &&
	     { jq -e '.job.payload.desktop == true' "$JOB_FILE" >/dev/null ||
	       { [ "$DRIVER" = boxlite ] && jq -e '.job.payload.agentTools? | length > 0' "$JOB_FILE" >/dev/null; }; }; then
      if ! RUNTIME_IMAGE=$(
        AGENTBOX_RUNTIME_DRIVER=docker
        export AGENTBOX_RUNTIME_DRIVER
        prepare_agent_image "$IMAGE" "$JOB_FILE"
      ); then
	    echo "stage runtime-image failed: Docker could not preinstall the requested runtime features for $DRIVER" >&2
        return 1
      fi
      AGENT_TOOLS_PREINSTALLED=true
    fi
    if [ "$DRIVER" = microsandbox ]; then
      report_job_progress image-prepare "正在准备 Microsandbox 镜像"
      if ! microsandbox_prepare_image "$RUNTIME_IMAGE" >/dev/null; then
	    echo "stage image-prepare failed: $DRIVER could not prepare $RUNTIME_IMAGE" >&2
        return 1
      fi
    else
      report_job_progress image-prepare "正在准备 $DRIVER 镜像"
      if ! runtime_call "$DRIVER" prepare-image "$RUNTIME_IMAGE" >/dev/null; then
        echo "stage image-prepare failed: $DRIVER could not prepare $RUNTIME_IMAGE" >&2
        return 1
      fi
    fi
    report_job_progress runtime-create "正在创建 $DRIVER 沙箱实例"
    set -- runtime_call "$DRIVER" create "$TARGET" "$RUNTIME_IMAGE" --cpus "$CPU_COUNT" \
      --memory "$MEMORY_MIB" --workdir "$WORKDIR" --network "$NETWORK"
    if [ "$DRIVER" = boxlite ] && [ "$NETWORK" = restricted ]; then
      CONTROL_PLANE_HOST=$(sandbox_control_plane_host "$DRIVER")
      for ALLOWED_HOST in $CONTROL_PLANE_HOST $(jq -r \
        '.job.payload.controlPlane.allowNet[]?, .job.payload.proxy.allowNet[]?' "$JOB_FILE" | sort -u); do
        set -- "$@" --allow-net "$ALLOWED_HOST"
      done
    fi
    if ! "$@" >/dev/null; then
      cleanup_failed_create
      echo "stage runtime-create failed: $DRIVER could not create the sandbox" >&2
      return 1
    fi
    if ! runtime_call "$DRIVER" fs-mkdir "$TARGET" "$WORKDIR" >/dev/null; then
      cleanup_failed_create
      echo "stage workspace-init failed: $DRIVER could not prepare $WORKDIR" >&2
      return 1
    fi
    if ! configure_proxy "$TARGET" "$JOB_FILE"; then
      cleanup_failed_create
      echo "stage proxy-config failed: image $IMAGE could not accept the selected network proxy" >&2
      return 1
    fi
    if [ "$AGENT_TOOLS_PREINSTALLED" = true ]; then
      for TOOL in $(jq -r '.job.payload.agentTools[]?' "$JOB_FILE" | sort -u); do
        COMMAND=$(agent_tool_command "$TOOL") || { cleanup_failed_create; return 1; }
        if ! docker exec "$TARGET" sh -lc "command -v $COMMAND >/dev/null"; then
          cleanup_failed_create
          echo "stage agent-tools failed: cached image command is unavailable: $COMMAND" >&2
          return 1
        fi
      done
    elif ! install_agent_tools "$TARGET" "$JOB_FILE"; then
        cleanup_failed_create
        echo "stage agent-tools failed: could not install or verify the selected Agent tools in the $DRIVER sandbox for image $IMAGE" >&2
        return 1
    fi
  fi

  report_job_progress configuration "正在写入沙箱配置"
  if [ "$DRIVER" = docker ]; then
    docker exec "$TARGET" mkdir -p /opt/agentbox
  else
    runtime_call "$DRIVER" fs-mkdir "$TARGET" /opt/agentbox >/dev/null
  fi
  MANIFEST=$(mktemp)
  if ! docker exec "$TARGET" rm -rf /opt/agentbox/manifest.json ||
     ! jq '.job.payload | del(.credentials, .proxy)' "$JOB_FILE" > "$MANIFEST" ||
     ! docker exec -i "$TARGET" sh -c 'cat > /opt/agentbox/manifest.json' < "$MANIFEST"; then
    rm -f "$MANIFEST"
    cleanup_failed_create
    echo "stage manifest-write failed: could not provision the sandbox manifest" >&2
    return 1
  fi
  rm -f "$MANIFEST"
  if ! configure_credentials "$TARGET" "$JOB_FILE"; then
    cleanup_failed_create
    echo "stage credentials failed: image $IMAGE could not accept AgentBox configuration" >&2
    return 1
  fi
  if ! configure_skills "$TARGET" "$JOB_FILE"; then
    cleanup_failed_create
    echo "stage skills failed: image $IMAGE could not accept AgentBox skills" >&2
    return 1
  fi
  if ! configure_mcp_servers "$TARGET" "$JOB_FILE"; then
    cleanup_failed_create
    echo "stage mcp failed: image $IMAGE could not accept MCP configuration" >&2
    return 1
  fi
  if ! install_agent_wrappers "$TARGET" "$JOB_FILE"; then
    cleanup_failed_create
    echo "stage agent-wrappers failed: image $IMAGE could not install Agent wrappers" >&2
    return 1
  fi
  if [ -n "$SETUP" ]; then
    report_job_progress setup "正在执行模板初始化命令"
    if ! docker exec "$TARGET" sh -lc "$SETUP" >&2; then
      cleanup_failed_create
      echo "stage setup-command failed: image $IMAGE must provide a POSIX shell for setup commands" >&2
      return 1
    fi
  fi
  if jq -e '.job.payload.desktop == true' "$JOB_FILE" >/dev/null; then
    if ! printf '%s\n' 127.0.0.1 | docker exec -i "$TARGET" sh -c \
      'cat > /opt/agentbox/desktop/listen-address'; then
      cleanup_failed_create
      echo "stage desktop-config failed: sandbox desktop listener could not be configured" >&2
      return 1
    fi
  fi
  if ! ensure_desktop "$TARGET" "$JOB_FILE"; then
    cleanup_failed_create
    return 1
  fi
  report_job_progress verify "正在验证沙箱可用性"
  printf '%s' "$TARGET"
}

sandbox_container_name() {
  JOB_FILE=$1
  EXTERNAL_ID=$(jq -r '.job.payload.externalId // empty' "$JOB_FILE")
  if [ -n "$EXTERNAL_ID" ]; then
    printf '%s' "$EXTERNAL_ID"
  else
    SANDBOX_ID=$(jq -r '.job.payload.sandboxId' "$JOB_FILE")
    printf 'agentbox-%s' "$SANDBOX_ID"
  fi
}

start_sandbox() {
  JOB_FILE=$1
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  EXTERNAL_ID=$(jq -r '.job.payload.externalId // empty' "$JOB_FILE")
  if [ -z "$EXTERNAL_ID" ]; then
    create_sandbox "$JOB_FILE"
    return
  fi
  TARGET=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  report_job_progress runtime-check "正在检查 $DRIVER 运行时"
  report_job_progress runtime-start "正在启动沙箱实例"
  if [ "$DRIVER" = docker ]; then
    docker inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
    docker start "$TARGET" >/dev/null
  else
    runtime_call "$DRIVER" start "$TARGET" >/dev/null
    WORKDIR=$(jq -r '.job.payload.workdir // "/workspace"' "$JOB_FILE")
    if [ "$DRIVER" = boxlite ]; then
      install_boxlite_guest "$TARGET" "$WORKDIR" >/dev/null
    else
      runtime_call "$DRIVER" fs-mkdir "$TARGET" "$WORKDIR" >/dev/null
    fi
  fi

  if ! configure_proxy "$TARGET" "$JOB_FILE"; then
    echo "stage proxy-config failed: sandbox could not apply the selected network proxy" >&2
    return 1
  fi

  if jq -e '.job.payload.credentials? and .job.payload.agentTools?' "$JOB_FILE" >/dev/null; then
    report_job_progress configuration "正在同步沙箱配置"
    install_agent_tools "$TARGET" "$JOB_FILE"
    configure_credentials "$TARGET" "$JOB_FILE"
    configure_skills "$TARGET" "$JOB_FILE"
    configure_mcp_servers "$TARGET" "$JOB_FILE"
    install_agent_wrappers "$TARGET" "$JOB_FILE"
  fi
  ensure_desktop "$TARGET" "$JOB_FILE" || return 1
  report_job_progress verify "正在验证沙箱可用性"
  printf '%s' "$TARGET"
}

restart_sandbox() {
  JOB_FILE=$1
  stop_sandbox "$JOB_FILE" >/dev/null
  start_sandbox "$JOB_FILE"
}

stop_sandbox() {
  JOB_FILE=$1
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  TARGET=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  if [ "$DRIVER" = docker ]; then
    docker inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
    docker stop --time 10 "$TARGET" >/dev/null
  else
    runtime_call "$DRIVER" stop "$TARGET" >/dev/null
  fi
  printf '%s' "$TARGET"
}

delete_sandbox() {
  JOB_FILE=$1
  SANDBOX_ID=$(jq -r '.job.payload.sandboxId' "$JOB_FILE")
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  TARGET=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  if [ "$DRIVER" = docker ]; then
    docker rm -f "$TARGET" >/dev/null 2>&1 || true
    docker volume rm "agentbox-$SANDBOX_ID-workspace" >/dev/null 2>&1 || true
  else
    runtime_call "$DRIVER" delete "$TARGET"
  fi
}

process_job() {
  JOB_FILE=$1
  JOB_ID=$(jq -r '.job.id' "$JOB_FILE")
  ACTION=$(jq -r '.job.action' "$JOB_FILE")
  case "$ACTION" in
    update-worker)
      LOG_FILE=$(mktemp)
      if ! update_worker "$JOB_FILE" 2>"$LOG_FILE"; then
        MESSAGE=$(tail -c 3500 "$LOG_FILE")
        complete_job "$JOB_ID" false "" "$MESSAGE"
      fi
      rm -f "$LOG_FILE"
      ;;
    create-sandbox)
      LOG_FILE=$(mktemp)
      if EXTERNAL_ID=$(create_sandbox "$JOB_FILE" 2>"$LOG_FILE"); then
        complete_job "$JOB_ID" true "$EXTERNAL_ID" "Sandbox created"
      else
        MESSAGE=$(tail -c 3500 "$LOG_FILE")
        complete_job "$JOB_ID" false "" "$MESSAGE"
      fi
      rm -f "$LOG_FILE"
      ;;
    start-sandbox|stop-sandbox|restart-sandbox|delete-sandbox)
      LOG_FILE=$(mktemp)
      case "$ACTION" in
        start-sandbox) OPERATION=start_sandbox ;;
        stop-sandbox) OPERATION=stop_sandbox ;;
        restart-sandbox) OPERATION=restart_sandbox ;;
        delete-sandbox) OPERATION=delete_sandbox ;;
      esac
      if EXTERNAL_ID=$($OPERATION "$JOB_FILE" 2>"$LOG_FILE"); then
        complete_job "$JOB_ID" true "$EXTERNAL_ID" "Sandbox operation completed"
      else
        MESSAGE=$(tail -c 3500 "$LOG_FILE")
        complete_job "$JOB_ID" false "" "$MESSAGE"
      fi
      rm -f "$LOG_FILE"
      ;;
    *) complete_job "$JOB_ID" false "" "Unsupported worker action: $ACTION" ;;
  esac
}

heartbeat_loop() {
  while :; do
    CAPS=$(worker_capabilities)
    INVENTORY=$(worker_inventory)
    HEARTBEAT=$(jq -n --argjson capabilities "$CAPS" --argjson inventory "$INVENTORY" \
      --arg workerVersion "$AGENTBOX_WORKER_VERSION" \
      '{capabilities:$capabilities,inventory:$inventory,workerVersion:$workerVersion}')
    if ! curl -fsS -X POST "$SERVER_URL/api/servers/$SERVER_ID/heartbeat" \
      -H "Authorization: Bearer $CREDENTIAL" \
      -H 'Content-Type: application/json' --data "$HEARTBEAT" >/dev/null 2>&1; then
      LEGACY_HEARTBEAT=$(printf '%s' "$HEARTBEAT" | jq 'del(.workerVersion)')
      curl -fsS -X POST "$SERVER_URL/api/servers/$SERVER_ID/heartbeat" \
        -H "Authorization: Bearer $CREDENTIAL" \
        -H 'Content-Type: application/json' --data "$LEGACY_HEARTBEAT" >/dev/null 2>&1 || true
    fi
    sleep 15
  done
}

run_worker() {
  [ -r "$CONFIG" ] || { echo "worker is not configured" >&2; exit 1; }
  command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
  command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
  SERVER_URL=$(sed -n '1p' "$CONFIG")
  SERVER_ID=$(sed -n '2p' "$CONFIG")
  CREDENTIAL=$(sed -n '3p' "$CONFIG")
  prepare_boxlite_config
  refresh_boxlite_images || {
    [ -s "$BOXLITE_IMAGES_FILE" ] || printf '[]\n' > "$BOXLITE_IMAGES_FILE"
    echo "warning: BoxLite image inventory is unavailable" >&2
  }
  AGENTBOX_BOXLITE_SERVER_MODE=1
  export AGENTBOX_BOXLITE_SERVER_MODE
  ensure_boxlite_server || echo "warning: BoxLite service is unavailable" >&2
  finalize_worker_update || true
  heartbeat_loop &
  HEARTBEAT_PID=$!
  SESSION_PID=
  if [ -x /usr/local/bin/agentbox-worker ]; then
    /usr/local/bin/agentbox-worker session "$CONFIG" &
    SESSION_PID=$!
  fi
  cleanup_worker() {
    kill "$HEARTBEAT_PID" >/dev/null 2>&1 || true
    [ -z "$SESSION_PID" ] || kill "$SESSION_PID" >/dev/null 2>&1 || true
    stop_boxlite_server
    wait "$HEARTBEAT_PID" >/dev/null 2>&1 || true
    [ -z "$SESSION_PID" ] || wait "$SESSION_PID" >/dev/null 2>&1 || true
  }
  trap cleanup_worker EXIT
  trap 'exit 0' INT TERM
  while :; do
    finalize_worker_update || true
    [ "${AGENTBOX_BOXLITE_SERVER_MODE:-}" != 1 ] || ensure_boxlite_server || true
    JOB_FILE=$(mktemp)
    HTTP_STATUS=$(curl -sS -o "$JOB_FILE" -w '%{http_code}' -X POST \
      "$SERVER_URL/api/servers/$SERVER_ID/jobs/claim" \
      -H "Authorization: Bearer $CREDENTIAL" || true)
    if [ "$HTTP_STATUS" = 200 ]; then
      process_job "$JOB_FILE" || true
    fi
    rm -f "$JOB_FILE"
    sleep 5
  done
}

case "${1:-}" in
  setup) setup_worker "$@" ;;
  run) run_worker ;;
  status) systemctl status agentbox-worker.service ;;
  *) usage ;;
esac
`
