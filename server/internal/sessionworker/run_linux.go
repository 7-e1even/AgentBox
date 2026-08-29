//go:build linux

package sessionworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/creack/pty"
)

const (
	sessionReadLimit      = 1024 * 1024
	sessionWriteTimeout   = 10 * time.Second
	boxliteBinary         = "/usr/local/bin/boxlite"
	boxliteURL            = "http://127.0.0.1:48100"
	boxliteGuestHelper    = "/opt/agentbox/agentbox-guest"
	microsandboxBinary    = "/usr/local/bin/agentbox-microsandbox-driver"
	uploadStagingDir      = "/var/lib/agentbox-worker/uploads"
	stagedUploadMaxAge    = 24 * time.Hour
	maxConcurrentRPCs     = 32
	desktopSessionCommand = "/opt/agentbox/desktop/start.sh && exec socat STDIO TCP:127.0.0.1:5900"
)

func Run(ctx context.Context, configPath string, stderr io.Writer) error {
	cleanupStaleUploads(stderr)
	delay := time.Second
	for ctx.Err() == nil {
		connected, err := runConnection(ctx, configPath)
		if connected {
			delay = time.Second
		}
		if err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintf(stderr, "session connection: %v\n", err)
		}
		if ctx.Err() != nil {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if delay < 10*time.Second {
			delay *= 2
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
		}
	}
	return nil
}

func runConnection(ctx context.Context, configPath string) (bool, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return false, fmt.Errorf("read worker configuration: %w", err)
	}
	config, err := parseConfig(string(content))
	if err != nil {
		return false, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+config.credential)
	header.Set("User-Agent", "AgentBox-Session-Worker/3")
	conn, _, err := websocket.Dial(ctx, config.websocketURL(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return false, err
	}
	conn.SetReadLimit(sessionReadLimit)
	manager := newSessionManager(conn)
	defer func() {
		manager.closeAll()
		conn.CloseNow()
	}()
	for {
		var incoming message
		if err := wsjson.Read(ctx, conn, &incoming); err != nil {
			return true, err
		}
		manager.handle(incoming)
	}
}

// cleanupStaleUploads removes staged upload files left behind by interrupted
// sessions so the staging directory cannot grow without bound.
func cleanupStaleUploads(stderr io.Writer) {
	entries, err := os.ReadDir(uploadStagingDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(stderr, "staging cleanup: read %s: %v\n", uploadStagingDir, err)
		}
		return
	}
	cutoff := time.Now().Add(-stagedUploadMaxAge)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(uploadStagingDir, entry.Name())); err == nil {
			removed++
		}
	}
	if removed > 0 {
		_, _ = fmt.Fprintf(stderr, "staging cleanup: removed %d stale upload(s) from %s\n", removed, uploadStagingDir)
	}
}

type sessionManager struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	sessions map[string]*terminalSession
	desktops map[string]*desktopSession
	mu       sync.RWMutex
	rpcSlots chan struct{}
}

func newSessionManager(conn *websocket.Conn) *sessionManager {
	return &sessionManager{
		conn:     conn,
		sessions: make(map[string]*terminalSession),
		desktops: make(map[string]*desktopSession),
		rpcSlots: make(chan struct{}, maxConcurrentRPCs),
	}
}

func (m *sessionManager) send(outgoing message) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), sessionWriteTimeout)
	defer cancel()
	return wsjson.Write(ctx, m.conn, outgoing)
}

func (m *sessionManager) handle(incoming message) {
	if incoming.Type == "open" {
		m.open(incoming)
		return
	}
	m.mu.RLock()
	session := m.sessions[incoming.SessionID]
	desktop := m.desktops[incoming.SessionID]
	m.mu.RUnlock()
	switch incoming.Type {
	case "rpc":
		select {
		case m.rpcSlots <- struct{}{}:
			go func() {
				defer func() { <-m.rpcSlots }()
				m.handleRPC(incoming)
			}()
		default:
			_ = m.send(message{
				Type:      "rpc-result",
				SessionID: incoming.SessionID,
				RequestID: incoming.RequestID,
				Error:     "session worker is busy; retry the file operation",
			})
		}
	case "input":
		if session != nil {
			_ = session.input(incoming.Data)
		}
	case "resize":
		if session != nil {
			_ = session.resize(incoming.Cols, incoming.Rows)
		}
	case "desktop-input":
		if desktop != nil {
			_ = desktop.input(incoming.Data)
		}
	case "close":
		if session != nil {
			m.mu.Lock()
			if m.sessions[incoming.SessionID] == session {
				delete(m.sessions, incoming.SessionID)
			}
			m.mu.Unlock()
			session.close()
		}
		if desktop != nil {
			m.mu.Lock()
			if m.desktops[incoming.SessionID] == desktop {
				delete(m.desktops, incoming.SessionID)
			}
			m.mu.Unlock()
			desktop.close()
		}
	}
}

