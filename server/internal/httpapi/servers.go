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
	s.writeJSON(w, http.StatusOK, map[string]any{
		"servers":       servers,
		"workerVersion": s.workerVersion,
	})
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
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]string{"version": targetVersion})
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

const workerInstall = `#!/bin/sh
set -eu

SERVER_URL=${1:-}
case "$SERVER_URL" in
  http://*|https://*) ;;
  *) echo "usage: install.sh <agentbox-url>" >&2; exit 2 ;;
esac

worker_tmp=$(mktemp)
microsandbox_source_tmp=$(mktemp)
microsandbox_build_tmp=$(mktemp -d)
boxlite_tmp=$(mktemp -d)
trap 'rm -f "$worker_tmp" "$microsandbox_source_tmp"; rm -rf "$microsandbox_build_tmp" "$boxlite_tmp"' EXIT
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64; BOXLITE_ARCH=x86_64 ;;
  aarch64|arm64) ARCH=arm64; BOXLITE_ARCH=aarch64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
curl -fsSL "$SERVER_URL/api/worker/agentbox-worker?arch=$ARCH" -o "$worker_tmp"
curl -fsSL "$SERVER_URL/api/worker/agentbox-microsandbox-driver.go" -o "$microsandbox_source_tmp"

install_host_dependencies() {
  if command -v jq >/dev/null; then
    return 0
  fi
  if ! command -v apt-get >/dev/null; then
    echo "jq is required; automatic installation currently supports apt-based Linux" >&2
    exit 1
  fi
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y ca-certificates jq
}

install_host_dependencies
install -m 0755 "$worker_tmp" /usr/local/bin/agentbox-worker
install -d -m 0755 /usr/local/lib/agentbox
install -d -m 0700 /var/lib/agentbox-worker
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
      GOPROXY=https://goproxy.cn,direct go mod tidy
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

boxlite_call() {
  ACTION=${1:-}
  [ "$#" -gt 0 ] && shift
  case "$ACTION" in
    probe)
      [ -c /dev/kvm ] && [ -x /usr/local/bin/boxlite ] && /usr/local/bin/boxlite info
      ;;
    create)
      TARGET=${1:-}; IMAGE=${2:-}; shift 2
      CPUS=2; MEMORY=4096; WORKDIR=/workspace; NETWORK=enabled
      while [ "$#" -gt 0 ]; do
        case "$1" in
          --cpus) CPUS=${2:-2}; shift 2 ;;
          --memory) MEMORY=${2:-4096}; shift 2 ;;
          --workdir) WORKDIR=${2:-/workspace}; shift 2 ;;
          --network) [ "${2:-}" = none ] && NETWORK=disabled || NETWORK=enabled; shift 2 ;;
          *) echo "unsupported BoxLite create option: $1" >&2; return 2 ;;
        esac
      done
      if ! /usr/local/bin/boxlite inspect "$TARGET" >/dev/null 2>&1; then
        /usr/local/bin/boxlite create --detach --name "$TARGET" --cpus "$CPUS" --memory "$MEMORY" \
          --workdir "$WORKDIR" --network "$NETWORK" "$IMAGE" >/dev/null
      fi
      /usr/local/bin/boxlite start "$TARGET"
      ;;
    inspect) /usr/local/bin/boxlite inspect "$1" ;;
    start) /usr/local/bin/boxlite start "$1" ;;
    stop) /usr/local/bin/boxlite stop "$1" ;;
    delete) /usr/local/bin/boxlite rm --force "$1" ;;
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
        /usr/local/bin/boxlite exec -i -u 0:0 -w "$WORKDIR" "$TARGET" -- "$@"
      elif [ "$USE_STDIN" = true ]; then
        /usr/local/bin/boxlite exec -i -u 0:0 "$TARGET" -- "$@"
      elif [ -n "$WORKDIR" ]; then
        /usr/local/bin/boxlite exec -u 0:0 -w "$WORKDIR" "$TARGET" -- "$@"
      else
        /usr/local/bin/boxlite exec -u 0:0 "$TARGET" -- "$@"
      fi
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
  if [ -x /usr/local/bin/agentbox-worker ]; then
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
    printf '%s\n' "$JOB_ID" > "$CONFIRMED"
    if complete_job "$JOB_ID" true "" "Worker 已更新到 $TARGET_VERSION"; then
      rm -f "$MARKER" "$CONFIRMED"
      [ -z "$ROLLBACK_SCRIPT" ] || rm -f "$ROLLBACK_SCRIPT"
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
  if ! curl -fsSL "$SERVER_URL/api/worker/agentbox-worker?arch=$ARCH&version=$TARGET_VERSION" -o "$WORKER_TMP"; then
    rm -f "$WORKER_TMP"
    return 1
  fi
  chmod 0755 "$WORKER_TMP" || { rm -f "$WORKER_TMP"; return 1; }
  DOWNLOADED_VERSION=$("$WORKER_TMP" version 2>/dev/null || true)
  [ "$DOWNLOADED_VERSION" = "$TARGET_VERSION" ] || {
    echo "downloaded Worker version mismatch: $DOWNLOADED_VERSION" >&2
    rm -f "$WORKER_TMP"
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
[ -n "$JOB_ID" ] || { rm -f "$0"; exit 0; }
if [ -s "$STATE_DIR/worker-update-confirmed" ] &&
   [ "$(cat "$STATE_DIR/worker-update-confirmed")" = "$JOB_ID" ] &&
   systemctl is-active --quiet agentbox-worker.service &&
   [ "$(/usr/local/bin/agentbox-worker version 2>/dev/null || true)" = "$TARGET_VERSION" ]; then
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
  ORIGINAL_JOB_FILE=$2
  JOB_FILE=$(mktemp)
  jq --arg runtimeBase "${SERVER_URL%/}" '
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
      append_env "$ENV_FILE" ANTHROPIC_API_KEY "$CLAUDE_SECRET"
      append_env "$ENV_FILE" ANTHROPIC_AUTH_TOKEN "$CLAUDE_SECRET"
      append_env "$ENV_FILE" ANTHROPIC_BASE_URL "$CLAUDE_ENDPOINT"
      append_env "$ENV_FILE" ANTHROPIC_MODEL "$CLAUDE_MODEL"
    fi
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
  finalize_worker_update || true
  heartbeat_loop &
  HEARTBEAT_PID=$!
  SESSION_PID=
  if [ -x /usr/local/bin/agentbox-worker ]; then
    /usr/local/bin/agentbox-worker session "$CONFIG" &
    SESSION_PID=$!
  fi
  trap 'kill "$HEARTBEAT_PID" >/dev/null 2>&1 || true; [ -z "$SESSION_PID" ] || kill "$SESSION_PID" >/dev/null 2>&1 || true' EXIT INT TERM
  while :; do
    finalize_worker_update || true
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
