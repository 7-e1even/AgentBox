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
	s.writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
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
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"credential": credential,
		"server":     server,
	})
}

func (s *Server) heartbeatServer(w http.ResponseWriter, request *http.Request) {
	credential := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	var capabilities []string
	var inventory *platform.ServerInventory
	if request.ContentLength != 0 {
		var body struct {
			Capabilities []string                  `json:"capabilities"`
			Inventory    *platform.ServerInventory `json:"inventory"`
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
		if inventory != nil {
			platform.NormalizeServerInventory(inventory)
			if err := platform.ValidateServerInventory(*inventory); err != nil {
				s.handleError(w, err)
				return
			}
		}
	}
	if err := s.store.HeartbeatServer(request.Context(), request.PathValue("id"), credential, capabilities, inventory); err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteServer(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteServer(request.Context(), request.PathValue("id")); err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workerInstallScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(workerInstall))
}

func (s *Server) workerScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(workerDaemon))
}

func (s *Server) workerSessionScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	_, _ = w.Write([]byte(workerSessionDaemon))
}

func (s *Server) workerRuntimeDriverScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	_, _ = w.Write([]byte(workerRuntimeDriver))
}

const workerInstall = `#!/bin/sh
set -eu

SERVER_URL=${1:-}
case "$SERVER_URL" in
  http://*|https://*) ;;
  *) echo "usage: install.sh <agentbox-url>" >&2; exit 2 ;;
esac

worker_tmp=$(mktemp)
session_tmp=$(mktemp)
runtime_tmp=$(mktemp)
microsandbox_source_tmp=$(mktemp)
microsandbox_build_tmp=$(mktemp -d)
trap 'rm -f "$worker_tmp" "$session_tmp" "$runtime_tmp" "$microsandbox_source_tmp"; rm -rf "$microsandbox_build_tmp"' EXIT
curl -fsSL "$SERVER_URL/api/worker/agentbox-worker" -o "$worker_tmp"
curl -fsSL "$SERVER_URL/api/worker/agentbox-session-worker" -o "$session_tmp"
curl -fsSL "$SERVER_URL/api/worker/agentbox-runtime-driver" -o "$runtime_tmp"
curl -fsSL "$SERVER_URL/api/worker/agentbox-microsandbox-driver.go" -o "$microsandbox_source_tmp"

install_host_dependencies() {
  if command -v python3 >/dev/null && command -v jq >/dev/null; then
    return 0
  fi
  if ! command -v apt-get >/dev/null; then
    echo "python3 and jq are required; automatic installation currently supports apt-based Linux" >&2
    exit 1
  fi
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y ca-certificates jq python3
}

install_host_dependencies
install -m 0755 "$worker_tmp" /usr/local/bin/agentbox-worker
install -d -m 0755 /usr/local/lib/agentbox
install -m 0644 "$session_tmp" /usr/local/lib/agentbox/session_worker.py
install -m 0755 "$runtime_tmp" /usr/local/lib/agentbox/runtime_driver.py

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

install_boxlite_sdk() {
  [ -c /dev/kvm ] || return 0
  if ! python3 -m venv /opt/agentbox/runtime >/dev/null 2>&1; then
    if command -v apt-get >/dev/null; then
      export DEBIAN_FRONTEND=noninteractive
      apt-get update
      apt-get install -y python3-venv
      python3 -m venv /opt/agentbox/runtime
    else
      echo "warning: python3-venv is unavailable; skipping microVM SDKs" >&2
      return 0
    fi
  fi
  if ! /opt/agentbox/runtime/bin/python -m pip install --disable-pip-version-check --upgrade \
      "boxlite==0.9.7"; then
    echo "warning: BoxLite SDK installation failed; Docker remains available" >&2
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
    if ! go mod download; then
      GOPROXY=https://goproxy.cn,direct go mod download
    fi
    CGO_ENABLED=1 go build -tags agentbox_driver -trimpath -ldflags '-s -w' -o agentbox-microsandbox-driver .
  )
  install -m 0755 "$microsandbox_build_tmp/agentbox-microsandbox-driver" /usr/local/bin/agentbox-microsandbox-driver
  /usr/local/bin/agentbox-microsandbox-driver probe >/dev/null
}

install_microvm_build_dependencies
install_boxlite_sdk
install_microsandbox_driver
migrate_existing_config
if command -v systemctl >/dev/null && systemctl is-active --quiet agentbox-worker.service; then
  systemctl restart agentbox-worker.service
fi
echo "AgentBox Worker installed. Runtime capabilities will be published after self-test."
`