func (m *sessionManager) open(incoming message) {
	if incoming.SessionID == "" || incoming.ExternalID == "" ||
		(incoming.Driver != "docker" && incoming.Driver != "boxlite" && incoming.Driver != "microsandbox") {
		_ = m.send(message{Type: "error", SessionID: incoming.SessionID, Error: "invalid sandbox session target"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := runtimeInspect(ctx, incoming.Driver, incoming.ExternalID); err != nil {
		_ = m.send(message{Type: "error", SessionID: incoming.SessionID, Error: truncateError(err)})
		return
	}
	if incoming.Mode == "desktop" {
		desktop, err := startDesktopSession(m, incoming.SessionID, incoming.Driver, incoming.ExternalID)
		if err != nil {
			_ = m.send(message{Type: "error", SessionID: incoming.SessionID, Error: truncateError(err)})
			return
		}
		m.mu.Lock()
		previous := m.desktops[incoming.SessionID]
		m.desktops[incoming.SessionID] = desktop
		m.mu.Unlock()
		if previous != nil {
			previous.close()
		}
		_ = m.send(message{Type: "ready", SessionID: incoming.SessionID})
		return
	}
	session, err := startTerminalSession(m, incoming.SessionID, incoming.Driver, incoming.ExternalID, incoming.Cols, incoming.Rows)
	if err != nil {
		_ = m.send(message{Type: "error", SessionID: incoming.SessionID, Error: truncateError(err)})
		return
	}
	m.mu.Lock()
	previous := m.sessions[incoming.SessionID]
	m.sessions[incoming.SessionID] = session
	m.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	_ = m.send(message{Type: "ready", SessionID: incoming.SessionID})
}

func (m *sessionManager) handleRPC(incoming message) {
	m.mu.RLock()
	session := m.sessions[incoming.SessionID]
	m.mu.RUnlock()
	result := message{Type: "rpc-result", SessionID: incoming.SessionID, RequestID: incoming.RequestID}
	if session == nil {
		result.Error = "terminal session is not ready"
		_ = m.send(result)
		return
	}
	containerPath, err := validPath(incoming.Path)
	if err == nil {
		switch incoming.Operation {
		case "list":
			result.Result, err = rpcList(session.driver, session.target, containerPath)
		case "read":
			result.Result, err = rpcRead(session.driver, session.target, containerPath)
		case "write":
			result.Result, err = rpcWrite(session.driver, session.target, containerPath, incoming.Content)
		case "upload-start":
			result.Result, err = rpcUploadStart(session.driver, session.target, containerPath, incoming.UploadID)
		case "upload-chunk":
			result.Result, err = rpcUploadChunk(session.driver, session.target, containerPath, incoming.UploadID, incoming.Content)
		case "upload-finish":
			result.Result, err = rpcUploadFinish(session.driver, session.target, containerPath, incoming.UploadID)
		case "upload-cancel":
			result.Result, err = rpcUploadCancel(session.driver, session.target, containerPath, incoming.UploadID)
		default:
			err = errors.New("unsupported file operation")
		}
	}
	if err != nil {
		result.Error = truncateError(err)
		result.Result = nil
	} else {
		result.OK = true
	}
	_ = m.send(result)
}

func (m *sessionManager) sessionClosed(id string, session *terminalSession, retryable bool) {
	m.mu.Lock()
	if m.sessions[id] != session {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	_ = m.send(message{Type: "closed", SessionID: id, Retryable: retryable})
}

func (m *sessionManager) desktopClosed(id string, desktop *desktopSession, runErr error) {
	m.mu.Lock()
	if m.desktops[id] != desktop {
		m.mu.Unlock()
		return
	}
	delete(m.desktops, id)
	m.mu.Unlock()
	if runErr != nil && !desktop.closedByUser.Load() {
		_ = m.send(message{Type: "error", SessionID: id, Error: truncateError(runErr)})
		return
	}
	_ = m.send(message{Type: "closed", SessionID: id})
}

func (m *sessionManager) closeAll() {
	m.mu.Lock()
	sessions := make([]*terminalSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*terminalSession)
	desktops := make([]*desktopSession, 0, len(m.desktops))
	for _, desktop := range m.desktops {
		desktops = append(desktops, desktop)
	}
	m.desktops = make(map[string]*desktopSession)
	m.mu.Unlock()
	for _, session := range sessions {
		session.close()
	}
	for _, desktop := range desktops {
		desktop.close()
	}
}

type desktopSession struct {
	manager      *sessionManager
	id           string
	command      *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	closed       atomic.Bool
	closedByUser atomic.Bool
	writeMu      sync.Mutex
}

func startDesktopSession(manager *sessionManager, id, driver, target string) (*desktopSession, error) {
	if driver == "boxlite" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("listen for BoxLite desktop: %w", err)
		}
		defer listener.Close()
		tcpListener, ok := listener.(*net.TCPListener)
		if !ok {
			return nil, errors.New("BoxLite desktop listener is not TCP")
		}
		port := tcpListener.Addr().(*net.TCPAddr).Port
		bridgeCommand := fmt.Sprintf(
			"/opt/agentbox/desktop/start.sh && exec socat TCP:host.boxlite.internal:%d TCP:127.0.0.1:5900",
			port,
		)
		command := exec.Command(boxliteBinary, "--url", boxliteURL, "exec", "-u", "0:0", target, "--", "/bin/sh", "-lc", bridgeCommand)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		if err := command.Start(); err != nil {
			return nil, fmt.Errorf("start BoxLite desktop bridge: %w", err)
		}
		if err := tcpListener.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return nil, fmt.Errorf("set BoxLite desktop deadline: %w", err)
		}
		stream, err := tcpListener.Accept()
		if err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return nil, fmt.Errorf("accept BoxLite desktop bridge: %w", err)
		}
		desktop := &desktopSession{manager: manager, id: id, command: command, stdin: stream, stdout: stream}
		go desktop.pumpOutput()
		return desktop, nil
	}
	command, err := desktopCommand(driver, target)
	if err != nil {
		return nil, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	desktop := &desktopSession{manager: manager, id: id, command: command, stdin: stdin, stdout: stdout}
	go desktop.pumpOutput()
	return desktop, nil
}

func desktopCommand(driver, target string) (*exec.Cmd, error) {
	guestCommand := []string{"/bin/sh", "-lc", desktopSessionCommand}
	switch driver {
	case "docker":
		return exec.Command("docker", append([]string{"exec", "-i", "-u", "0:0", target}, guestCommand...)...), nil
	case "microsandbox":
		return exec.Command(microsandboxBinary, append([]string{"exec", "--stdin", target, "--"}, guestCommand...)...), nil
	default:
		return nil, errors.New("unsupported sandbox desktop driver")
	}
}

func (s *desktopSession) pumpOutput() {
	buffer := make([]byte, 64*1024)
	for {
		count, err := s.stdout.Read(buffer)
		if count > 0 {
			_ = s.manager.send(message{
				Type: "desktop-data", SessionID: s.id,
				Data: base64.StdEncoding.EncodeToString(buffer[:count]),
			})
		}
		if err != nil {
			break
		}
	}
	var runErr error
	if s.command != nil {
		runErr = s.command.Wait()
	}
	s.closed.Store(true)
	_ = s.stdin.Close()
	_ = s.stdout.Close()
	s.manager.desktopClosed(s.id, s, runErr)
}

func (s *desktopSession) input(data string) error {
	if s.closed.Load() {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(decoded) > sessionReadLimit {
		return errors.New("invalid desktop input")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(decoded)
	return err
}

func (s *desktopSession) close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.closedByUser.Store(true)
	_ = s.stdin.Close()
	_ = s.stdout.Close()
	if s.command == nil || s.command.Process == nil {
		return
	}
	_ = s.command.Process.Signal(syscall.SIGTERM)
	process := s.command.Process
	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		<-timer.C
		_ = process.Kill()
	}()
}

type terminalSession struct {
	manager      *sessionManager
	id           string
	driver       string
	target       string
	command      *exec.Cmd
	terminal     *os.File
	closed       atomic.Bool
	closedByUser atomic.Bool
	terminalMu   sync.Mutex
}

func startTerminalSession(manager *sessionManager, id, driver, target string, columns, rows int) (*terminalSession, error) {
	command, err := terminalCommand(driver, target)
	if err != nil {
		return nil, err
	}
	columns, rows = normalizedSize(columns, rows)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(columns), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	session := &terminalSession{manager: manager, id: id, driver: driver, target: target, command: command, terminal: terminal}
	go session.pumpOutput()
	return session, nil
}

func normalizedSize(columns, rows int) (int, int) {
	if columns < 2 || columns > 1000 {
		columns = 120
	}
	if rows < 1 || rows > 500 {
		rows = 30
	}
	return columns, rows
}

func terminalCommand(driver, target string) (*exec.Cmd, error) {
	guestCommand := []string{"/usr/bin/env", "HOME=/root", "USER=root", "LOGNAME=root", "TERM=xterm-256color", "/bin/sh", "-c", loginShellCommand}
	switch driver {
	case "docker":
		return exec.Command("docker", append([]string{"exec", "-it", "-u", "0:0", target}, guestCommand...)...), nil
	case "boxlite":
		args := []string{"--url", boxliteURL, "exec", "-it", "-u", "0:0", target, "--"}
		return exec.Command(boxliteBinary, append(args, guestCommand...)...), nil
	case "microsandbox":
		return exec.Command(microsandboxBinary, "terminal", target), nil
	default:
		return nil, errors.New("unsupported sandbox session driver")
	}
}

func (s *terminalSession) pumpOutput() {
	decoder := streamDecoder{}
	buffer := make([]byte, 32*1024)
	transportTail := ""
	transportFailed := false
	for {
		count, err := s.terminal.Read(buffer)
		if count > 0 {
			if data := decoder.decode(buffer[:count], false); data != "" {
				_ = s.manager.send(message{Type: "output", SessionID: s.id, Data: data})
				if s.driver == "boxlite" {
					transportTail += data
					if len(transportTail) > 2048 {
						transportTail = transportTail[len(transportTail)-2048:]
					}
					if isBoxLiteAttachFailure(transportTail) {
						transportFailed = true
						break
					}
				}
			}
		}
		if err != nil {
			if !s.closed.Load() && !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EIO) {
				_ = s.manager.send(message{Type: "error", SessionID: s.id, Error: truncateError(err)})
			}
			break
		}
	}
	if data := decoder.decode(nil, true); data != "" {
		_ = s.manager.send(message{Type: "output", SessionID: s.id, Data: data})
	}
	if transportFailed && s.command.Process != nil {
		_ = s.command.Process.Kill()
	}
	runErr := s.command.Wait()
	retryable := (runErr != nil || transportFailed) && !s.closedByUser.Load()
	s.closed.Store(true)
	_ = s.terminal.Close()
	s.manager.sessionClosed(s.id, s, retryable)
}

func (s *terminalSession) input(data string) error {
	if s.closed.Load() {
		return nil
	}
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	_, err := io.WriteString(s.terminal, data)
	return err
}

func (s *terminalSession) resize(columns, rows int) error {
	if s.closed.Load() {
		return nil
	}
	columns, rows = normalizedSize(columns, rows)
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	return pty.Setsize(s.terminal, &pty.Winsize{Cols: uint16(columns), Rows: uint16(rows)})
}

func (s *terminalSession) close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.closedByUser.Store(true)
	_ = s.terminal.Close()
	if s.command.Process == nil {
		return
	}
	_ = s.command.Process.Signal(syscall.SIGTERM)
	process := s.command.Process
	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		<-timer.C
		_ = process.Kill()
	}()
}

type streamDecoder struct {
	pending []byte
}

func (d *streamDecoder) decode(chunk []byte, final bool) string {
	data := append(append([]byte(nil), d.pending...), chunk...)
	d.pending = nil
	var output strings.Builder
	for len(data) > 0 {
		if !utf8.FullRune(data) && !final {
			d.pending = append([]byte(nil), data...)
			break
		}
		runeValue, size := utf8.DecodeRune(data)
		output.WriteRune(runeValue)
		data = data[size:]
	}
	return output.String()
}

func runtimeInspect(ctx context.Context, driver, target string) error {
	var command *exec.Cmd
	switch driver {
	case "docker":
		command = exec.CommandContext(ctx, "docker", "inspect", target)
	case "boxlite":
		command = exec.CommandContext(ctx, boxliteBinary, "--url", boxliteURL, "inspect", target)
	case "microsandbox":
		command = exec.CommandContext(ctx, microsandboxBinary, "inspect", target)
	default:
		return errors.New("unsupported sandbox driver")
	}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func runtimeRun(driver, target, script, containerPath string, input []byte, timeout time.Duration, extraArgs ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	guestCommand := []string{"sh", "-c", script, "sh", containerPath}
	guestCommand = append(guestCommand, extraArgs...)
	var command *exec.Cmd
	switch driver {
	case "docker":
		args := []string{"exec", "-u", "0:0"}
		if input != nil {
			args = append(args, "-i")
		}
		args = append(args, target)
		command = exec.CommandContext(ctx, "docker", append(args, guestCommand...)...)
	case "boxlite":
		args := []string{"--url", boxliteURL, "exec", "-u", "0:0"}
		if input != nil {
			args = append(args, "-i")
		}
		args = append(args, target, "--")
		command = exec.CommandContext(ctx, boxliteBinary, append(args, guestCommand...)...)
	case "microsandbox":
		args := []string{"exec"}
		if input != nil {
			args = append(args, "--stdin")
		}
		args = append(args, target, "--")
		command = exec.CommandContext(ctx, microsandboxBinary, append(args, guestCommand...)...)
	default:
		return nil, errors.New("unsupported sandbox driver")
	}
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = "sandbox operation failed"
		}
		return nil, errors.New(message)
	}
	return output, nil
}