const workerDaemon = `#!/bin/sh
set -eu

CONFIG=/etc/agentbox-worker.conf
STATE_DIR=/var/lib/agentbox-worker

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
  BODY=$(jq -n --arg pairingToken "$TOKEN" --arg serverId "$SERVER_ID" \
    --arg name "$HOST" --arg hostname "$HOST" --arg arch "$ARCH" --argjson capabilities "$CAPS" \
    '{pairingToken:$pairingToken,serverId:$serverId,name:$name,hostname:$hostname,os:"linux",arch:$arch,capabilities:$capabilities}')
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

runtime_python() {
  if [ -x /opt/agentbox/runtime/bin/python ]; then
    printf '%s' /opt/agentbox/runtime/bin/python
  else
    command -v python3
  fi
}

runtime_call() {
  if [ "${1:-}" = microsandbox ]; then
    [ -x /usr/local/bin/agentbox-microsandbox-driver ] || { echo "AgentBox Microsandbox Go driver is unavailable" >&2; return 1; }
    shift
    /usr/local/bin/agentbox-microsandbox-driver "$@"
    return
  fi
  PYTHON=$(runtime_python) || { echo "Python runtime is unavailable" >&2; return 1; }
  [ -r /usr/local/lib/agentbox/runtime_driver.py ] || { echo "AgentBox runtime driver is unavailable" >&2; return 1; }
  "$PYTHON" /usr/local/lib/agentbox/runtime_driver.py "$@"
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
          PARENT=${PATH_VALUE%/*}
          [ -n "$PARENT" ] || PARENT=/
          runtime_call "$AGENTBOX_RUNTIME_DRIVER" exec --stdin "$TARGET" -- \
            sh -c 'set -eu; mkdir -p "$1"; cat > "$2"' sh "$PARENT" "$PATH_VALUE" < "$SOURCE"
          ;;
        *) echo "only host-to-sandbox copy is supported" >&2; return 2 ;;
      esac
      ;;
    *) echo "unsupported compatibility operation for $AGENTBOX_RUNTIME_DRIVER: $OPERATION" >&2; return 2 ;;
  esac
}

worker_capabilities() {
  CAPS=
  command -v docker >/dev/null && docker info >/dev/null 2>&1 && CAPS='"docker"'
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
  if command -v python3 >/dev/null && [ -r /usr/local/lib/agentbox/session_worker.py ]; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"interactive-session\""
  fi
  printf '[%s]' "$CAPS"
}

worker_inventory() {
  ARCH=$(uname -m)
  [ "$ARCH" = x86_64 ] && ARCH=amd64
  [ "$ARCH" = aarch64 ] && ARCH=arm64
  DOCKER_IMAGES='[]'
  if command -v docker >/dev/null && docker info >/dev/null 2>&1; then
    DOCKER_IMAGES=$(docker image ls --no-trunc --format '{{json .}}' 2>/dev/null | jq -s --arg arch "$ARCH" '
      map(select((.Repository | startswith("agentbox/runtime-")) | not) | {
        id: .ID,
        reference: (if .Repository == "<none>" or .Tag == "<none>" then .ID else (.Repository + ":" + .Tag) end),
        architecture: $arch,
        size: .Size,
        created: .CreatedSince,
        format: "oci",
        path: ""
      })
    ' 2>/dev/null || printf '[]')
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
          '. + [{id:$id,reference:$reference,architecture:$arch,size:$size,created:$created,format:$format,path:$path}]' \
          "$VM_FILE" > "$NEXT_FILE"
        mv "$NEXT_FILE" "$VM_FILE"
      done
  fi
  VM_IMAGES=$(cat "$VM_FILE")
  rm -f "$VM_FILE"
  jq -n --argjson dockerImages "$DOCKER_IMAGES" --argjson vmImages "$VM_IMAGES" --arg vmImageDirectory "$VM_DIR" \
    '{dockerImages:$dockerImages,vmImages:$vmImages,vmImageDirectory:$vmImageDirectory}'
}

complete_job() {
  JOB_ID=$1
  SUCCESS=$2
  EXTERNAL_ID=$3
  MESSAGE=$4
  BODY=$(jq -n \
    --argjson success "$SUCCESS" \
    --arg externalId "$EXTERNAL_ID" \
    --arg message "$MESSAGE" \
    '{success:$success,externalId:$externalId,message:$message}')
  curl -fsS -X POST \
    "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/complete" \
    -H "Authorization: Bearer $CREDENTIAL" \
    -H 'Content-Type: application/json' \
    --data "$BODY" >/dev/null
}

agent_tool_command() {
  case "$1" in
    antigravity) printf '%s' agy ;;
    claude-code) printf '%s' claude ;;
    codebuddy) printf '%s' codebuddy ;;
    codex) printf '%s' codex ;;
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

install_agent_tools() {
  CONTAINER=$1
  JOB_FILE=$2
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE" | sort -u)
  [ -n "$TOOLS" ] || return 0
  docker exec "$CONTAINER" sh -lc \
    'export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get install -y curl ca-certificates git jq unzip python3' >&2
  set --
  for TOOL in $TOOLS; do
    COMMAND=$(agent_tool_command "$TOOL") || { echo "unsupported Agent tool: $TOOL" >&2; return 1; }
    if docker exec "$CONTAINER" sh -lc "command -v $COMMAND >/dev/null"; then
      continue
    fi
    case "$TOOL" in
      antigravity|cursor|qwenpaw) continue ;;
      codex) PACKAGE='@openai/codex' ;;
      claude-code) PACKAGE='@anthropic-ai/claude-code' ;;
      codebuddy) PACKAGE='@tencent-ai/codebuddy-code' ;;
      gemini-cli) PACKAGE='@google/gemini-cli' ;;
      grok) PACKAGE='@xai-official/grok' ;;
      kimi) PACKAGE='@moonshot-ai/kimi-code' ;;
      omp) PACKAGE='@oh-my-pi/pi-coding-agent' ;;
      openclaw) PACKAGE='openclaw' ;;
      opencode) PACKAGE='opencode-ai' ;;
      pi) PACKAGE='@mariozechner/pi-coding-agent' ;;
      copilot-cli) PACKAGE='@github/copilot' ;;
      qoder-cli) PACKAGE='@qoder-ai/qodercli' ;;
      qoder-cn) PACKAGE='@qodercn-ai/qoderclicn' ;;
      qwen-code) PACKAGE='@qwen-code/qwen-code' ;;
      reasonix) PACKAGE='reasonix' ;;
      deveco) echo "DevEco Code does not support Linux sandboxes; use a custom prebuilt environment" >&2; return 1 ;;
      trae-cli) echo "TRAE CLI has no verified unattended Linux installer; use a custom prebuilt environment" >&2; return 1 ;;
      *) echo "unsupported Agent tool: $TOOL" >&2; return 1 ;;
    esac
    set -- "$@" "$PACKAGE@latest"
  done

  if printf '%s\n' "$TOOLS" | grep -qx omp &&
     ! docker exec "$CONTAINER" sh -lc 'command -v bun >/dev/null'; then
    docker exec "$CONTAINER" sh -lc \
      'set -eu; INSTALLER=$(mktemp); trap '\''rm -f "$INSTALLER"'\'' EXIT; curl --connect-timeout 15 --max-time 300 -fsSL https://bun.sh/install -o "$INSTALLER"; bash "$INSTALLER" >/dev/null; test -x /root/.bun/bin/bun; ln -sf /root/.bun/bin/bun /usr/bin/bun' >&2
  fi
  if [ "$#" -gt 0 ]; then
    docker exec "$CONTAINER" sh -lc \
      'if ! node --version 2>/dev/null | grep -q "^v22\."; then curl -fsSL https://deb.nodesource.com/setup_22.x | sh; export DEBIAN_FRONTEND=noninteractive; apt-get install -y nodejs; fi' >&2
    docker exec "$CONTAINER" npm install -g "$@" >&2
  fi

  for TOOL in $TOOLS; do
    COMMAND=$(agent_tool_command "$TOOL") || return 1
    if docker exec "$CONTAINER" sh -lc "command -v $COMMAND >/dev/null"; then
      continue
    fi
    case "$TOOL" in
      antigravity)
        docker exec "$CONTAINER" sh -lc \
          'set -eu; case "$(uname -m)" in x86_64|amd64) PLATFORM=linux_amd64 ;; aarch64|arm64) PLATFORM=linux_arm64 ;; *) exit 1 ;; esac; TMP_DIR=$(mktemp -d); trap '\''rm -rf "$TMP_DIR"'\'' EXIT; MANIFEST=$(curl --connect-timeout 15 --max-time 60 -fsSL "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/$PLATFORM.json"); URL=$(printf "%s" "$MANIFEST" | jq -r .url); SHA512=$(printf "%s" "$MANIFEST" | jq -r .sha512); curl --connect-timeout 15 --max-time 300 -fsSL "$URL" -o "$TMP_DIR/agy.tar.gz"; printf "%s  %s\n" "$SHA512" "$TMP_DIR/agy.tar.gz" | sha512sum -c -; tar -xzf "$TMP_DIR/agy.tar.gz" -C "$TMP_DIR" antigravity; install -m 0755 "$TMP_DIR/antigravity" /usr/bin/agy' >&2
        ;;
      cursor)
        docker exec "$CONTAINER" sh -lc \
          'set -eu; INSTALLER=$(mktemp); trap '\''rm -f "$INSTALLER"'\'' EXIT; curl --connect-timeout 15 --max-time 300 -fsSL https://cursor.com/install -o "$INSTALLER"; bash "$INSTALLER"; if [ -x /root/.local/bin/cursor-agent ]; then ln -sf /root/.local/bin/cursor-agent /usr/bin/cursor-agent; elif [ -x /root/.cursor/bin/cursor-agent ]; then ln -sf /root/.cursor/bin/cursor-agent /usr/bin/cursor-agent; else exit 1; fi' >&2
        ;;
      qwenpaw)
        docker exec "$CONTAINER" sh -lc \
          'set -eu; INSTALLER=$(mktemp); trap '\''rm -f "$INSTALLER"'\'' EXIT; curl --connect-timeout 15 --max-time 300 -LsSf https://astral.sh/uv/install.sh -o "$INSTALLER"; sh "$INSTALLER" >/dev/null; timeout 3600 /root/.local/bin/uv tool install --python 3.12 qwenpaw; test -x /root/.local/bin/qwenpaw; ln -sf /root/.local/bin/qwenpaw /usr/bin/qwenpaw' >&2
        ;;
    esac
  done

  VERSION_FILE=$(mktemp)
  printf '{}\n' > "$VERSION_FILE"
  for TOOL in $TOOLS; do
    COMMAND=$(agent_tool_command "$TOOL") || { rm -f "$VERSION_FILE"; return 1; }
    docker exec "$CONTAINER" sh -lc "command -v $COMMAND >/dev/null" || {
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
  done
  docker exec "$CONTAINER" mkdir -p /opt/agentbox
  docker cp "$VERSION_FILE" "$CONTAINER:/opt/agentbox/agent-versions.json" >/dev/null
  rm -f "$VERSION_FILE"
}

best_cached_agent_base() {
  BASE_IMAGE=$1
  REQUESTED_TOOLS=$2
  NOW=$3
  TTL_SECONDS=$4
  REQUESTED_COUNT=$(printf '%s\n' "$REQUESTED_TOOLS" | grep -c .)
  BEST_IMAGE=$BASE_IMAGE
  BEST_COUNT=0
  for IMAGE_ID in $(docker image ls -q --filter label=agentbox.runtime.cache=true | sort -u); do
    CANDIDATE_BASE=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.base" }}' "$IMAGE_ID" 2>/dev/null || true)
    [ "$CANDIDATE_BASE" = "$BASE_IMAGE" ] || continue
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

prepare_agent_image() {
  BASE_IMAGE=$1
  JOB_FILE=$2
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE" | sort -u)
  [ -n "$TOOLS" ] || { printf '%s' "$BASE_IMAGE"; return 0; }
  command -v sha256sum >/dev/null || { echo "sha256sum is required for Agent runtime caching" >&2; return 1; }
  command -v paste >/dev/null || { echo "paste is required for Agent runtime caching" >&2; return 1; }

  TOOL_SET=$(printf '%s\n' "$TOOLS" | paste -sd, -)
  CACHE_KEY=$(printf '%s\n%s\n' "$BASE_IMAGE" "$TOOL_SET" | sha256sum | cut -c1-16)
  CACHE_IMAGE="agentbox/runtime-$CACHE_KEY:latest"
  REFRESH_IMAGE="agentbox/runtime-$CACHE_KEY:refresh-$$"
  BUILD_CONTAINER="agentbox-runtime-build-$CACHE_KEY"
  NOW=$(date +%s)
  TTL_HOURS=${AGENTBOX_AGENT_IMAGE_TTL_HOURS:-24}
  case "$TTL_HOURS" in ''|*[!0-9]*) TTL_HOURS=24 ;; esac
  TTL_SECONDS=$((TTL_HOURS * 3600))

  CACHED_AT=$(docker image inspect -f '{{ index .Config.Labels "agentbox.runtime.refreshed" }}' "$CACHE_IMAGE" 2>/dev/null || true)
  case "$CACHED_AT" in
    ''|*[!0-9]*) ;;
    *)
      AGE=$((NOW - CACHED_AT))
      if [ "$AGE" -ge 0 ] && [ "$AGE" -lt "$TTL_SECONDS" ]; then
        printf '%s' "$CACHE_IMAGE"
        return 0
      fi
      ;;
  esac

  BUILD_BASE_IMAGE=$(best_cached_agent_base "$BASE_IMAGE" "$TOOLS" "$NOW" "$TTL_SECONDS")

  if docker inspect "$BUILD_CONTAINER" >/dev/null 2>&1; then
    docker start "$BUILD_CONTAINER" >/dev/null
  else
    docker run -d --name "$BUILD_CONTAINER" \
      --label "agentbox.runtime.build=$CACHE_KEY" \
      "$BUILD_BASE_IMAGE" sh -lc 'trap : TERM INT; sleep infinity & wait' >/dev/null
  fi
  if install_agent_tools "$BUILD_CONTAINER" "$JOB_FILE" &&
     docker commit \
       --change "LABEL agentbox.runtime.cache=true" \
       --change "LABEL agentbox.runtime.refreshed=$NOW" \
       --change "LABEL agentbox.runtime.base=$BASE_IMAGE" \
       --change "LABEL agentbox.runtime.tools=$TOOL_SET" \
       "$BUILD_CONTAINER" "$REFRESH_IMAGE" >/dev/null &&
     docker tag "$REFRESH_IMAGE" "$CACHE_IMAGE"; then
    docker rm -f "$BUILD_CONTAINER" >/dev/null 2>&1 || true
    docker image rm "$REFRESH_IMAGE" >/dev/null 2>&1 || true
    printf '%s' "$CACHE_IMAGE"
    return 0
  fi

  docker stop --time 10 "$BUILD_CONTAINER" >/dev/null 2>&1 || true
  docker image rm "$REFRESH_IMAGE" >/dev/null 2>&1 || true
  if docker image inspect "$CACHE_IMAGE" >/dev/null 2>&1; then
    echo "Agent runtime refresh failed; using the last working cached image" >&2
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

configure_credentials() {
  CONTAINER=$1
  JOB_FILE=$2
  CREDENTIAL_COUNT=$(jq '.job.payload.credentials | length' "$JOB_FILE")
  [ "$CREDENTIAL_COUNT" -gt 0 ] || return 0

  ENV_FILE=$(mktemp)
  chmod 600 "$ENV_FILE"
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
      select((.protocol == "anthropic" or .protocol == "openai-chat") and
        (.endpoint // "") != "" and (.modelId // "") != "")][0] // empty' "$JOB_FILE")
    if [ -n "$KIMI_CREDENTIAL" ]; then
      KIMI_PROVIDER=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.providerId')
      KIMI_PROTOCOL=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.protocol')
      KIMI_SECRET=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.secret')
      KIMI_ENDPOINT=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.endpoint')
      KIMI_MODEL=$(printf '%s' "$KIMI_CREDENTIAL" | jq -r '.modelId')
      if [ "$KIMI_PROVIDER" = kimi ] || [ "$KIMI_PROVIDER" = moonshot ]; then
        KIMI_PROVIDER_TYPE=kimi
      elif [ "$KIMI_PROTOCOL" = anthropic ]; then
        KIMI_PROVIDER_TYPE=anthropic
      else
        KIMI_PROVIDER_TYPE=openai
      fi
      append_env "$ENV_FILE" KIMI_MODEL_NAME "$KIMI_MODEL"
      append_env "$ENV_FILE" KIMI_MODEL_API_KEY "$KIMI_SECRET"
      append_env "$ENV_FILE" KIMI_MODEL_PROVIDER_TYPE "$KIMI_PROVIDER_TYPE"
      append_env "$ENV_FILE" KIMI_MODEL_BASE_URL "$KIMI_ENDPOINT"
    fi
  fi

  docker exec "$CONTAINER" mkdir -p /opt/agentbox/secrets
  docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /opt/agentbox/secrets/agentbox.env' < "$ENV_FILE"
  rm -f "$ENV_FILE"

  CODEX_CREDENTIAL=$(jq -c '[.job.payload.credentials[] | select(.protocol == "openai-responses")][0] // empty' "$JOB_FILE")
  if [ -n "$CODEX_CREDENTIAL" ]; then
    CODEX_ID=$(printf '%s' "$CODEX_CREDENTIAL" | jq -r '.id')
    CODEX_ENV_ID=$(printf '%s' "$CODEX_ID" | tr '[:lower:]-' '[:upper:]_')
    CODEX_ENDPOINT=$(printf '%s' "$CODEX_CREDENTIAL" | jq -r '.endpoint // empty')
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
           apiKey: ("AGENTBOX_KEY_" + (.id | env_id)),
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
    docker exec -i "$CONTAINER" sh -c "cat > /opt/agentbox/skills/$ID/SKILL.md" < "$SKILL_FILE"
    for TARGET in \
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
      docker exec "$CONTAINER" mkdir -p "$TARGET"
      docker exec -i "$CONTAINER" sh -c "cat > $TARGET/SKILL.md" < "$SKILL_FILE"
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
    CLAUDE_CONFIG=$(mktemp)
    jq '{mcpServers: (.job.payload.mcpServers | map(
      . as $server | {key: .id, value:
        (if .spec.transport == "stdio" then
          {type: "stdio", command: .spec.command,
           args: ((.spec.args // "") | [scan("[^[:space:]]+")])}
        else {type: "http", url: .spec.url} end)}
    ) | from_entries)}' "$JOB_FILE" > "$CLAUDE_CONFIG"
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.claude.json' < "$CLAUDE_CONFIG"
    rm -f "$CLAUDE_CONFIG"
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
    printf '#!/bin/sh\nset -a\n. /opt/agentbox/secrets/agentbox.env\nset +a\nexec /usr/bin/%s "$@"\n' "$COMMAND" > "$WRAPPER"
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

  if [ "$DRIVER" = docker ]; then
    command -v docker >/dev/null || { echo "Docker is not installed" >&2; return 1; }
    docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable" >&2; return 1; }
    MEMORY=$(printf '%s' "$MEMORY_VALUE" | tr -d ' ' | sed -e 's/GiB$/g/' -e 's/MiB$/m/')
    if docker inspect "$TARGET" >/dev/null 2>&1; then
      OWNER=$(docker inspect -f '{{ index .Config.Labels "agentbox.sandbox" }}' "$TARGET")
      [ "$OWNER" = "$SANDBOX_ID" ] || { echo "container name collision: $TARGET" >&2; return 1; }
      docker start "$TARGET" >/dev/null
    else
      RUNTIME_IMAGE=$(prepare_agent_image "$IMAGE" "$JOB_FILE")
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
  else
    runtime_probe "$DRIVER" || { echo "$DRIVER SDK self-test failed" >&2; return 1; }
    CPU_COUNT=$(printf '%s' "${CPU:-2}" | sed 's/\..*$//')
    case "$CPU_COUNT" in ''|*[!0-9]*) CPU_COUNT=2 ;; esac
    MEMORY_MIB=$(memory_mib "$MEMORY_VALUE")
    runtime_call "$DRIVER" create "$TARGET" "$IMAGE" --cpus "$CPU_COUNT" \
      --memory "$MEMORY_MIB" --workdir "$WORKDIR" --network "$NETWORK" >/dev/null
    install_agent_tools "$TARGET" "$JOB_FILE"
  fi

  docker exec "$TARGET" mkdir -p /opt/agentbox
  MANIFEST=$(mktemp)
  jq '.job.payload | del(.credentials)' "$JOB_FILE" > "$MANIFEST"
  docker cp "$MANIFEST" "$TARGET:/opt/agentbox/manifest.json" >/dev/null
  rm -f "$MANIFEST"
  configure_credentials "$TARGET" "$JOB_FILE"
  configure_skills "$TARGET" "$JOB_FILE"
  configure_mcp_servers "$TARGET" "$JOB_FILE"
  install_agent_wrappers "$TARGET" "$JOB_FILE"
  [ -z "$SETUP" ] || docker exec "$TARGET" sh -lc "$SETUP" >&2
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
  TARGET=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  if [ "$DRIVER" = docker ]; then
    docker inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
    docker start "$TARGET" >/dev/null
  else
    runtime_call "$DRIVER" start "$TARGET" >/dev/null
  fi

  if jq -e '.job.payload.credentials? and .job.payload.agentTools?' "$JOB_FILE" >/dev/null; then
    install_agent_tools "$TARGET" "$JOB_FILE"
    configure_credentials "$TARGET" "$JOB_FILE"
    configure_skills "$TARGET" "$JOB_FILE"
    configure_mcp_servers "$TARGET" "$JOB_FILE"
    install_agent_wrappers "$TARGET" "$JOB_FILE"
  fi
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

login_agent() {
  JOB_FILE=$1
  CONTAINER=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  export AGENTBOX_RUNTIME_DRIVER
  TOOL=$(jq -r '.job.payload.tool // empty' "$JOB_FILE")
  docker inspect "$CONTAINER" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
  case "$TOOL" in
    codex)
      docker exec "$CONTAINER" test -x /usr/bin/codex || { echo "Codex is not installed in this sandbox" >&2; return 1; }
      if docker exec "$CONTAINER" /usr/bin/codex login status >/dev/null 2>&1; then
        printf 'Codex 已经登录，无需重复操作。'
        return 0
      fi
      docker exec "$CONTAINER" sh -lc '
        mkdir -p /opt/agentbox/login
        chmod 700 /opt/agentbox/login
        : > /opt/agentbox/login/codex.log
        chmod 600 /opt/agentbox/login/codex.log
        nohup timeout 600 /usr/bin/codex login --device-auth \
          > /opt/agentbox/login/codex.log 2>&1 </dev/null &
      '
      ATTEMPT=0
      while [ "$ATTEMPT" -lt 10 ]; do
        MESSAGE=$(docker exec "$CONTAINER" sh -c 'cat /opt/agentbox/login/codex.log' | \
          sed 's/\x1b\[[0-9;]*m//g' | tail -c 3500)
        if [ -n "$MESSAGE" ]; then
          if printf '%s' "$MESSAGE" | grep -Eiq '(^|[[:space:]])(error|failed|forbidden)([[:space:]:]|$)'; then
            printf '%s' "$MESSAGE" >&2
            return 1
          fi
          printf '%s' "$MESSAGE"
          return 0
        fi
        ATTEMPT=$((ATTEMPT + 1))
        sleep 1
      done
      echo "Codex did not return a device login code" >&2
      return 1
      ;;
    *) echo "Unsupported Agent login: $TOOL" >&2; return 1 ;;
  esac
}

process_job() {
  JOB_FILE=$1
  JOB_ID=$(jq -r '.job.id' "$JOB_FILE")
  ACTION=$(jq -r '.job.action' "$JOB_FILE")
  case "$ACTION" in
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
    login-agent)
      LOG_FILE=$(mktemp)
      if MESSAGE=$(login_agent "$JOB_FILE" 2>"$LOG_FILE"); then
        complete_job "$JOB_ID" true "$(sandbox_container_name "$JOB_FILE")" "$MESSAGE"
      else
        MESSAGE=$(tail -c 3500 "$LOG_FILE")
        complete_job "$JOB_ID" false "$(sandbox_container_name "$JOB_FILE")" "$MESSAGE"
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
      '{capabilities:$capabilities,inventory:$inventory}')
    curl -fsS -X POST "$SERVER_URL/api/servers/$SERVER_ID/heartbeat" \
      -H "Authorization: Bearer $CREDENTIAL" \
      -H 'Content-Type: application/json' --data "$HEARTBEAT" >/dev/null 2>&1 || true
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
  heartbeat_loop &
  HEARTBEAT_PID=$!
  SESSION_PID=
  if SESSION_PYTHON=$(runtime_python 2>/dev/null) && [ -r /usr/local/lib/agentbox/session_worker.py ]; then
    "$SESSION_PYTHON" /usr/local/lib/agentbox/session_worker.py "$CONFIG" &
    SESSION_PID=$!
  fi
  trap 'kill "$HEARTBEAT_PID" >/dev/null 2>&1 || true; [ -z "$SESSION_PID" ] || kill "$SESSION_PID" >/dev/null 2>&1 || true' EXIT INT TERM
  while :; do
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