type fileEntry struct {
	Type       string  `json:"type"`
	Size       int64   `json:"size"`
	ModifiedAt float64 `json:"modifiedAt"`
	Path       string  `json:"path"`
	Name       string  `json:"name"`
}

type runtimeFileExistsResult struct {
	Exists bool `json:"exists"`
}

func runtimeFileRun(driver, target, action, containerPath string, input io.Reader, timeout time.Duration, extraPaths ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var command *exec.Cmd
	switch driver {
	case "boxlite":
		args := []string{"--url", boxliteURL, "exec"}
		if input != nil {
			args = append(args, "-i")
		}
		args = append(args, "-u", "0:0", target, "--", boxliteGuestHelper, "guest-fs", action, containerPath)
		args = append(args, extraPaths...)
		command = exec.CommandContext(ctx, boxliteBinary, args...)
	case "microsandbox":
		args := []string{"fs-" + action, target, containerPath}
		args = append(args, extraPaths...)
		command = exec.CommandContext(ctx, microsandboxBinary, args...)
	default:
		return nil, errors.New("native filesystem operations are unavailable for this sandbox driver")
	}
	command.Stdin = input
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = "sandbox filesystem operation failed"
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

func runtimeFileExists(driver, target, containerPath string) (bool, error) {
	output, err := runtimeFileRun(driver, target, "exists", containerPath, nil, 15*time.Second)
	if err != nil {
		return false, err
	}
	var result runtimeFileExistsResult
	if err := json.Unmarshal(output, &result); err != nil {
		return false, fmt.Errorf("decode sandbox file metadata: %w", err)
	}
	return result.Exists, nil
}

func rpcList(driver, target, containerPath string) ([]fileEntry, error) {
	if driver != "docker" {
		output, err := runtimeFileRun(driver, target, "list", containerPath, nil, 15*time.Second)
		if err != nil {
			return nil, err
		}
		var entries []fileEntry
		if err := json.Unmarshal(output, &entries); err != nil {
			return nil, fmt.Errorf("decode sandbox directory listing: %w", err)
		}
		return entries, nil
	}
	output, err := runtimeRun(driver, target, `set -eu; test -d "$1" || { echo "directory not found: $1" >&2; exit 1; }; tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT; if find "$1" -mindepth 1 -maxdepth 1 -printf "%y\t%s\t%T@\t%p\n" > "$tmp" 2>/dev/null; then sed -n "1,1000p" "$tmp"; exit 0; fi; find "$1" -mindepth 1 -maxdepth 1 -print | while IFS= read -r entry; do if [ -d "$entry" ]; then entry_type=d; else entry_type=f; fi; entry_size=$(stat -c %s "$entry" 2>/dev/null || printf '0'); entry_mtime=$(stat -c %Y "$entry" 2>/dev/null || printf '0'); printf '%s\t%s\t%s\t%s\n' "$entry_type" "$entry_size" "$entry_mtime" "$entry"; done > "$tmp"; sed -n "1,1000p" "$tmp"`, containerPath, nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	entries := make([]fileEntry, 0)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		entryPath := strings.Join(fields[3:], "\t")
		size, _ := strconv.ParseInt(fields[1], 10, 64)
		modifiedAt, _ := strconv.ParseFloat(fields[2], 64)
		entryType := "file"
		if fields[0] == "d" {
			entryType = "directory"
		}
		entries = append(entries, fileEntry{Type: entryType, Size: size, ModifiedAt: modifiedAt, Path: entryPath, Name: path.Base(entryPath)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "directory"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func rpcRead(driver, target, containerPath string) (string, error) {
	var output []byte
	var err error
	if driver == "docker" {
		output, err = runtimeRun(driver, target, `set -eu; test -f "$1" || { echo "file not found: $1" >&2; exit 1; }; size=$(wc -c < "$1"); [ "$size" -le 524288 ] || { echo "file exceeds the 512 KiB editor limit" >&2; exit 1; }; cat "$1"`, containerPath, nil, 15*time.Second)
	} else {
		output, err = runtimeFileRun(driver, target, "read", containerPath, nil, 15*time.Second)
	}
	if err != nil {
		return "", err
	}
	if len(output) > maxFileSize {
		return "", errors.New("file exceeds the 512 KiB editor limit")
	}
	if bytes.IndexByte(output, 0) >= 0 {
		return "", errors.New("binary files cannot be opened in the text editor")
	}
	if !utf8.Valid(output) {
		return "", errors.New("file is not valid UTF-8 text")
	}
	return string(output), nil
}

func rpcWrite(driver, target, containerPath, content string) (string, error) {
	encoded := []byte(content)
	if len(encoded) > maxFileSize {
		return "", errors.New("file exceeds the 512 KiB editor limit")
	}
	var err error
	if driver == "docker" {
		_, err = runtimeRun(driver, target, `set -eu; target=$1; parent=${target%/*}; [ -n "$parent" ] || parent=/; mkdir -p "$parent"; temp="$target.agentbox.$$"; trap "rm -f \$temp" EXIT; cat > "$temp"; mv "$temp" "$target"; trap - EXIT`, containerPath, encoded, 15*time.Second)
	} else {
		_, err = runtimeFileRun(driver, target, "write", containerPath, bytes.NewReader(encoded), 15*time.Second)
	}
	return "saved", err
}

func stagedUploadPath(target, containerPath, uploadID string) string {
	digest := sha256.Sum256([]byte(target + "\x00" + containerPath + "\x00" + uploadID))
	return filepath.Join(uploadStagingDir, fmt.Sprintf("%x.part", digest))
}

var stagedUploadLocks sync.Map

// lockStagedUpload serializes chunk writes for one staged upload so concurrent
// RPC handlers cannot append out-of-order chunks to the same file.
func lockStagedUpload(key string) func() {
	lock, _ := stagedUploadLocks.LoadOrStore(key, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func rpcUploadStart(driver, target, containerPath, uploadID string) (string, error) {
	tempPath, err := uploadTempPath(containerPath, uploadID)
	if err != nil {
		return "", err
	}
	if driver != "docker" {
		exists, err := runtimeFileExists(driver, target, containerPath)
		if err != nil {
			return "", err
		}
		if exists {
			return "", fmt.Errorf("file already exists: %s", containerPath)
		}
		if err := os.MkdirAll(uploadStagingDir, 0o700); err != nil {
			return "", err
		}
		stagedPath := stagedUploadPath(target, containerPath, uploadID)
		file, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return "", err
		}
		return "started", file.Close()
	}
	_, err = runtimeRun(driver, target, `set -eu; final=$1; temp=$2; [ ! -e "$final" ] || { echo "file already exists: $final" >&2; exit 1; }; parent=${final%/*}; [ -n "$parent" ] || parent=/; mkdir -p "$parent"; rm -f "$temp"; : > "$temp"`, containerPath, nil, 15*time.Second, tempPath)
	return "started", err
}

func rpcUploadChunk(driver, target, containerPath, uploadID, content string) (string, error) {
	tempPath, err := uploadTempPath(containerPath, uploadID)
	if err != nil {
		return "", err
	}
	chunk, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return "", errors.New("upload chunk is not valid base64")
	}
	if len(chunk) > maxUploadChunkSize {
		return "", errors.New("upload chunk exceeds 192 KiB")
	}
	unlock := lockStagedUpload(stagedUploadPath(target, containerPath, uploadID))
	defer unlock()
	if driver != "docker" {
		stagedPath := stagedUploadPath(target, containerPath, uploadID)
		file, err := os.OpenFile(stagedPath, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", errors.New("upload is not initialized")
			}
			return "", err
		}
		_, writeErr := file.Write(chunk)
		info, statErr := file.Stat()
		closeErr := file.Close()
		if err := errors.Join(writeErr, statErr, closeErr); err != nil {
			return "", err
		}
		if info.Size() > maxUploadSize {
			_ = os.Remove(stagedPath)
			return "", errors.New("upload exceeds 50 MiB")
		}
		return "received", nil
	}
	script := fmt.Sprintf(`set -eu; target=$1; test -f "$target" || { echo "upload is not initialized" >&2; exit 1; }; cat >> "$target"; size=$(wc -c < "$target"); [ "$size" -le %d ] || { rm -f "$target"; echo "upload exceeds 50 MiB" >&2; exit 1; }`, maxUploadSize)
	_, err = runtimeRun(driver, target, script, tempPath, chunk, 30*time.Second)
	return "received", err
}

func rpcUploadFinish(driver, target, containerPath, uploadID string) (string, error) {
	tempPath, err := uploadTempPath(containerPath, uploadID)
	if err != nil {
		return "", err
	}
	if driver != "docker" {
		stagedPath := stagedUploadPath(target, containerPath, uploadID)
		unlock := lockStagedUpload(stagedPath)
		defer func() {
			unlock()
			stagedUploadLocks.Delete(stagedPath)
		}()
		file, err := os.Open(stagedPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", errors.New("upload is not initialized")
			}
			return "", err
		}
		defer file.Close()
		exists, err := runtimeFileExists(driver, target, containerPath)
		if err != nil {
			return "", err
		}
		if exists {
			_ = os.Remove(stagedPath)
			return "", fmt.Errorf("file already exists: %s", containerPath)
		}
		if _, err := runtimeFileRun(driver, target, "write", containerPath, file, 2*time.Minute); err != nil {
			return "", err
		}
		if err := os.Remove(stagedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "uploaded", nil
	}
	_, err = runtimeRun(driver, target, `set -eu; final=$1; temp=$2; test -f "$temp" || { echo "upload is not initialized" >&2; exit 1; }; [ ! -e "$final" ] || { rm -f "$temp"; echo "file already exists: $final" >&2; exit 1; }; mv "$temp" "$final"`, containerPath, nil, 15*time.Second, tempPath)
	return "uploaded", err
}

func rpcUploadCancel(driver, target, containerPath, uploadID string) (string, error) {
	tempPath, err := uploadTempPath(containerPath, uploadID)
	if err != nil {
		return "", err
	}
	if driver != "docker" {
		stagedPath := stagedUploadPath(target, containerPath, uploadID)
		unlock := lockStagedUpload(stagedPath)
		defer func() {
			unlock()
			stagedUploadLocks.Delete(stagedPath)
		}()
		if err := os.Remove(stagedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "cancelled", nil
	}
	_, err = runtimeRun(driver, target, `rm -f "$1"`, tempPath, nil, 15*time.Second)
	return "cancelled", err
}
