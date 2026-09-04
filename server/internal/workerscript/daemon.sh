#!/bin/sh
set -eu

CONFIG=/etc/agentbox-worker.conf
STATE_DIR=/var/lib/agentbox-worker
BOXLITE_CONFIG=$STATE_DIR/boxlite.json
BOXLITE_URL=http://127.0.0.1:48100
BOXLITE_LOG=$STATE_DIR/boxlite-serve.log
BOXLITE_IMAGES_FILE=$STATE_DIR/boxlite-images.json
WORKER_OCI_IMAGE_DIR=$STATE_DIR/oci-images
BOXLITE_SERVER_PID_FILE=$STATE_DIR/boxlite-serve.pid

normalize_worker_origin() {
  WORKER_ORIGIN=${1:-}
  while [ "${WORKER_ORIGIN%/}" != "$WORKER_ORIGIN" ]; do WORKER_ORIGIN=${WORKER_ORIGIN%/}; done
  case "$WORKER_ORIGIN" in
    *[[:space:]]*|*\?*|*\#*|*@*)
      echo "Worker URL must be an HTTP(S) origin without credentials, path, query, or fragment" >&2
      return 1
      ;;
    https://*) WORKER_AUTHORITY=${WORKER_ORIGIN#https://}; WORKER_PLAIN_HTTP=false ;;
    http://*) WORKER_AUTHORITY=${WORKER_ORIGIN#http://}; WORKER_PLAIN_HTTP=true ;;
    *) echo "Worker URL must use HTTPS, or HTTP on an exact loopback host" >&2; return 1 ;;
  esac
  case "$WORKER_AUTHORITY" in ''|*/*) echo "Worker URL must contain only an origin" >&2; return 1 ;; esac
  WORKER_HOST=$WORKER_AUTHORITY
  case "$WORKER_AUTHORITY" in
    \[*\]:*)
      WORKER_HOST=${WORKER_AUTHORITY%%]*}
      WORKER_HOST=${WORKER_HOST#\[}
      WORKER_PORT=${WORKER_AUTHORITY#*]:}
      case "$WORKER_PORT" in ''|*[!0-9]*) echo "Worker URL port is invalid" >&2; return 1 ;; esac
      ;;
    \[*\]) WORKER_HOST=${WORKER_AUTHORITY#\[}; WORKER_HOST=${WORKER_HOST%\]} ;;
    *:*)
      WORKER_HOST=${WORKER_AUTHORITY%:*}
      WORKER_PORT=${WORKER_AUTHORITY##*:}
      case "$WORKER_HOST" in ''|*:*) echo "Worker URL authority is invalid" >&2; return 1 ;; esac
      case "$WORKER_PORT" in ''|*[!0-9]*) echo "Worker URL port is invalid" >&2; return 1 ;; esac
      ;;
  esac
  [ -n "$WORKER_HOST" ] || { echo "Worker URL host is required" >&2; return 1; }
  if [ "$WORKER_PLAIN_HTTP" = true ]; then
    case "$WORKER_HOST" in
      localhost|::1) ;;
      127.*)
        printf '%s\n' "$WORKER_HOST" | grep -Eq '^127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$' || {
          echo "Plain HTTP is allowed only for an exact localhost or loopback IP" >&2
          return 1
        }
        OLD_IFS=$IFS; IFS=.; set -- $WORKER_HOST; IFS=$OLD_IFS
        for WORKER_OCTET in "$@"; do
          [ "$WORKER_OCTET" -le 255 ] || {
            echo "Plain HTTP is allowed only for an exact localhost or loopback IP" >&2
            return 1
          }
        done
        ;;
      *) echo "Plain HTTP is allowed only for an exact localhost or loopback IP" >&2; return 1 ;;
    esac
  fi
  printf '%s' "$WORKER_ORIGIN"
}

worker_origin_curl() {
  case "$SERVER_URL" in
    https://*) curl --proto '=https' --proto-redir '=https' "$@" ;;
    http://*) curl --proto '=http' "$@" ;;
  esac
}

worker_origin_curl_follow() {
  case "$SERVER_URL" in
    https://*) curl --proto '=https' --proto-redir '=https' -L "$@" ;;
    http://*) curl --proto '=http' "$@" ;;
  esac
}

usage() {
  echo "usage: agentbox-worker setup --server <url> --token <pairing-token>" >&2
  echo "       agentbox-worker run|status|recover-update --restore-previous" >&2
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
  SERVER_URL=$(normalize_worker_origin "$SERVER_URL") || exit 2
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
  RESPONSE=$(worker_origin_curl -fsS -X POST "$SERVER_URL/api/servers/register" -H 'Content-Type: application/json' --data "$BODY")
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
RestartSec=2
KillMode=control-group
TimeoutStopSec=20s

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
  boxlite_local_cli start "$TARGET" >/dev/null 2>&1 || true
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
  [ -n "$CAPS" ] && CAPS="$CAPS,"
  CAPS="$CAPS\"sandbox-extensions\",\"mcp-managed-config\",\"managed-capability-config\",\"fail-closed-job-output\""
  if [ -e "$STATE_DIR/worker-update.json" ]; then
    CAPS="$CAPS,\"worker-update-pending\""
    if ! worker_update_marker_valid "$STATE_DIR/worker-update.json"; then
      CAPS="$CAPS,\"worker-update-degraded\""
    fi
  fi
  printf '[%s]' "$CAPS"
}

worker_inventory() {
	ARCH=$(worker_arch)
	DOCKER_IMAGES='[]'
  if printf '%s' "$1" | jq -e 'index("docker") != null' >/dev/null; then
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

# Runs in a subshell so response parsing cannot overwrite the active job's
# variables. Missing selection is the n-1 Server's existing v1 wire contract.
worker_request() (
  RESPONSE_FILE=$1
  shift
  RESPONSE_HEADERS=$(mktemp)
  trap 'rm -f "$RESPONSE_HEADERS"' EXIT
  RESPONSE_STATUS=$(worker_origin_curl -sS --connect-timeout 10 --max-time 30 \
    -o "$RESPONSE_FILE" -D "$RESPONSE_HEADERS" -w '%{http_code}' \
    -H 'X-AgentBox-Worker-Protocol-Min: 1' \
    -H 'X-AgentBox-Worker-Protocol-Max: 1' "$@") || return 1
  SELECTED_PROTOCOL=$(tr -d '\r' < "$RESPONSE_HEADERS" |
    awk 'tolower($1) == "x-agentbox-worker-protocol:" { sub(/^[^:]*:[ \t]*/, ""); sub(/[ \t]*$/, ""); selected = $0 } END { print selected }')
  case "$RESPONSE_STATUS" in
    426)
      echo "Worker protocol is incompatible with the Server; install its matching Worker release" >&2
      printf '%s' 426
      return 1
      ;;
    2??)
      case "$SELECTED_PROTOCOL" in
        ''|1) ;;
        *)
          echo "Server selected unsupported Worker protocol: $SELECTED_PROTOCOL; refusing the response" >&2
          printf '%s' 426
          return 1
          ;;
      esac
      ;;
  esac
  printf '%s' "$RESPONSE_STATUS"
)

complete_job() {
  JOB_ID=$1
  SUCCESS=$2
  EXTERNAL_ID=$3
  MESSAGE=$4
  EXIT_CODE=${5:-}
  OUTPUT_FILE=${6:-/dev/null}
  OUTPUT_TRUNCATED=${7:-false}
  TIMED_OUT=${8:-false}
  ERROR_CODE=${9:-}
  ERROR_STAGE=${10:-}
  ERROR_RETRYABLE=${11:-false}
  ERROR_DETAILS=${12:-}
  [ -n "$ERROR_DETAILS" ] || ERROR_DETAILS='{}'
  AGENT_TOOLS_FILE=${13:-/dev/null}
  LEASE_GENERATION=${LEASE_GENERATION:-0}
  BODY=$(jq -n \
	--argjson leaseGeneration "$LEASE_GENERATION" \
    --argjson success "$SUCCESS" \
    --arg externalId "$EXTERNAL_ID" \
    --arg message "$MESSAGE" \
    --arg exitCode "$EXIT_CODE" \
    --rawfile output "$OUTPUT_FILE" \
    --argjson outputTruncated "$OUTPUT_TRUNCATED" \
    --argjson timedOut "$TIMED_OUT" \
    --arg errorCode "$ERROR_CODE" \
    --arg errorStage "$ERROR_STAGE" \
    --argjson errorRetryable "$ERROR_RETRYABLE" \
    --argjson errorDetails "$ERROR_DETAILS" \
    --slurpfile agentTools "$AGENT_TOOLS_FILE" \
	'{leaseGeneration:$leaseGeneration,success:$success,externalId:$externalId,message:$message,
      exitCode:(if $exitCode == "" then null else ($exitCode | tonumber) end),
      output:$output,outputTruncated:$outputTruncated,timedOut:$timedOut,
      agentTools:($agentTools[0] // []),
      error:(if $success or $errorCode == "" then null else
        ({code:$errorCode,retryable:$errorRetryable,details:$errorDetails} +
         if $errorStage == "" then {} else {stage:$errorStage} end)
      end)}')
  COMPLETE_STATUS=$(worker_request /dev/null -X POST \
    "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/complete" \
    -H "Authorization: Bearer $CREDENTIAL" \
    -H 'Content-Type: application/json' \
    --data "$BODY") || return 1
  [ "$COMPLETE_STATUS" = 204 ]
}

complete_job_failure() {
  JOB_ID=$1
  ERROR_CODE=$2
  ERROR_STAGE=$3
  ERROR_RETRYABLE=$4
  MESSAGE=$5
  ACTION=$6
  WORKER_VERSION=${7:-}
  ERROR_DETAILS=$(jq -cn --arg action "$ACTION" --arg workerVersion "$WORKER_VERSION" \
    '{action:$action} + (if $workerVersion == "" then {} else {workerVersion:$workerVersion} end)')
  complete_job "$JOB_ID" false "" "$MESSAGE" "" /dev/null false false \
    "$ERROR_CODE" "$ERROR_STAGE" "$ERROR_RETRYABLE" "$ERROR_DETAILS"
}

complete_agent_tool_job() {
  JOB_ID=$1
  SUCCESS=$2
  MESSAGE=$3
  RESULT_FILE=$4
  ACTION=$5
  ERROR_CODE=${6:-}
  ERROR_DETAILS=$(jq -cn --arg action "$ACTION" '{action:$action}')
  complete_job "$JOB_ID" "$SUCCESS" "" "$MESSAGE" "" /dev/null false false \
    "$ERROR_CODE" agent-tools false "$ERROR_DETAILS" "$RESULT_FILE"
}

worker_error_stage_from_log() {
  WORKER_ERROR_LOG=$1
  [ -f "$WORKER_ERROR_LOG" ] || return 1
  WORKER_ERROR_STAGE=$(sed -n 's/.*stage \([a-z0-9_-]*\) failed:.*/\1/p' \
    "$WORKER_ERROR_LOG" | tail -n 1)
  case "$WORKER_ERROR_STAGE" in
    agent-tools|agent-wrappers|cleanup|create|create-result|credentials|\
    delete|desktop-config|desktop-start|extensions|image-prepare|\
    manifest-write|mcp|network-policy|proxy-check|proxy-config|restart|\
    runtime-create|runtime-image|runtime-probe|setup-command|skills|start|\
    stop|variables|worker-update|workspace-init)
      printf '%s' "$WORKER_ERROR_STAGE"
      ;;
    *) return 1 ;;
  esac
}

worker_output_was_withheld() {
  case "$1" in
    "[job output withheld:"*|"[job log unavailable:"*|"[job output exceeded "*) return 0 ;;
    *) return 1 ;;
  esac
}

worker_safe_stage_message() {
  printf 'Sandbox operation failed during stage %s; detailed output was withheld' "$1"
}

worker_error_retryable() {
  case "$1" in
    runtime-probe|runtime-image|image-prepare|runtime-create) printf '%s' true ;;
    *) printf '%s' false ;;
  esac
}

report_job_progress() {
  [ -n "${JOB_ID:-}" ] || return 0
  STAGE=$1
  MESSAGE=$2
  CACHE_STATUS=${3:-}
  CACHE_REASON=${4:-}
  AGENT_TOOL=${5:-}
  AGENT_TOOL_STATUS=${6:-}
  LEASE_GENERATION=${LEASE_GENERATION:-0}
  BODY=$(jq -n --argjson leaseGeneration "$LEASE_GENERATION" \
	--arg stage "$STAGE" --arg message "$MESSAGE" \
    --arg cacheStatus "$CACHE_STATUS" --arg cacheReason "$CACHE_REASON" \
    --arg agentTool "$AGENT_TOOL" --arg agentToolStatus "$AGENT_TOOL_STATUS" \
	'{leaseGeneration:$leaseGeneration,stage:$stage,message:$message,
      cacheStatus:(if $cacheStatus == "" then null else $cacheStatus end),
      cacheReason:(if $cacheReason == "" then null else $cacheReason end),
      agentTool:(if $agentTool == "" then null else $agentTool end),
      agentToolStatus:(if $agentToolStatus == "" then null else $agentToolStatus end)}') || return 0
  worker_request /dev/null -X POST \
    "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/progress" \
    -H "Authorization: Bearer $CREDENTIAL" \
    -H 'Content-Type: application/json' \
    --data "$BODY" >/dev/null || true
}

report_agent_tool_progress() {
  TOOL=$1
  STATUS=$2
  MESSAGE=$3
  report_job_progress "$PROGRESS_STAGE" "$MESSAGE" "" "" "$TOOL" "$STATUS"
}

worker_update_lease_keepalive() {
  while :; do
    sleep 30
    report_job_progress worker-update "Worker update is still in progress"
  done
}

worker_update_marker_valid() {
  MARKER=$1
  [ -s "$MARKER" ] || return 1
  jq -e '
    def nonempty: type == "string" and length > 0;
    def version: nonempty and startswith("v") and length <= 64;
    def checksum: type == "string" and test("^[0-9A-Fa-f]{64}$");
    (.jobId | nonempty) and
    (.targetVersion | version) and
    ((.formatVersion // 1) == 1 or (.formatVersion // 1) == 2) and
    (if (.formatVersion // 1) == 2 then
      (.leaseGeneration | type == "number" and . >= 0 and floor == .) and
      ((.phase == "prepared") or (.phase == "worker-active") or
       (.phase == "activated") or (.phase == "rolled-back")) and
      (.workerPreviousVersion | version) and
      (.rollbackScript == "/var/lib/agentbox-worker/worker-update-rollback.sh") and
      (.workerPrevious == "/usr/local/lib/agentbox/agentbox-worker.previous") and
      (.workerChecksum | checksum) and
      (.workerPreviousChecksum | checksum) and
      (.driverUpdated | type == "boolean") and
      (.driverHadPrevious | type == "boolean") and
      (.guardianTimer == "agentbox-worker-update-rollback.timer") and
      (.guardianService == "agentbox-worker-update-rollback.service") and
      (if .driverUpdated then
        (.driverPrevious == "/usr/local/lib/agentbox/agentbox-microsandbox-driver.previous") and
        (.driverChecksum | checksum) and
        (if .driverHadPrevious then (.driverPreviousChecksum | checksum)
         else .driverPreviousChecksum == "" end)
       else true end)
     else true end)
  ' "$MARKER" >/dev/null 2>&1
}

worker_update_lock() {
  command -v flock >/dev/null || {
    echo "flock is required for crash-safe Worker updates" >&2
    return 1
  }
  exec 9>"$STATE_DIR/worker-update.lock" || return 1
  flock -x 9
}

worker_update_sync() {
  command -v sync >/dev/null || {
    echo "sync is required for crash-safe Worker updates" >&2
    return 1
  }
  for SYNC_PATH in "$@"; do
    [ -e "$SYNC_PATH" ] || return 1
    if ! sync -f "$SYNC_PATH" 2>/dev/null; then
      # BusyBox and older coreutils may not support -f. A full sync is slower,
      # but still preserves the ordering contract before the next mutation.
      sync
      return
    fi
  done
}

worker_update_sha256() {
  FILE=$1
  [ -f "$FILE" ] || return 1
  CHECKSUM=$(sha256sum "$FILE" 2>/dev/null | awk '{print $1}') || return 1
  [ "${#CHECKSUM}" -eq 64 ] || return 1
  case "$CHECKSUM" in *[!0-9A-Fa-f]*) return 1 ;; esac
  printf '%s' "$CHECKSUM"
}

worker_update_file_matches() {
  FILE=$1
  EXPECTED_CHECKSUM=$2
  [ -n "$EXPECTED_CHECKSUM" ] && [ -x "$FILE" ] || return 1
  [ "$(worker_update_sha256 "$FILE" 2>/dev/null || true)" = "$EXPECTED_CHECKSUM" ]
}

worker_update_driver_absent() {
  [ ! -e /usr/local/bin/agentbox-microsandbox-driver ] &&
    [ ! -L /usr/local/bin/agentbox-microsandbox-driver ]
}

worker_update_driver_matches_legacy() {
  DRIVER_UPDATED=$1
  EXPECTED_CHECKSUM=$2
  [ "$DRIVER_UPDATED" = true ] || return 0
  [ -n "$EXPECTED_CHECKSUM" ] && [ -x /usr/local/bin/agentbox-microsandbox-driver ] || return 1
  CURRENT_CHECKSUM=$(worker_update_legacy_checksum /usr/local/bin/agentbox-microsandbox-driver)
  [ -n "$CURRENT_CHECKSUM" ] && [ "$CURRENT_CHECKSUM" = "$EXPECTED_CHECKSUM" ]
}

worker_update_legacy_checksum() {
  [ -f "$1" ] || return 1
  VALUE=$(cksum "$1" 2>/dev/null | awk '{print $1 ":" $2}') || return 1
  [ -n "$VALUE" ] || return 1
  printf '%s' "$VALUE"
}

worker_update_driver_previous_matches_legacy() (
  MARKER=$1
  DRIVER_UPDATED=$(jq -r '.driverUpdated // false' "$MARKER")
  [ "$DRIVER_UPDATED" = true ] || exit 0
  DRIVER_HAD_PREVIOUS=$(jq -r '.driverHadPrevious // false' "$MARKER")
  if [ "$DRIVER_HAD_PREVIOUS" != true ]; then
    worker_update_driver_absent
    exit
  fi
  DRIVER_PREVIOUS=$(jq -r '.driverPrevious // "/usr/local/lib/agentbox/agentbox-microsandbox-driver.previous"' "$MARKER")
  [ -x /usr/local/bin/agentbox-microsandbox-driver ] && [ -x "$DRIVER_PREVIOUS" ] || exit 1
  CURRENT_CHECKSUM=$(worker_update_legacy_checksum /usr/local/bin/agentbox-microsandbox-driver)
  PREVIOUS_CHECKSUM=$(worker_update_legacy_checksum "$DRIVER_PREVIOUS")
  [ "$CURRENT_CHECKSUM" = "$PREVIOUS_CHECKSUM" ]
)

worker_update_pair_state() (
  MARKER=$1
  FORMAT_VERSION=$(jq -r '.formatVersion // 1' "$MARKER")
  TARGET_VERSION=$(jq -r '.targetVersion // empty' "$MARKER")
  DRIVER_UPDATED=$(jq -r '.driverUpdated // false' "$MARKER")
  if [ "$FORMAT_VERSION" != 2 ]; then
    CURRENT_VERSION=$(/usr/local/bin/agentbox-worker version 2>/dev/null || printf unknown)
    PREVIOUS_VERSION=$(jq -r '.workerPreviousVersion // empty' "$MARKER")
    DRIVER_CHECKSUM=$(jq -r '.driverChecksum // empty' "$MARKER")
    WORKER_NEW=false
    WORKER_OLD=false
    [ "$CURRENT_VERSION" != "$TARGET_VERSION" ] || WORKER_NEW=true
    if [ -n "$PREVIOUS_VERSION" ]; then
      [ "$CURRENT_VERSION" != "$PREVIOUS_VERSION" ] || WORKER_OLD=true
    elif [ "$CURRENT_VERSION" != unknown ] && [ "$CURRENT_VERSION" != "$TARGET_VERSION" ]; then
      WORKER_OLD=true
    fi
    DRIVER_NEW=false
    DRIVER_OLD=false
    worker_update_driver_matches_legacy "$DRIVER_UPDATED" "$DRIVER_CHECKSUM" && DRIVER_NEW=true
    worker_update_driver_previous_matches_legacy "$MARKER" && DRIVER_OLD=true
    if [ "$WORKER_NEW" = true ] && [ "$DRIVER_NEW" = true ]; then
      printf new
    elif [ "$WORKER_OLD" = true ] && [ "$DRIVER_OLD" = true ]; then
      printf old
    else
      printf mixed
    fi
    exit 0
  fi

  WORKER_CHECKSUM=$(jq -r '.workerChecksum // empty' "$MARKER")
  WORKER_PREVIOUS_CHECKSUM=$(jq -r '.workerPreviousChecksum // empty' "$MARKER")
  if worker_update_file_matches /usr/local/bin/agentbox-worker "$WORKER_CHECKSUM"; then
    WORKER_STATE=new
  elif worker_update_file_matches /usr/local/bin/agentbox-worker "$WORKER_PREVIOUS_CHECKSUM"; then
    WORKER_STATE=old
  else
    WORKER_STATE=unknown
  fi

  if [ "$DRIVER_UPDATED" != true ]; then
    DRIVER_NEW=true
    DRIVER_OLD=true
  else
    DRIVER_CHECKSUM=$(jq -r '.driverChecksum // empty' "$MARKER")
    DRIVER_HAD_PREVIOUS=$(jq -r '.driverHadPrevious // false' "$MARKER")
    DRIVER_PREVIOUS_CHECKSUM=$(jq -r '.driverPreviousChecksum // empty' "$MARKER")
    DRIVER_NEW=false
    DRIVER_OLD=false
    if worker_update_file_matches /usr/local/bin/agentbox-microsandbox-driver "$DRIVER_CHECKSUM"; then
      DRIVER_NEW=true
    fi
    if [ "$DRIVER_HAD_PREVIOUS" = true ]; then
      if worker_update_file_matches /usr/local/bin/agentbox-microsandbox-driver "$DRIVER_PREVIOUS_CHECKSUM"; then
        DRIVER_OLD=true
      fi
    elif worker_update_driver_absent; then
      DRIVER_OLD=true
    fi
  fi

  if [ "$WORKER_STATE" = new ] && [ "$DRIVER_NEW" = true ]; then
    printf new
  elif [ "$WORKER_STATE" = old ] && [ "$DRIVER_OLD" = true ]; then
    printf old
  else
    printf mixed
  fi
)

worker_update_fallback_valid() (
  MARKER=$1
  FORMAT_VERSION=$(jq -r '.formatVersion // 1' "$MARKER")
  WORKER_PREVIOUS=$(jq -r '.workerPrevious // "/usr/local/lib/agentbox/agentbox-worker.previous"' "$MARKER")
  if [ "$FORMAT_VERSION" != 2 ]; then
    [ -x "$WORKER_PREVIOUS" ] || exit 1
    FALLBACK_VERSION=$("$WORKER_PREVIOUS" version 2>/dev/null || true)
    PREVIOUS_VERSION=$(jq -r '.workerPreviousVersion // empty' "$MARKER")
    TARGET_VERSION=$(jq -r '.targetVersion // empty' "$MARKER")
    [ -n "$FALLBACK_VERSION" ] || exit 1
    if [ -n "$PREVIOUS_VERSION" ]; then
      [ "$FALLBACK_VERSION" = "$PREVIOUS_VERSION" ] || exit 1
    else
      [ "$FALLBACK_VERSION" != "$TARGET_VERSION" ] || exit 1
    fi
    DRIVER_UPDATED=$(jq -r '.driverUpdated // false' "$MARKER")
    DRIVER_HAD_PREVIOUS=$(jq -r '.driverHadPrevious // false' "$MARKER")
    if [ "$DRIVER_UPDATED" = true ] && [ "$DRIVER_HAD_PREVIOUS" = true ]; then
      DRIVER_PREVIOUS=$(jq -r '.driverPrevious // "/usr/local/lib/agentbox/agentbox-microsandbox-driver.previous"' "$MARKER")
      [ -x "$DRIVER_PREVIOUS" ] && worker_update_legacy_checksum "$DRIVER_PREVIOUS" >/dev/null
    fi
    exit
  fi
  WORKER_PREVIOUS_CHECKSUM=$(jq -r '.workerPreviousChecksum // empty' "$MARKER")
  worker_update_file_matches "$WORKER_PREVIOUS" "$WORKER_PREVIOUS_CHECKSUM" || exit 1
  DRIVER_UPDATED=$(jq -r '.driverUpdated // false' "$MARKER")
  DRIVER_HAD_PREVIOUS=$(jq -r '.driverHadPrevious // false' "$MARKER")
  if [ "$DRIVER_UPDATED" = true ] && [ "$DRIVER_HAD_PREVIOUS" = true ]; then
    DRIVER_PREVIOUS=$(jq -r '.driverPrevious // empty' "$MARKER")
    DRIVER_PREVIOUS_CHECKSUM=$(jq -r '.driverPreviousChecksum // empty' "$MARKER")
    worker_update_file_matches "$DRIVER_PREVIOUS" "$DRIVER_PREVIOUS_CHECKSUM"
  fi
)

update_worker_marker_phase() {
  MARKER=$1
  PHASE=$2
  PHASE_TMP="$MARKER.phase"
  jq --arg phase "$PHASE" '.phase = $phase' "$MARKER" > "$PHASE_TMP" &&
    mv -f "$PHASE_TMP" "$MARKER" &&
    worker_update_sync "$(dirname "$MARKER")"
}

cleanup_worker_update_guardian() {
  JOB_ID=$1
  GUARDIAN_TIMER=$2
  GUARDIAN_SERVICE=$3
  if [ -n "$GUARDIAN_TIMER" ]; then
    [ "$GUARDIAN_TIMER" = agentbox-worker-update-rollback.timer ] &&
      [ "$GUARDIAN_SERVICE" = agentbox-worker-update-rollback.service ] || return 1
    systemctl disable --now "$GUARDIAN_TIMER" >/dev/null 2>&1 || true
    # Do not synchronously stop the guardian while holding its shared lock. If
    # it was already triggered, it will acquire the lock after this caller
    # removes or updates the marker and then exit without touching the pair.
    rm -f /etc/systemd/system/agentbox-worker-update-rollback.timer \
      /etc/systemd/system/agentbox-worker-update-rollback.service \
      /etc/systemd/system/agentbox-worker.service.d/agentbox-restart.conf \
      "$STATE_DIR/worker-update-start-guard.sh"
    systemctl daemon-reload >/dev/null 2>&1 || true
    return 0
  fi
  systemctl stop "agentbox-worker-update-$JOB_ID.timer" \
    "agentbox-worker-update-$JOB_ID.service" >/dev/null 2>&1 || true
  return 0
}

schedule_worker_restart() {
  JOB_ID=$1
  if systemd-run --quiet --unit="agentbox-worker-restart-$JOB_ID" --on-active=1s \
      /bin/systemctl restart agentbox-worker.service; then
    return 0
  fi
  systemctl start "agentbox-worker-restart-$JOB_ID.service" >/dev/null 2>&1
}

finalize_worker_update() (
  MARKER="$STATE_DIR/worker-update.json"
  [ -e "$MARKER" ] || exit 0
  worker_update_lock || exit 1
  [ -s "$MARKER" ] || exit 1
  worker_update_marker_valid "$MARKER" || exit 1
  JOB_ID=$(jq -r '.jobId // empty' "$MARKER")
  LEASE_GENERATION=$(jq -r '.leaseGeneration // 0' "$MARKER")
  TARGET_VERSION=$(jq -r '.targetVersion // empty' "$MARKER")
  PREVIOUS_VERSION=$(jq -r '.workerPreviousVersion // empty' "$MARKER")
  ROLLBACK_SCRIPT=$(jq -r '.rollbackScript // empty' "$MARKER")
  GUARDIAN_TIMER=$(jq -r '.guardianTimer // empty' "$MARKER")
  GUARDIAN_SERVICE=$(jq -r '.guardianService // empty' "$MARKER")
  DRIVER_UPDATED=$(jq -r '.driverUpdated // false' "$MARKER")
  [ -n "$JOB_ID" ] && [ -n "$TARGET_VERSION" ] || exit 1
  PAIR_STATE=$(worker_update_pair_state "$MARKER") || exit 1
  CONFIRMED="$STATE_DIR/worker-update-confirmed"
  CONFIRMED_TMP="$STATE_DIR/.worker-update-confirmed"

  case "$PAIR_STATE" in
    new)
      # Do not accept the on-disk pair from the old in-memory process before
      # systemd has actually started the downloaded Worker.
      [ "${AGENTBOX_WORKER_VERSION:-unknown}" = "$TARGET_VERSION" ] || exit 0
      printf '%s\n' "$JOB_ID" > "$CONFIRMED_TMP" || exit 1
      if ! complete_job "$JOB_ID" true "" "Worker 已更新到 $TARGET_VERSION"; then
        rm -f "$CONFIRMED_TMP"
        exit 1
      fi
      mv -f "$CONFIRMED_TMP" "$CONFIRMED" || exit 1
      # The missing journal is the guardian's idempotent stop condition. Remove
      # it while holding the shared lock before disabling the timer, so a crash
      # cannot leave a pending journal without a recovery owner.
      rm -f "$MARKER" || exit 1
      worker_update_sync "$STATE_DIR" || exit 1
      cleanup_worker_update_guardian "$JOB_ID" "$GUARDIAN_TIMER" "$GUARDIAN_SERVICE" || true
      rm -f "$CONFIRMED" "$ROLLBACK_SCRIPT"
      # The update is now accepted and no rollback reader can depend on the old
      # backup pair. Refresh the fallback used by the next update only after
      # removing this update's journal.
      COMMIT_OK=true
      install -m 0755 /usr/local/bin/agentbox-worker \
        /usr/local/lib/agentbox/.agentbox-worker.previous &&
        mv -f /usr/local/lib/agentbox/.agentbox-worker.previous \
          /usr/local/lib/agentbox/agentbox-worker.previous || COMMIT_OK=false
      if [ "$DRIVER_UPDATED" = true ]; then
        install -m 0755 /usr/local/bin/agentbox-microsandbox-driver \
          /usr/local/lib/agentbox/.agentbox-microsandbox-driver.previous &&
          mv -f /usr/local/lib/agentbox/.agentbox-microsandbox-driver.previous \
            /usr/local/lib/agentbox/agentbox-microsandbox-driver.previous || COMMIT_OK=false
      fi
      if [ "$COMMIT_OK" != true ]; then
        echo "warning: Worker update was accepted but its fallback pair could not be committed" >&2
      fi
      ;;
    old)
      CURRENT_VERSION=$(/usr/local/bin/agentbox-worker version 2>/dev/null || printf unknown)
      [ -n "$PREVIOUS_VERSION" ] || PREVIOUS_VERSION=$CURRENT_VERSION
      if complete_job_failure "$JOB_ID" worker_update_rolled_back worker-update true \
          "Worker 更新已回滚，当前版本 $CURRENT_VERSION" update-worker "$CURRENT_VERSION"; then
        rm -f "$MARKER" || exit 1
        worker_update_sync "$STATE_DIR" || exit 1
        cleanup_worker_update_guardian "$JOB_ID" "$GUARDIAN_TIMER" "$GUARDIAN_SERVICE" || true
        rm -f "$CONFIRMED" "$CONFIRMED_TMP" "$ROLLBACK_SCRIPT"
        # Exit the new in-memory process only after the rollback result is
        # durable. Restart=always then execs the restored previous binary.
        [ "${AGENTBOX_WORKER_VERSION:-unknown}" = "$PREVIOUS_VERSION" ] || exit 75
      fi
      ;;
    mixed)
      worker_update_fallback_valid "$MARKER" || exit 1
      WORKER_PREVIOUS=$(jq -r '.workerPrevious // "/usr/local/lib/agentbox/agentbox-worker.previous"' "$MARKER")
      DRIVER_PREVIOUS=$(jq -r '.driverPrevious // "/usr/local/lib/agentbox/agentbox-microsandbox-driver.previous"' "$MARKER")
      DRIVER_HAD_PREVIOUS=$(jq -r '.driverHadPrevious // false' "$MARKER")
      restore_worker_update "$WORKER_PREVIOUS" "$DRIVER_PREVIOUS" \
        "$DRIVER_UPDATED" "$DRIVER_HAD_PREVIOUS" || exit 1
      [ "$(worker_update_pair_state "$MARKER")" = old ] || exit 1
      update_worker_marker_phase "$MARKER" rolled-back || exit 1
      ;;
  esac
)

restore_worker_binary() {
  PREVIOUS=$1
  RESTORE_TMP=/usr/local/bin/.agentbox-worker-restore
  install -m 0755 "$PREVIOUS" "$RESTORE_TMP" || return 1
  mv -f "$RESTORE_TMP" /usr/local/bin/agentbox-worker
}

restore_microsandbox_driver() {
  PREVIOUS=$1
  HAD_PREVIOUS=$2
  RESTORE_TMP=/usr/local/bin/.agentbox-microsandbox-driver-restore
  if [ "$HAD_PREVIOUS" = true ]; then
    install -m 0755 "$PREVIOUS" "$RESTORE_TMP" || return 1
    mv -f "$RESTORE_TMP" /usr/local/bin/agentbox-microsandbox-driver
  else
    rm -f "$RESTORE_TMP" /usr/local/bin/agentbox-microsandbox-driver
  fi
}

restore_worker_update() (
  WORKER_PREVIOUS=$1
  DRIVER_PREVIOUS=$2
  DRIVER_UPDATED=$3
  DRIVER_HAD_PREVIOUS=$4
  RESTORE_FAILED=false
  restore_worker_binary "$WORKER_PREVIOUS" || RESTORE_FAILED=true
  if [ "$DRIVER_UPDATED" = true ]; then
    restore_microsandbox_driver "$DRIVER_PREVIOUS" "$DRIVER_HAD_PREVIOUS" || RESTORE_FAILED=true
  fi
  [ "$RESTORE_FAILED" = false ] || exit 1
  worker_update_sync /usr/local/bin
)

abort_worker_update() (
  WORKER_PREVIOUS=$1
  DRIVER_PREVIOUS=$2
  DRIVER_UPDATED=$3
  DRIVER_HAD_PREVIOUS=$4
  JOB_ID=$5
  MARKER=$6
  CONFIRMED=$7
  ROLLBACK_SCRIPT=$8
  WORKER_TMP=$9
  shift 9
  WORKER_NEXT=$1
  DRIVER_NEXT=$2
  MARKER_TMP=$3
  if ! worker_update_fallback_valid "$MARKER"; then
    echo "previous Worker/driver pair failed validation; refusing a partial rollback" >&2
    return 1
  fi
  if ! restore_worker_update "$WORKER_PREVIOUS" "$DRIVER_PREVIOUS" \
      "$DRIVER_UPDATED" "$DRIVER_HAD_PREVIOUS"; then
    echo "failed to restore the previous Worker and Microsandbox driver pair" >&2
    return 1
  fi
  # Remove the marker before cancelling units. Even if systemd cannot stop a
  # not-yet-started check, it will see no job id and leave the restored pair in place.
  rm -f "$MARKER" "$CONFIRMED" "$MARKER_TMP" "$WORKER_TMP" "$WORKER_NEXT" "$DRIVER_NEXT"
  worker_update_sync "$STATE_DIR" || return 1
  cleanup_worker_update_guardian "$JOB_ID" agentbox-worker-update-rollback.timer \
    agentbox-worker-update-rollback.service || true
  rm -f "$ROLLBACK_SCRIPT"
)

recover_worker_update() (
  [ "${1:-}" = --restore-previous ] || usage
  [ "$(id -u)" = 0 ] || { echo "Worker update recovery must run as root" >&2; exit 1; }
  MARKER="$STATE_DIR/worker-update.json"
  [ -e "$MARKER" ] || { echo "No Worker update journal requires recovery."; exit 0; }
  worker_update_lock || exit 1
  [ -e "$MARKER" ] || exit 0
  if worker_update_marker_valid "$MARKER" && worker_update_fallback_valid "$MARKER"; then
    echo "Worker update journal is valid; automatic recovery is still active." >&2
    exit 1
  fi
  PREVIOUS=/usr/local/lib/agentbox/agentbox-worker.previous
  DRIVER_PREVIOUS=/usr/local/lib/agentbox/agentbox-microsandbox-driver.previous
  [ -x "$PREVIOUS" ] || {
    echo "Previous Worker backup is unavailable; reinstall the Worker before discarding the journal." >&2
    exit 1
  }
  PREVIOUS_VERSION=$("$PREVIOUS" version 2>/dev/null || true)
  case "$PREVIOUS_VERSION" in v*) ;; *) echo "Previous Worker backup is invalid." >&2; exit 1 ;; esac
  DRIVER_HAD_PREVIOUS=false
  [ ! -e "$DRIVER_PREVIOUS" ] && [ ! -L "$DRIVER_PREVIOUS" ] || {
    [ -x "$DRIVER_PREVIOUS" ] || {
      echo "Previous Microsandbox driver backup is invalid." >&2
      exit 1
    }
    DRIVER_HAD_PREVIOUS=true
  }
  systemctl stop agentbox-worker.service
  restore_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" true "$DRIVER_HAD_PREVIOUS" || {
    echo "Failed to restore the previous Worker/driver pair; journal was retained." >&2
    exit 1
  }
  QUARANTINE="$STATE_DIR/worker-update.corrupt.$(date +%s).$$.json"
  mv "$MARKER" "$QUARANTINE" || exit 1
  rm -f "$STATE_DIR/worker-update-confirmed" "$STATE_DIR/.worker-update-confirmed" \
    "$STATE_DIR/worker-update.json.phase"
  worker_update_sync "$STATE_DIR" || exit 1
  cleanup_worker_update_guardian "manual" agentbox-worker-update-rollback.timer \
    agentbox-worker-update-rollback.service || true
  flock -u 9
  exec 9>&-
  systemctl restart agentbox-worker.service
  echo "Restored Worker $PREVIOUS_VERSION; corrupt journal quarantined at $QUARANTINE"
)

refresh_microsandbox_driver() (
  DRIVER_NEXT=$1
  rm -f "$DRIVER_NEXT"
  if [ ! -c /dev/kvm ] &&
     [ ! -e /usr/local/bin/agentbox-microsandbox-driver ] &&
     [ ! -L /usr/local/bin/agentbox-microsandbox-driver ]; then
    exit 0
  fi
  command -v go >/dev/null || { echo "Go is required to update the Microsandbox driver" >&2; return 1; }
  command -v gcc >/dev/null || { echo "a C compiler is required to update the Microsandbox driver" >&2; return 1; }
  install -d -m 0700 "$STATE_DIR/go" "$STATE_DIR/go-mod-cache" "$STATE_DIR/go-build-cache"
  BUILD_DIR=$(mktemp -d "$STATE_DIR/microsandbox-driver.XXXXXX") || return 1
  trap 'rm -rf "$BUILD_DIR"' EXIT
  if ! worker_origin_curl_follow -fsS "$SERVER_URL/api/worker/agentbox-microsandbox-driver.go" -o "$BUILD_DIR/main.go"; then
    return 1
  fi
  cat > "$BUILD_DIR/go.mod" <<'EOF'
module agentbox/microsandbox-driver

go 1.22

require github.com/superradcompany/microsandbox/sdk/go v0.6.15
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
    return 1
  fi
  install -m 0755 "$BUILD_DIR/agentbox-microsandbox-driver" "$DRIVER_NEXT" || {
    rm -f "$DRIVER_NEXT"
    return 1
  }
)

update_worker() (
  JOB_FILE=$1
  JOB_ID=$(jq -r '.job.id' "$JOB_FILE")
  LEASE_GENERATION=$(jq -r '.job.leaseGeneration // 0' "$JOB_FILE")
  TARGET_VERSION=$(jq -r '.job.payload.version // empty' "$JOB_FILE")
  EXPECTED_PREVIOUS_VERSION=$(jq -r '.job.payload.previousVersion // empty' "$JOB_FILE")
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
  command -v sha256sum >/dev/null || {
    echo "sha256sum is required for crash-safe Worker updates" >&2
    return 1
  }
  command -v flock >/dev/null || {
    echo "flock is required for crash-safe Worker updates" >&2
    return 1
  }

  report_job_progress worker-update "Preparing Worker update"
  worker_update_lease_keepalive &
  LEASE_KEEPALIVE_PID=$!
  cleanup_worker_update_lease() {
    kill "$LEASE_KEEPALIVE_PID" >/dev/null 2>&1 || true
    wait "$LEASE_KEEPALIVE_PID" >/dev/null 2>&1 || true
  }
  trap cleanup_worker_update_lease EXIT
  trap 'exit 1' INT TERM

  WORKER_TMP=$(mktemp "$STATE_DIR/agentbox-worker.XXXXXX") || return 1
  WORKER_HEADERS=$(mktemp "$STATE_DIR/agentbox-worker-headers.XXXXXX") || { rm -f "$WORKER_TMP"; return 1; }
  if ! worker_origin_curl_follow -fsS -D "$WORKER_HEADERS" "$SERVER_URL/api/worker/agentbox-worker?arch=$ARCH&version=$TARGET_VERSION" -o "$WORKER_TMP"; then
    rm -f "$WORKER_TMP" "$WORKER_HEADERS"
    return 1
  fi
  # Verify integrity before executing anything from the downloaded binary.
  EXPECTED_SHA256=$(tr -d '\r' < "$WORKER_HEADERS" |
    sed -n 's/^[Xx]-[Cc]hecksum-[Ss]ha256:[[:space:]]*\([0-9A-Fa-f]\{1,\}\).*/\1/p' | head -n 1)
  rm -f "$WORKER_HEADERS"
  ACTUAL_SHA256=$(worker_update_sha256 "$WORKER_TMP") || {
    echo "failed to checksum the downloaded Worker" >&2
    rm -f "$WORKER_TMP"
    return 1
  }
  if [ -z "$EXPECTED_SHA256" ]; then
    echo "the server did not provide a Worker checksum; refusing to update" >&2
    rm -f "$WORKER_TMP"
    return 1
  fi
  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    echo "downloaded Worker checksum mismatch (expected $EXPECTED_SHA256, got $ACTUAL_SHA256)" >&2
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
  DRIVER_NEXT=/usr/local/bin/.agentbox-microsandbox-driver-next
  refresh_microsandbox_driver "$DRIVER_NEXT" || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    echo "failed to refresh the Microsandbox driver" >&2
    return 1
  }

  DRIVER_UPDATED=false
  DRIVER_HAD_PREVIOUS=false
  DRIVER_CHECKSUM=
  if [ -s "$DRIVER_NEXT" ]; then
    DRIVER_UPDATED=true
    DRIVER_CHECKSUM=$(worker_update_sha256 "$DRIVER_NEXT") || {
      rm -f "$WORKER_TMP" "$DRIVER_NEXT"
      echo "failed to checksum the staged Microsandbox driver" >&2
      return 1
    }
  fi

  worker_update_lock || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    return 1
  }
  MARKER="$STATE_DIR/worker-update.json"
  if [ -e "$MARKER" ]; then
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    echo "a previous Worker update still requires recovery" >&2
    return 1
  fi
  systemctl disable --now agentbox-worker-update-rollback.timer >/dev/null 2>&1 || true

  install -d -m 0755 /usr/local/lib/agentbox || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    return 1
  }
  PREVIOUS=/usr/local/lib/agentbox/agentbox-worker.previous
  PREVIOUS_TMP=/usr/local/lib/agentbox/.agentbox-worker.previous
  DRIVER_PREVIOUS=/usr/local/lib/agentbox/agentbox-microsandbox-driver.previous
  DRIVER_PREVIOUS_TMP=/usr/local/lib/agentbox/.agentbox-microsandbox-driver.previous
  NEXT=/usr/local/bin/.agentbox-worker-next
  PREVIOUS_VERSION=$(/usr/local/bin/agentbox-worker version 2>/dev/null || true)
  [ -n "$PREVIOUS_VERSION" ] || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    echo "current Worker version is unavailable" >&2
    return 1
  }
  if [ -z "$EXPECTED_PREVIOUS_VERSION" ] || [ "$PREVIOUS_VERSION" != "$EXPECTED_PREVIOUS_VERSION" ]; then
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    echo "current Worker version $PREVIOUS_VERSION does not match queued previous version $EXPECTED_PREVIOUS_VERSION" >&2
    return 1
  fi
  install -m 0755 /usr/local/bin/agentbox-worker "$PREVIOUS_TMP" || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT" "$PREVIOUS_TMP"
    return 1
  }
  mv -f "$PREVIOUS_TMP" "$PREVIOUS" || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT" "$PREVIOUS_TMP"
    return 1
  }
  WORKER_PREVIOUS_CHECKSUM=$(worker_update_sha256 "$PREVIOUS") || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    echo "failed to checksum the previous Worker" >&2
    return 1
  }
  DRIVER_PREVIOUS_CHECKSUM=
  # Always snapshot the current driver state, even when this release does not
  # rebuild it. That keeps manual recovery unambiguous if the JSON journal is
  # later damaged: a fixed backup exists exactly when a driver existed before
  # this generation.
  if [ -x /usr/local/bin/agentbox-microsandbox-driver ]; then
    DRIVER_HAD_PREVIOUS=true
    install -m 0755 /usr/local/bin/agentbox-microsandbox-driver "$DRIVER_PREVIOUS_TMP" || {
      rm -f "$WORKER_TMP" "$DRIVER_NEXT" "$DRIVER_PREVIOUS_TMP"
      return 1
    }
    mv -f "$DRIVER_PREVIOUS_TMP" "$DRIVER_PREVIOUS" || {
      rm -f "$WORKER_TMP" "$DRIVER_NEXT" "$DRIVER_PREVIOUS_TMP"
      return 1
    }
    DRIVER_PREVIOUS_CHECKSUM=$(worker_update_sha256 "$DRIVER_PREVIOUS") || {
      rm -f "$WORKER_TMP" "$DRIVER_NEXT"
      echo "failed to checksum the previous Microsandbox driver" >&2
      return 1
    }
  else
    rm -f "$DRIVER_PREVIOUS_TMP" "$DRIVER_PREVIOUS" || {
      rm -f "$WORKER_TMP" "$DRIVER_NEXT"
      echo "failed to record that the previous Microsandbox driver was absent" >&2
      return 1
    }
  fi
  worker_update_sync /usr/local/lib/agentbox || {
    rm -f "$WORKER_TMP" "$DRIVER_NEXT"
    echo "failed to persist the previous Worker/driver pair" >&2
    return 1
  }
  install -m 0755 "$WORKER_TMP" "$NEXT" || {
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT"
    return 1
  }

  CONFIRMED="$STATE_DIR/worker-update-confirmed"
  CONFIRMED_TMP="$STATE_DIR/.worker-update-confirmed"
  rm -f "$CONFIRMED" "$CONFIRMED_TMP"
  START_GUARD="$STATE_DIR/worker-update-start-guard.sh"
  START_GUARD_TMP="$STATE_DIR/.worker-update-start-guard.sh"
  cat > "$START_GUARD_TMP" <<'START_GUARD'
#!/bin/sh
set -eu
MARKER=/var/lib/agentbox-worker/worker-update.json
[ -e "$MARKER" ] || exit 0
[ -s "$MARKER" ] || exit 1
FORMAT_VERSION=$(jq -r 'if type == "object" and has("formatVersion") then .formatVersion else 1 end' "$MARKER" 2>/dev/null) || exit 1
[ "$FORMAT_VERSION" = 1 ] && exit 0
[ "$FORMAT_VERSION" = 2 ] || exit 1
EXPECTED_CHECKSUM=$(jq -r '.workerChecksum // empty' "$MARKER" 2>/dev/null) || exit 1
CURRENT_CHECKSUM=$(sha256sum /usr/local/bin/agentbox-worker 2>/dev/null | awk '{print $1}') || exit 1
if [ -z "$EXPECTED_CHECKSUM" ] || [ "$CURRENT_CHECKSUM" != "$EXPECTED_CHECKSUM" ]; then
  echo "Worker rollback is awaiting Server reconciliation; refusing to start the previous job loop" >&2
  exit 1
fi
START_GUARD
  chmod 0700 "$START_GUARD_TMP" && mv -f "$START_GUARD_TMP" "$START_GUARD" || {
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$START_GUARD_TMP"
    return 1
  }
  ROLLBACK_SCRIPT="$STATE_DIR/worker-update-rollback.sh"
  MARKER_TMP="$STATE_DIR/.worker-update.json"
  cat > "$ROLLBACK_SCRIPT" <<'ROLLBACK'
#!/bin/sh
set -eu
STATE_DIR=/var/lib/agentbox-worker
MARKER=${1:-$STATE_DIR/worker-update.json}
LOCK=$STATE_DIR/worker-update.lock
TIMER=agentbox-worker-update-rollback.timer

cleanup_guardian() {
  systemctl disable --now "$TIMER" >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/agentbox-worker-update-rollback.timer \
    /etc/systemd/system/agentbox-worker-update-rollback.service \
    /etc/systemd/system/agentbox-worker.service.d/agentbox-restart.conf \
    "$STATE_DIR/worker-update-start-guard.sh" "$STATE_DIR/worker-update-rollback.sh"
  systemctl daemon-reload >/dev/null 2>&1 || true
}

command -v flock >/dev/null || exit 1
command -v sha256sum >/dev/null || exit 1
command -v sync >/dev/null || exit 1
exec 9>"$LOCK"
flock -x 9
[ -e "$MARKER" ] || {
  # The timer is armed before the journal is published. If the updater exits
  # in that short pre-publication window there is no state to recover. The
  # shared lock proves the updater no longer owns this generation, so remove
  # the otherwise persistent timer and start guard.
  cleanup_guardian
  exit 0
}
[ -s "$MARKER" ] || exit 1
JOB_ID=$(jq -r '.jobId // empty' "$MARKER" 2>/dev/null || true)
[ -n "$JOB_ID" ] || exit 1
FORMAT_VERSION=$(jq -r '.formatVersion // 1' "$MARKER" 2>/dev/null || true)
[ "$FORMAT_VERSION" = 2 ] || exit 1
LEASE_GENERATION=$(jq -r '.leaseGeneration // 0' "$MARKER" 2>/dev/null || true)
TARGET_VERSION=$(jq -r '.targetVersion // empty' "$MARKER" 2>/dev/null || true)
PREVIOUS_VERSION=$(jq -r '.workerPreviousVersion // empty' "$MARKER" 2>/dev/null || true)
WORKER_PREVIOUS=$(jq -r '.workerPrevious // empty' "$MARKER" 2>/dev/null || true)
WORKER_CHECKSUM=$(jq -r '.workerChecksum // empty' "$MARKER" 2>/dev/null || true)
WORKER_PREVIOUS_CHECKSUM=$(jq -r '.workerPreviousChecksum // empty' "$MARKER" 2>/dev/null || true)
DRIVER_UPDATED=$(jq -r '.driverUpdated // false' "$MARKER" 2>/dev/null || true)
DRIVER_HAD_PREVIOUS=$(jq -r '.driverHadPrevious // false' "$MARKER" 2>/dev/null || true)
DRIVER_PREVIOUS=$(jq -r '.driverPrevious // empty' "$MARKER" 2>/dev/null || true)
DRIVER_CHECKSUM=$(jq -r '.driverChecksum // empty' "$MARKER" 2>/dev/null || true)
DRIVER_PREVIOUS_CHECKSUM=$(jq -r '.driverPreviousChecksum // empty' "$MARKER" 2>/dev/null || true)

file_checksum() {
  VALUE=$(sha256sum "$1" 2>/dev/null | awk '{print $1}') || return 1
  [ "${#VALUE}" -eq 64 ] || return 1
  case "$VALUE" in *[!0-9A-Fa-f]*) return 1 ;; esac
  printf '%s' "$VALUE"
}

file_matches() {
  [ -n "$2" ] && [ -x "$1" ] || return 1
  [ "$(file_checksum "$1" 2>/dev/null || true)" = "$2" ]
}

sync_path() {
  sync -f "$1" 2>/dev/null || sync
}

driver_absent() {
  [ ! -e /usr/local/bin/agentbox-microsandbox-driver ] &&
    [ ! -L /usr/local/bin/agentbox-microsandbox-driver ]
}

report_rollback() {
  CONFIG=/etc/agentbox-worker.conf
  [ -r "$CONFIG" ] && [ -n "$PREVIOUS_VERSION" ] || return 1
  SERVER_URL=$(sed -n '1p' "$CONFIG")
  SERVER_ID=$(sed -n '2p' "$CONFIG")
  CREDENTIAL=$(sed -n '3p' "$CONFIG")
  [ -n "$SERVER_URL" ] && [ -n "$SERVER_ID" ] && [ -n "$CREDENTIAL" ] || return 1
  MESSAGE="Worker 更新已回滚，当前版本 $PREVIOUS_VERSION"
  BODY=$(jq -n --argjson leaseGeneration "$LEASE_GENERATION" \
    --arg message "$MESSAGE" --arg workerVersion "$PREVIOUS_VERSION" \
    '{leaseGeneration:$leaseGeneration,success:false,externalId:"",message:$message,
      exitCode:null,output:"",outputTruncated:false,timedOut:false,agentTools:[],
      error:{code:"worker_update_rolled_back",stage:"worker-update",retryable:true,
        details:{action:"update-worker",workerVersion:$workerVersion}}}') || return 1
  STATUS=$(curl -sS --connect-timeout 10 --max-time 30 -o /dev/null -w '%{http_code}' \
    -X POST "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/complete" \
    -H "Authorization: Bearer $CREDENTIAL" \
    -H 'X-AgentBox-Worker-Protocol-Min: 1' \
    -H 'X-AgentBox-Worker-Protocol-Max: 1' \
    -H 'Content-Type: application/json' --data "$BODY") || return 1
  [ "$STATUS" = 204 ]
}

NEW_PAIR=true
file_matches /usr/local/bin/agentbox-worker "$WORKER_CHECKSUM" || NEW_PAIR=false
if [ "$DRIVER_UPDATED" = true ]; then
  file_matches /usr/local/bin/agentbox-microsandbox-driver "$DRIVER_CHECKSUM" || NEW_PAIR=false
fi
if [ -s "$STATE_DIR/worker-update-confirmed" ] &&
   [ "$(cat "$STATE_DIR/worker-update-confirmed")" = "$JOB_ID" ] &&
   systemctl is-active --quiet agentbox-worker.service &&
   [ "$NEW_PAIR" = true ] &&
   [ "$(/usr/local/bin/agentbox-worker version 2>/dev/null || true)" = "$TARGET_VERSION" ]; then
  rm -f "$MARKER" "$STATE_DIR/worker-update-confirmed"
  sync_path "$STATE_DIR"
  cleanup_guardian
  exit 0
fi

# Validate the entire fallback before changing either active path. A failed
# validation leaves the durable marker and guardian in place for inspection.
file_matches "$WORKER_PREVIOUS" "$WORKER_PREVIOUS_CHECKSUM"
if [ "$DRIVER_UPDATED" = true ] && [ "$DRIVER_HAD_PREVIOUS" = true ]; then
  file_matches "$DRIVER_PREVIOUS" "$DRIVER_PREVIOUS_CHECKSUM"
fi

# Stop the whole service cgroup before changing either active executable. This
# also fences the independently supervised interactive session daemon.
systemctl stop agentbox-worker.service

RESTORE_FAILED=false
WORKER_RESTORE_TMP=/usr/local/bin/.agentbox-worker-restore
if install -m 0755 "$WORKER_PREVIOUS" "$WORKER_RESTORE_TMP"; then
  mv -f "$WORKER_RESTORE_TMP" /usr/local/bin/agentbox-worker || RESTORE_FAILED=true
else
  RESTORE_FAILED=true
fi
if [ "$DRIVER_UPDATED" = true ]; then
  DRIVER_RESTORE_TMP=/usr/local/bin/.agentbox-microsandbox-driver-restore
  if [ "$DRIVER_HAD_PREVIOUS" = true ]; then
    if install -m 0755 "$DRIVER_PREVIOUS" "$DRIVER_RESTORE_TMP"; then
      mv -f "$DRIVER_RESTORE_TMP" /usr/local/bin/agentbox-microsandbox-driver || RESTORE_FAILED=true
    else
      RESTORE_FAILED=true
    fi
  else
    rm -f "$DRIVER_RESTORE_TMP" /usr/local/bin/agentbox-microsandbox-driver || RESTORE_FAILED=true
  fi
fi
[ "$RESTORE_FAILED" = false ] || exit 1
file_matches /usr/local/bin/agentbox-worker "$WORKER_PREVIOUS_CHECKSUM"
if [ "$DRIVER_UPDATED" = true ]; then
  if [ "$DRIVER_HAD_PREVIOUS" = true ]; then
    file_matches /usr/local/bin/agentbox-microsandbox-driver "$DRIVER_PREVIOUS_CHECKSUM"
  else
    driver_absent
  fi
fi
sync_path /usr/local/bin
PHASE_TMP="$MARKER.phase"
jq '.phase = "rolled-back"' "$MARKER" > "$PHASE_TMP"
mv -f "$PHASE_TMP" "$MARKER"
sync_path "$STATE_DIR"
# Do not let a pre-protocol previous Worker observe the still-pending journal.
# The ExecStartPre guard also fences an unexpected service restart in this
# window. Only clear the journal after the Server accepts the bound rollback.
report_rollback
rm -f "$MARKER" "$STATE_DIR/worker-update-confirmed"
sync_path "$STATE_DIR"
flock -u 9
exec 9>&-
systemctl start agentbox-worker.service || exit 1
cleanup_guardian
ROLLBACK
  chmod 0700 "$ROLLBACK_SCRIPT" || {
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$ROLLBACK_SCRIPT"
    return 1
  }
  GUARDIAN_SERVICE=agentbox-worker-update-rollback.service
  GUARDIAN_TIMER=agentbox-worker-update-rollback.timer
  jq -n --arg jobId "$JOB_ID" --argjson leaseGeneration "$LEASE_GENERATION" \
    --arg targetVersion "$TARGET_VERSION" --arg previousVersion "$PREVIOUS_VERSION" \
    --arg rollbackScript "$ROLLBACK_SCRIPT" --arg workerPrevious "$PREVIOUS" \
    --arg workerChecksum "$ACTUAL_SHA256" --arg workerPreviousChecksum "$WORKER_PREVIOUS_CHECKSUM" \
    --argjson driverUpdated "$DRIVER_UPDATED" \
    --argjson driverHadPrevious "$DRIVER_HAD_PREVIOUS" --arg driverPrevious "$DRIVER_PREVIOUS" \
    --arg driverChecksum "$DRIVER_CHECKSUM" --arg driverPreviousChecksum "$DRIVER_PREVIOUS_CHECKSUM" \
    --arg guardianService "$GUARDIAN_SERVICE" --arg guardianTimer "$GUARDIAN_TIMER" \
    '{formatVersion:2,phase:"prepared",jobId:$jobId,leaseGeneration:$leaseGeneration,
      targetVersion:$targetVersion,workerPreviousVersion:$previousVersion,
      rollbackScript:$rollbackScript,workerPrevious:$workerPrevious,
      workerChecksum:$workerChecksum,workerPreviousChecksum:$workerPreviousChecksum,
      driverUpdated:$driverUpdated,driverHadPrevious:$driverHadPrevious,
      driverPrevious:$driverPrevious,driverChecksum:$driverChecksum,
      driverPreviousChecksum:$driverPreviousChecksum,
      guardianService:$guardianService,guardianTimer:$guardianTimer}' > "$MARKER_TMP" || {
      rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT"
      return 1
    }
  install -d -m 0755 /etc/systemd/system/agentbox-worker.service.d || {
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT"
    return 1
  }
  if ! cat > /etc/systemd/system/agentbox-worker.service.d/agentbox-restart.conf <<'UNIT'
[Service]
ExecStartPre=/var/lib/agentbox-worker/worker-update-start-guard.sh
RestartSec=2
KillMode=control-group
TimeoutStopSec=20s
UNIT
  then
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT"
    return 1
  fi
  GUARDIAN_SERVICE_FILE=/etc/systemd/system/agentbox-worker-update-rollback.service
  GUARDIAN_TIMER_FILE=/etc/systemd/system/agentbox-worker-update-rollback.timer
  GUARDIAN_SERVICE_TMP=$GUARDIAN_SERVICE_FILE.tmp
  GUARDIAN_TIMER_TMP=$GUARDIAN_TIMER_FILE.tmp
  if ! cat > "$GUARDIAN_SERVICE_TMP" <<'UNIT'
[Unit]
Description=Rollback an unconfirmed AgentBox Worker update
After=local-fs.target

[Service]
Type=oneshot
ExecStart=/var/lib/agentbox-worker/worker-update-rollback.sh /var/lib/agentbox-worker/worker-update.json
Restart=on-failure
RestartSec=10s
UNIT
  then
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT" \
      "$GUARDIAN_SERVICE_TMP" "$GUARDIAN_TIMER_TMP"
    return 1
  fi
  if ! cat > "$GUARDIAN_TIMER_TMP" <<'UNIT'
[Unit]
Description=Guard an AgentBox Worker update across restarts

[Timer]
OnActiveSec=120s
AccuracySec=1s
Unit=agentbox-worker-update-rollback.service

[Install]
WantedBy=timers.target
UNIT
  then
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT" \
      "$GUARDIAN_SERVICE_TMP" "$GUARDIAN_TIMER_TMP"
    return 1
  fi
  if ! mv -f "$GUARDIAN_SERVICE_TMP" "$GUARDIAN_SERVICE_FILE" ||
     ! mv -f "$GUARDIAN_TIMER_TMP" "$GUARDIAN_TIMER_FILE"; then
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT" \
      "$GUARDIAN_SERVICE_TMP" "$GUARDIAN_TIMER_TMP" \
      "$GUARDIAN_SERVICE_FILE" "$GUARDIAN_TIMER_FILE"
    return 1
  fi
  if ! worker_update_sync "$STATE_DIR" /etc/systemd/system \
      /etc/systemd/system/agentbox-worker.service.d; then
    cleanup_worker_update_guardian "$JOB_ID" "$GUARDIAN_TIMER" "$GUARDIAN_SERVICE" || true
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT"
    echo "failed to persist Worker update recovery assets" >&2
    return 1
  fi
  if ! systemctl daemon-reload; then
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT" \
      "$GUARDIAN_SERVICE_FILE" "$GUARDIAN_TIMER_FILE"
    return 1
  fi
  if ! systemctl enable --now "$GUARDIAN_TIMER" >/dev/null 2>&1; then
    cleanup_worker_update_guardian "$JOB_ID" "$GUARDIAN_TIMER" "$GUARDIAN_SERVICE" || true
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT"
    echo "failed to install the persistent Worker rollback guardian" >&2
    return 1
  fi
  if ! worker_update_sync /etc/systemd/system; then
    cleanup_worker_update_guardian "$JOB_ID" "$GUARDIAN_TIMER" "$GUARDIAN_SERVICE" || true
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT"
    echo "failed to persist the enabled Worker rollback guardian" >&2
    return 1
  fi
  # Publish the recovery journal only after the guardian timer is both enabled
  # across boots and active in this boot. The restart below merely resets its
  # deadline; it is no longer the first point at which rollback is protected.
  if ! mv -f "$MARKER_TMP" "$MARKER"; then
    cleanup_worker_update_guardian "$JOB_ID" "$GUARDIAN_TIMER" "$GUARDIAN_SERVICE" || true
    rm -f "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" "$ROLLBACK_SCRIPT"
    return 1
  fi
  if ! worker_update_sync "$STATE_DIR"; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to persist the Worker update journal" >&2
    return 1
  fi
  if ! systemctl restart "$GUARDIAN_TIMER"; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to arm the persistent Worker rollback guardian" >&2
    return 1
  fi
  # The session daemon can start the Microsandbox driver independently of the
  # job loop. Stop it before either active path changes so no request can see a
  # mixed Worker/driver generation.
  if ! pause_session_worker; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to stop the interactive session daemon before Worker activation" >&2
    return 1
  fi
  if ! mv -f "$NEXT" /usr/local/bin/agentbox-worker; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to activate the new Worker" >&2
    return 1
  fi
  if ! update_worker_marker_phase "$MARKER" worker-active; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to journal Worker activation" >&2
    return 1
  fi
  if [ "$DRIVER_UPDATED" = true ] &&
      ! mv -f "$DRIVER_NEXT" /usr/local/bin/agentbox-microsandbox-driver; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to activate the new Microsandbox driver" >&2
    return 1
  fi
  if ! update_worker_marker_phase "$MARKER" activated; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to journal Worker/driver activation" >&2
    return 1
  fi
  if ! worker_update_sync /usr/local/bin "$STATE_DIR"; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to persist the active Worker/driver pair" >&2
    return 1
  fi
  if ! schedule_worker_restart "$JOB_ID"; then
    abort_worker_update "$PREVIOUS" "$DRIVER_PREVIOUS" "$DRIVER_UPDATED" \
      "$DRIVER_HAD_PREVIOUS" "$JOB_ID" "$MARKER" "$CONFIRMED" "$ROLLBACK_SCRIPT" \
      "$WORKER_TMP" "$NEXT" "$DRIVER_NEXT" "$MARKER_TMP" || true
    echo "failed to schedule Worker restart" >&2
    return 1
  fi
  rm -f "$WORKER_TMP"
)

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
  if [ "${AGENTBOX_AGENT_TOOL_MODE:-ensure}" = upgrade ]; then
    echo "BoxLite portal interrupted an Agent update; restarting the control service and sandbox before resuming" >&2
    stop_boxlite_server
    ensure_boxlite_server || return 1
  else
    echo "BoxLite portal disconnected while installing Agent tools; restarting the sandbox and retrying once" >&2
  fi
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
  if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ] &&
     [ "${AGENTBOX_AGENT_TOOL_MODE:-ensure}" = upgrade ]; then
    echo "BoxLite command stream was interrupted during an Agent update; retrying without restarting the sandbox" >&2
    sleep 1
    if docker exec "$CONTAINER" "$@"; then
      return 0
    fi
    recover_boxlite_agent_tool_install "$CONTAINER" || return 1
    docker exec "$CONTAINER" "$@"
    return
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
  if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ] &&
     [ "${AGENTBOX_AGENT_TOOL_MODE:-ensure}" = upgrade ]; then
    echo "BoxLite input stream was interrupted during an Agent update; retrying without restarting the sandbox" >&2
    sleep 1
    if docker exec -i "$CONTAINER" "$@" < "$INPUT_FILE"; then
      return 0
    fi
    recover_boxlite_agent_tool_install "$CONTAINER" || return 1
    docker exec -i "$CONTAINER" "$@" < "$INPUT_FILE"
    return
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
  AGENT_EXEC_JOB_DIR=/opt/agentbox/jobs
  AGENT_EXEC_LOG_FILE="$AGENT_EXEC_JOB_DIR/$LABEL-install.log"
  AGENT_EXEC_STATUS_FILE="$AGENT_EXEC_LOG_FILE.status"
  agent_tool_exec "$CONTAINER" install -d -m 0700 "$AGENT_EXEC_JOB_DIR" || return 1
  INSTALL_ATTEMPT=1
  while [ "$INSTALL_ATTEMPT" -le 2 ]; do
    if ! boxlite_cli exec -d -u 0:0 "$CONTAINER" -- sh -lc '
        LOG_FILE=$1
        STATUS_FILE=$2
        shift 2
        rm -f "$LOG_FILE" "$STATUS_FILE" "$STATUS_FILE.tmp"
        "$@" >"$LOG_FILE" 2>&1
        CODE=$?
        printf "%s\n" "$CODE" >"$STATUS_FILE.tmp"
        mv -f "$STATUS_FILE.tmp" "$STATUS_FILE"
        exit "$CODE"
      ' agentbox "$AGENT_EXEC_LOG_FILE" "$AGENT_EXEC_STATUS_FILE" "$@" >/dev/null; then
      recover_boxlite_agent_tool_install "$CONTAINER" || return 1
      INSTALL_ATTEMPT=$((INSTALL_ATTEMPT + 1))
      continue
    fi
    DEADLINE=$(($(date +%s) + ${AGENTBOX_AGENT_TOOL_INSTALL_TIMEOUT_SECONDS:-1800}))
    PORTAL_FAILED=false
    PORTAL_FAILURES=0
    while [ "$(date +%s)" -lt "$DEADLINE" ]; do
      if ! STATUS=$(docker exec "$CONTAINER" sh -lc 'test -f "$1" && cat "$1" || true' agentbox "$AGENT_EXEC_STATUS_FILE"); then
        PORTAL_FAILURES=$((PORTAL_FAILURES + 1))
        if [ "$PORTAL_FAILURES" -lt 3 ]; then
          sleep 1
          continue
        fi
        echo "BoxLite portal remained unavailable while polling Agent tool installation; waiting for the detached task before recovery" >&2
        sleep 15
        if ! recover_boxlite_agent_tool_install "$CONTAINER" ||
           ! STATUS=$(docker exec "$CONTAINER" sh -lc 'test -f "$1" && cat "$1" || true' agentbox "$AGENT_EXEC_STATUS_FILE"); then
          PORTAL_FAILED=true
          break
        fi
        if [ -z "$STATUS" ]; then
          PORTAL_FAILED=true
          break
        fi
        PORTAL_FAILURES=0
      else
        PORTAL_FAILURES=0
      fi
      case "$STATUS" in
        0)
          docker exec "$CONTAINER" rm -f "$AGENT_EXEC_LOG_FILE" "$AGENT_EXEC_STATUS_FILE" >/dev/null 2>&1 || true
          return 0
          ;;
        '' ) sleep "${AGENTBOX_AGENT_TOOL_POLL_SECONDS:-5}" ;;
        * )
          docker exec "$CONTAINER" sh -lc 'test ! -f "$1" || tail -c 3000 "$1"' agentbox "$AGENT_EXEC_LOG_FILE" >&2 || true
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
    recover_boxlite_agent_tool_install "$CONTAINER" || true
    docker exec "$CONTAINER" sh -lc 'test ! -f "$1" || tail -c 3000 "$1"' \
      agentbox "$AGENT_EXEC_LOG_FILE" >&2 || true
    echo "Agent tool installation timed out after ${AGENTBOX_AGENT_TOOL_INSTALL_TIMEOUT_SECONDS:-1800} seconds: $LABEL" >&2
    return 1
  done
  return 1
}

copy_boxlite_agent_payload() {
  BOXLITE_COPY_TARGET=$1
  BOXLITE_COPY_SOURCE=$2
  BOXLITE_COPY_DESTINATION=$3
  stop_boxlite_server
  boxlite_local_cli start "$BOXLITE_COPY_TARGET" >/dev/null 2>&1 || true
  if boxlite_local_cli cp "$BOXLITE_COPY_SOURCE" \
      "$BOXLITE_COPY_TARGET:$BOXLITE_COPY_DESTINATION" >/dev/null; then
    BOXLITE_COPY_STATUS=0
  else
    BOXLITE_COPY_STATUS=$?
  fi
  if ! ensure_boxlite_server; then
    return 1
  fi
  return "$BOXLITE_COPY_STATUS"
}

checkpoint_boxlite_agent_update() {
  BOXLITE_CHECKPOINT_TARGET=$1
  sleep 6
  ensure_boxlite_server || return 1

  BOXLITE_CHECKPOINT_STOP_LOG=$(mktemp "$STATE_DIR/boxlite-agent-stop.XXXXXX") || return 1
  if ! boxlite_cli stop "$BOXLITE_CHECKPOINT_TARGET" \
      >/dev/null 2>"$BOXLITE_CHECKPOINT_STOP_LOG"; then
    BOXLITE_CHECKPOINT_INSPECT=$(boxlite_cli inspect "$BOXLITE_CHECKPOINT_TARGET" 2>/dev/null || true)
    BOXLITE_CHECKPOINT_BOX_STATUS=$(printf '%s' "$BOXLITE_CHECKPOINT_INSPECT" |
      boxlite_inspect_status)
    if [ "$BOXLITE_CHECKPOINT_BOX_STATUS" != stopped ]; then
      cat "$BOXLITE_CHECKPOINT_STOP_LOG" >&2
      rm -f "$BOXLITE_CHECKPOINT_STOP_LOG"
      return 1
    fi
  fi
  rm -f "$BOXLITE_CHECKPOINT_STOP_LOG"

  # libkrun keeps an open fd to the live qcow2 inode. Recreate the runtime
  # handle after stop so the snapshot cannot fork a disk that is still open.
  stop_boxlite_server
  ensure_boxlite_server || return 1
  BOXLITE_CHECKPOINT_INSPECT=$(boxlite_cli inspect "$BOXLITE_CHECKPOINT_TARGET") || return 1
  BOXLITE_CHECKPOINT_BOX_STATUS=$(printf '%s' "$BOXLITE_CHECKPOINT_INSPECT" |
    boxlite_inspect_status)
  [ "$BOXLITE_CHECKPOINT_BOX_STATUS" = stopped ] || return 1

  BOXLITE_CHECKPOINT_NAME="agent-update-$(date +%s%N)"
  BOXLITE_CHECKPOINT_RESULT=$(mktemp "$STATE_DIR/boxlite-agent-snapshot.XXXXXX") || return 1
  BOXLITE_CHECKPOINT_STATUS=$(curl -sS -o "$BOXLITE_CHECKPOINT_RESULT" \
    -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d "{\"name\":\"$BOXLITE_CHECKPOINT_NAME\"}" \
    "$BOXLITE_URL/v1/boxes/$BOXLITE_CHECKPOINT_TARGET/snapshots" || true)
  if [ "$BOXLITE_CHECKPOINT_STATUS" != 201 ]; then
    cat "$BOXLITE_CHECKPOINT_RESULT" >&2
    rm -f "$BOXLITE_CHECKPOINT_RESULT"
    return 1
  fi
  rm -f "$BOXLITE_CHECKPOINT_RESULT"

  stop_boxlite_server
  ensure_boxlite_server || return 1
  boxlite_cli start "$BOXLITE_CHECKPOINT_TARGET" >/dev/null
}

publish_boxlite_codex_package() {
  BOXLITE_CODEX_TARGET=$1
  BOXLITE_CODEX_HOST_PACKAGE=$2
  BOXLITE_CODEX_LATEST=$3
  BOXLITE_CODEX_BINARY_REL=$4
  BOXLITE_CODEX_STAGE="/opt/agentbox/tools/codex/.stage-$BOXLITE_CODEX_LATEST-$$"
  BOXLITE_CODEX_FINAL="/opt/agentbox/tools/codex/$BOXLITE_CODEX_LATEST"
  BOXLITE_CODEX_STATUS=0

  stop_boxlite_server
  boxlite_local_cli start "$BOXLITE_CODEX_TARGET" >/dev/null 2>&1 || true
  if ! boxlite_local_cli exec "$BOXLITE_CODEX_TARGET" -- sh -lc '
      set -eu
      STAGE=$1
      BINARY_REL=$2
      rm -rf "$STAGE"
      install -d -m 0755 "$STAGE/${BINARY_REL%/*}"
    ' agentbox "$BOXLITE_CODEX_STAGE" "$BOXLITE_CODEX_BINARY_REL" >/dev/null; then
    BOXLITE_CODEX_STATUS=1
  fi

  if [ "$BOXLITE_CODEX_STATUS" -eq 0 ] &&
     ! boxlite_local_cli cp \
        "$BOXLITE_CODEX_HOST_PACKAGE/$BOXLITE_CODEX_BINARY_REL" \
        "$BOXLITE_CODEX_TARGET:$BOXLITE_CODEX_STAGE/$BOXLITE_CODEX_BINARY_REL" \
        >/dev/null; then
    BOXLITE_CODEX_STATUS=1
  fi

  if [ "$BOXLITE_CODEX_STATUS" -eq 0 ] &&
     ! boxlite_local_cli exec "$BOXLITE_CODEX_TARGET" -- sh -lc '
      set -eu
      LATEST=$1
      BINARY_REL=$2
      STAGE=$3
      FINAL=$4
      STAGE_BINARY="$STAGE/$BINARY_REL"
      LINK_TMP="/usr/local/bin/.codex-agentbox.$$.tmp"
      OLD_FINAL="/opt/agentbox/tools/codex/.old-$LATEST-$$"
      trap '\''rm -f "$LINK_TMP"'\'' EXIT HUP INT TERM
      chmod 0755 "$STAGE_BINARY"
      "$STAGE_BINARY" --version | grep -Fq "$LATEST"
      rm -rf "$OLD_FINAL"
      [ ! -e "$FINAL" ] || mv "$FINAL" "$OLD_FINAL"
      if ! mv "$STAGE" "$FINAL"; then
        [ ! -e "$OLD_FINAL" ] || mv "$OLD_FINAL" "$FINAL"
        exit 1
      fi
      FINAL_BINARY="$FINAL/$BINARY_REL"
      "$FINAL_BINARY" --version | grep -Fq "$LATEST"
      ln -s "$FINAL_BINARY" "$LINK_TMP"
      "$LINK_TMP" --version | grep -Fq "$LATEST"
      mv -f "$LINK_TMP" /usr/local/bin/codex
      /usr/local/bin/codex --version | grep -Fq "$LATEST"
      setsid -f sh -lc '\''sync'\'' >/dev/null 2>&1
      trap - EXIT HUP INT TERM
      rm -rf "$OLD_FINAL"
    ' agentbox "$BOXLITE_CODEX_LATEST" "$BOXLITE_CODEX_BINARY_REL" \
      "$BOXLITE_CODEX_STAGE" "$BOXLITE_CODEX_FINAL" >/dev/null; then
    BOXLITE_CODEX_STATUS=1
  fi

  if [ "$BOXLITE_CODEX_STATUS" -eq 0 ]; then
    checkpoint_boxlite_agent_update "$BOXLITE_CODEX_TARGET" || BOXLITE_CODEX_STATUS=1
  elif ! ensure_boxlite_server; then
    return 1
  fi
  return "$BOXLITE_CODEX_STATUS"
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
  if ! curl --connect-timeout 15 --max-time 60 --retry 3 -fsSL \
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
  if ! curl --connect-timeout 15 --max-time 300 --retry 3 -fsSL \
      "$TARBALL_URL" -o "$ARCHIVE"; then
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
  OPENCODE_SIZE=$(wc -c < "$TMP_DIR/package/bin/opencode")
  if copy_boxlite_agent_payload "$CONTAINER" "$TMP_DIR/package/bin/opencode" \
      /opt/agentbox/opencode.upload; then
    STATUS=0
  else
    STATUS=$?
  fi
  rm -rf "$TMP_DIR"
  [ "$STATUS" -eq 0 ] || return "$STATUS"
  agent_tool_exec "$CONTAINER" sh -lc '
    set -eu
    UPLOAD=/opt/agentbox/opencode.upload
    EXPECTED_SIZE=$1
    [ "$(wc -c < "$UPLOAD")" -eq "$EXPECTED_SIZE" ]
    chmod 0755 "$UPLOAD"
    "$UPLOAD" --version >/dev/null
    mv -f "$UPLOAD" /usr/local/bin/opencode
    /usr/local/bin/opencode --version >/dev/null
  ' agentbox "$OPENCODE_SIZE"
}

install_boxlite_codex() {
  CONTAINER=$1
  case "$(uname -m)" in
    x86_64|amd64)
      PLATFORM=linux-x64
      BINARY_MEMBER=package/vendor/x86_64-unknown-linux-musl/bin/codex
      ;;
    aarch64|arm64)
      PLATFORM=linux-arm64
      BINARY_MEMBER=package/vendor/aarch64-unknown-linux-musl/bin/codex
      ;;
    *) echo "Codex does not provide a BoxLite binary for $(uname -m)" >&2; return 1 ;;
  esac
  BINARY_REL=${BINARY_MEMBER#package/}

  CACHE_DIR="$STATE_DIR/agent-tool-cache/codex"
  install -d -m 0700 "$CACHE_DIR" || return 1
  LATEST_META=$(mktemp "$CACHE_DIR/latest.XXXXXX") || return 1
  PLATFORM_META=$(mktemp "$CACHE_DIR/platform.XXXXXX") || { rm -f "$LATEST_META"; return 1; }
  cleanup_codex_metadata() {
    rm -f "$LATEST_META" "$PLATFORM_META"
  }
  if ! curl --connect-timeout 15 --max-time 60 --retry 3 -fsSL \
      'https://registry.npmjs.org/@openai%2fcodex/latest' -o "$LATEST_META"; then
    cleanup_codex_metadata
    return 1
  fi
  LATEST=$(jq -r '.version // empty' "$LATEST_META")
  NORMALIZED_LATEST=$(printf '%s' "$LATEST" | normalize_trusted_agent_tool_version) || {
    cleanup_codex_metadata
    return 1
  }
  [ "$NORMALIZED_LATEST" = "$LATEST" ] || { cleanup_codex_metadata; return 1; }
  PLATFORM_SPEC=$(jq -r --arg package "@openai/codex-$PLATFORM" \
    '.optionalDependencies[$package] // empty' "$LATEST_META")
  PLATFORM_VERSION=${PLATFORM_SPEC#npm:@openai/codex@}
  case "$LATEST" in ""|*[!0-9A-Za-z._-]*) cleanup_codex_metadata; return 1 ;; esac
  if [ "$PLATFORM_VERSION" = "$PLATFORM_SPEC" ] ||
     [ "$PLATFORM_VERSION" != "$LATEST-$PLATFORM" ]; then
    echo "Codex npm metadata does not contain the expected $PLATFORM package" >&2
    cleanup_codex_metadata
    return 1
  fi
  if ! curl --connect-timeout 15 --max-time 60 --retry 3 -fsSL \
      "https://registry.npmjs.org/@openai%2fcodex/$PLATFORM_VERSION" \
      -o "$PLATFORM_META"; then
    cleanup_codex_metadata
    return 1
  fi
  PACKAGE_NAME=$(jq -r '.name // empty' "$PLATFORM_META")
  PACKAGE_VERSION=$(jq -r '.version // empty' "$PLATFORM_META")
  TARBALL_URL=$(jq -r '.dist.tarball // empty' "$PLATFORM_META")
  INTEGRITY=$(jq -r '.dist.integrity // empty' "$PLATFORM_META")
  cleanup_codex_metadata
  if [ "$PACKAGE_NAME" != '@openai/codex' ] ||
     [ "$PACKAGE_VERSION" != "$PLATFORM_VERSION" ] ||
     [ -z "$TARBALL_URL" ] || [ "${INTEGRITY#sha512-}" = "$INTEGRITY" ]; then
    echo "Codex npm platform metadata is incomplete" >&2
    return 1
  fi
  EXPECTED_SHA512=$(printf '%s' "${INTEGRITY#sha512-}" | base64 -d | od -An -tx1 | tr -d ' \n')
  case "$EXPECTED_SHA512" in
    ''|*[!0-9a-f]*) echo "Codex npm integrity value is invalid" >&2; return 1 ;;
  esac
  ARCHIVE="$CACHE_DIR/codex-$PLATFORM_VERSION.tgz"
  archive_is_valid() {
    [ -s "$ARCHIVE" ] &&
      [ "$(sha512sum "$ARCHIVE" | cut -d ' ' -f 1)" = "$EXPECTED_SHA512" ]
  }

  if docker exec "$CONTAINER" sh -lc '
      set -eu
      FINAL="/opt/agentbox/tools/codex/$1/$2"
      [ -x "$FINAL" ]
      "$FINAL" --version | grep -Fq "$1"
      LINK_TMP="/usr/local/bin/.codex-agentbox.$$.tmp"
      trap '\''rm -f "$LINK_TMP"'\'' EXIT HUP INT TERM
      ln -s "$FINAL" "$LINK_TMP"
      "$LINK_TMP" --version | grep -Fq "$1"
      mv -f "$LINK_TMP" /usr/local/bin/codex
      trap - EXIT HUP INT TERM
    ' agentbox "$LATEST" "$BINARY_REL"; then
    report_agent_tool_progress codex cached "Codex $LATEST 已在沙箱缓存中"
    return 0
  fi

  if ! archive_is_valid; then
    report_agent_tool_progress codex running "正在从 npm 官方源并行下载 Codex $LATEST"
    TOTAL_SIZE=$(curl --connect-timeout 15 --max-time 60 --retry 3 -fsSI \
      -r 0-0 "$TARBALL_URL" | tr -d '\r' |
      awk 'tolower($1) == "content-range:" { split($3, value, "/"); print value[2] }' |
      tail -n 1)
    case "$TOTAL_SIZE" in
      ''|*[!0-9]*) TOTAL_SIZE= ;;
    esac
    if [ -n "$TOTAL_SIZE" ] && [ "$TOTAL_SIZE" -gt 8388608 ]; then
      PART_COUNT=8
      PART_SIZE=$(((TOTAL_SIZE + PART_COUNT - 1) / PART_COUNT))
      PIDS=
      PART_INDEX=0
      while [ "$PART_INDEX" -lt "$PART_COUNT" ]; do
        RANGE_START=$((PART_INDEX * PART_SIZE))
        RANGE_END=$((RANGE_START + PART_SIZE - 1))
        [ "$RANGE_END" -lt "$TOTAL_SIZE" ] || RANGE_END=$((TOTAL_SIZE - 1))
        PART_FILE="$ARCHIVE.part.$PART_INDEX"
        (
          EXPECTED_SIZE=$((RANGE_END - RANGE_START + 1))
          CURRENT_SIZE=0
          [ ! -f "$PART_FILE" ] || CURRENT_SIZE=$(wc -c < "$PART_FILE")
          if [ "$CURRENT_SIZE" -gt "$EXPECTED_SIZE" ]; then
            rm -f "$PART_FILE"
            CURRENT_SIZE=0
          fi
          if [ "$CURRENT_SIZE" -lt "$EXPECTED_SIZE" ]; then
            DOWNLOAD_START=$((RANGE_START + CURRENT_SIZE))
            curl --connect-timeout 15 --max-time 900 --retry 3 -fsSL \
              -r "$DOWNLOAD_START-$RANGE_END" "$TARBALL_URL" >> "$PART_FILE"
          fi
          [ "$(wc -c < "$PART_FILE")" -eq "$EXPECTED_SIZE" ]
        ) &
        PIDS="$PIDS $!"
        PART_INDEX=$((PART_INDEX + 1))
      done
      DOWNLOAD_STATUS=0
      for PART_PID in $PIDS; do
        wait "$PART_PID" || DOWNLOAD_STATUS=1
      done
      [ "$DOWNLOAD_STATUS" -eq 0 ] || return 1
      ARCHIVE_TMP="$ARCHIVE.tmp"
      : > "$ARCHIVE_TMP"
      PART_INDEX=0
      while [ "$PART_INDEX" -lt "$PART_COUNT" ]; do
        cat "$ARCHIVE.part.$PART_INDEX" >> "$ARCHIVE_TMP" || return 1
        PART_INDEX=$((PART_INDEX + 1))
      done
      [ "$(wc -c < "$ARCHIVE_TMP")" -eq "$TOTAL_SIZE" ] || return 1
      mv -f "$ARCHIVE_TMP" "$ARCHIVE"
    else
      ARCHIVE_TMP="$ARCHIVE.tmp"
      if ! curl --connect-timeout 15 --max-time 900 --retry 3 -fsSL \
          "$TARBALL_URL" -o "$ARCHIVE_TMP"; then
        rm -f "$ARCHIVE_TMP"
        return 1
      fi
      mv -f "$ARCHIVE_TMP" "$ARCHIVE"
    fi
  fi
  if ! archive_is_valid || ! tar -tzf "$ARCHIVE" "$BINARY_MEMBER" >/dev/null; then
    echo "Codex npm package integrity check failed" >&2
    rm -f "$ARCHIVE"
    return 1
  fi
  rm -f "$ARCHIVE".part.*

  HOST_PACKAGE="$CACHE_DIR/codex-$PLATFORM_VERSION.upload"
  HOST_BINARY="$HOST_PACKAGE/$BINARY_REL"
  if [ ! -x "$HOST_BINARY" ] ||
     ! "$HOST_BINARY" --version 2>/dev/null | grep -Fq "$LATEST"; then
    HOST_PACKAGE_TMP="$HOST_PACKAGE.tmp.$$"
    rm -rf "$HOST_PACKAGE_TMP"
    install -d -m 0700 "$HOST_PACKAGE_TMP"
    if ! tar -xzf "$ARCHIVE" -C "$HOST_PACKAGE_TMP" --strip-components=1 \
        "$BINARY_MEMBER" ||
       ! "$HOST_PACKAGE_TMP/$BINARY_REL" --version 2>/dev/null | grep -Fq "$LATEST"; then
      rm -rf "$HOST_PACKAGE_TMP"
      echo "Codex npm binary validation failed on the Worker" >&2
      return 1
    fi
    rm -rf "$HOST_PACKAGE"
    mv "$HOST_PACKAGE_TMP" "$HOST_PACKAGE"
  fi
  report_agent_tool_progress codex running "正在将 Codex $LATEST 发布到沙箱"
  PUBLISH_ATTEMPT=1
  while [ "$PUBLISH_ATTEMPT" -le 2 ]; do
    if publish_boxlite_codex_package "$CONTAINER" "$HOST_PACKAGE" \
        "$LATEST" "$BINARY_REL"; then
      return 0
    fi
    echo "Codex publish was interrupted; restoring the BoxLite control service and retrying from the Worker cache" >&2
    PUBLISH_ATTEMPT=$((PUBLISH_ATTEMPT + 1))
    if [ "$PUBLISH_ATTEMPT" -le 2 ]; then
      recover_boxlite_agent_tool_install "$CONTAINER" || return 1
    fi
  done
  return 1
}

install_agent_tools() {
  CONTAINER=$1
  JOB_FILE=$2
  MODE=${AGENTBOX_AGENT_TOOL_MODE:-ensure}
  PROGRESS_STAGE=${AGENTBOX_AGENT_TOOL_PROGRESS_STAGE:-agent-tools}
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE" | sort -u)
  [ -n "$TOOLS" ] || return 0
  if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ] && [ "$MODE" = upgrade ] &&
     ! docker exec "$CONTAINER" true >/dev/null 2>&1; then
    report_job_progress "$PROGRESS_STAGE" "正在恢复 BoxLite Agent 更新连接"
    recover_boxlite_agent_tool_install "$CONTAINER" || return 1
    docker exec "$CONTAINER" true >/dev/null 2>&1 || return 1
  fi
  if ! docker exec "$CONTAINER" sh -lc \
      'for command in curl git jq unzip python3; do command -v "$command" >/dev/null || exit 1; done'; then
    report_job_progress "$PROGRESS_STAGE" "正在安装 Agent 工具依赖"
    if ! agent_tool_exec_logged "$CONTAINER" prerequisites sh -lc \
        'export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get install -y curl ca-certificates git jq unzip python3' >&2; then
      echo "Agent tool prerequisite installation failed" >&2
      return 1
    fi
  else
    report_job_progress "$PROGRESS_STAGE" "Agent 工具依赖已就绪"
  fi
  INSTALL_PI=false
  INSTALL_OPENCODE=false
  INSTALL_CODEX=false
  set --
  for TOOL in $TOOLS; do
    COMMAND=$(agent_tool_command "$TOOL") || { echo "unsupported Agent tool: $TOOL" >&2; return 1; }
    if [ "$MODE" = ensure ] && [ "$TOOL" != pi ] && docker exec "$CONTAINER" sh -lc '
        command -v "$1" >/dev/null || exit 1
        timeout 20 "$1" --version >/dev/null 2>&1
      ' agentbox "$COMMAND" >/dev/null 2>&1; then
      report_agent_tool_progress "$TOOL" cached "已复用 Agent 工具缓存：$TOOL"
      continue
    fi
    case "$TOOL" in
      antigravity|cursor|qwenpaw) continue ;;
      codex)
        if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ]; then
          INSTALL_CODEX=true
          continue
        fi
        PACKAGE='@openai/codex'
        ;;
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
        'set -eu; find /usr/local/lib/node_modules -maxdepth 1 -type d -name '\''.opencode-ai-*'\'' -exec rm -rf -- {} +; if [ -d /usr/local/lib/node_modules/@openai ]; then find /usr/local/lib/node_modules/@openai -maxdepth 1 -type d -name '\''.codex-*'\'' -exec rm -rf -- {} +; fi; npm install -g "$@" || { npm cache clean --force >/dev/null 2>&1 || true; npm install -g "$@"; }' \
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
  if [ "$INSTALL_CODEX" = true ]; then
    report_agent_tool_progress codex running "正在准备 Codex npm 官方包"
    if ! install_boxlite_codex "$CONTAINER" >&2; then
      report_agent_tool_progress codex failed "Agent 工具安装失败：Codex"
      echo "Agent tool package installation failed: codex" >&2
      return 1
    fi
    report_agent_tool_progress codex installed "Codex npm 官方包已安装，等待验证"
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
    if ! docker exec "$CONTAINER" sh -lc '
      command -v "$1" >/dev/null || exit 1
      timeout 20 "$1" --version >/dev/null 2>&1
    ' agentbox "$COMMAND" >/dev/null 2>&1; then
      report_agent_tool_progress "$TOOL" failed "Agent 工具验证失败：$TOOL"
      echo "Agent tool $TOOL was installed but command $COMMAND is unavailable" >&2
      rm -f "$VERSION_FILE"
      return 1
    fi
    NEXT_VERSION_FILE=$(mktemp)
    jq --arg tool "$TOOL" '. + {($tool): "installed"}' \
      "$VERSION_FILE" > "$NEXT_VERSION_FILE"
    mv "$NEXT_VERSION_FILE" "$VERSION_FILE"
    report_agent_tool_progress "$TOOL" succeeded "Agent 工具已就绪：$TOOL"
  done
  if [ "$MODE" != ensure ]; then
    EXISTING_VERSION_FILE=$(mktemp)
    MERGED_VERSION_FILE=$(mktemp)
    if agent_tool_exec "$CONTAINER" cat /opt/agentbox/agent-versions.json > "$EXISTING_VERSION_FILE" 2>/dev/null &&
       jq -s '((.[0] | if type == "object" then with_entries(.value = "installed") else {} end) * .[1])' \
         "$EXISTING_VERSION_FILE" "$VERSION_FILE" > "$MERGED_VERSION_FILE" 2>/dev/null; then
      mv "$MERGED_VERSION_FILE" "$VERSION_FILE"
    else
      rm -f "$MERGED_VERSION_FILE"
    fi
    rm -f "$EXISTING_VERSION_FILE"
  fi
  if ! agent_tool_exec "$CONTAINER" mkdir -p /opt/agentbox ||
     ! agent_tool_exec "$CONTAINER" rm -rf /opt/agentbox/agent-versions.json ||
     ! agent_tool_exec_stdin "$CONTAINER" "$VERSION_FILE" sh -c 'cat > /opt/agentbox/agent-versions.json'; then
    echo "Agent tool version metadata could not be written" >&2
    rm -f "$VERSION_FILE"
    return 1
  fi
  rm -f "$VERSION_FILE"
}

agent_tool_source() {
  case "$1" in
    codex) printf '%s' npm ;;
    grok) printf '%s' official-installer ;;
    opencode)
      if [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ]; then
        printf '%s' boxlite-binary
      else
        printf '%s' npm
      fi
      ;;
    *) printf '%s' npm ;;
  esac
}

normalize_trusted_agent_tool_version() {
  LC_ALL=C head -c 4096 | jq -Rsj '
    [splits("[[:space:]]+") |
      select(length > 0) |
      sub("^[vV]"; "") |
      select(test("^(0|[1-9][0-9]{0,5})\\.(0|[1-9][0-9]{0,5})\\.(0|[1-9][0-9]{0,5})(-(alpha|beta|rc|pre|dev|canary|next)(\\.(0|[1-9][0-9]{0,5}))?)?$"))
    ][0] // empty
  '
}

agent_tool_probe_usable() {
  CONTAINER=$1
  TOOL_ID=$2
  COMMAND=$(agent_tool_command "$TOOL_ID") || return 1
  PROBE_ATTEMPT=1
  while [ "$PROBE_ATTEMPT" -le 3 ]; do
    if docker exec "$CONTAINER" sh -lc \
        'command -v "$1" >/dev/null || exit 1; timeout 20 "$1" --version >/dev/null 2>&1' \
        agentbox "$COMMAND" >/dev/null 2>&1; then
      return 0
    fi
    [ "${AGENTBOX_RUNTIME_DRIVER:-docker}" = boxlite ] || return 2
    PROBE_ATTEMPT=$((PROBE_ATTEMPT + 1))
    [ "$PROBE_ATTEMPT" -le 3 ] || return 2
    sleep 1
  done
  return 2
}

append_agent_tool_state() {
  RESULT_FILE=$1
  TOOL_ID=$2
  CURRENT_VERSION=$3
  LATEST_VERSION=$4
  PREVIOUS_VERSION=$5
  STATUS=$6
  MESSAGE=$7
  SOURCE=$8
  CHECKED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  NEXT_RESULT_FILE=$(mktemp)
  jq --arg tool "$TOOL_ID" --arg currentVersion "$CURRENT_VERSION" \
    --arg latestVersion "$LATEST_VERSION" --arg previousVersion "$PREVIOUS_VERSION" \
    --arg status "$STATUS" --arg message "$MESSAGE" --arg source "$SOURCE" \
    --arg checkedAt "$CHECKED_AT" \
    '. + [{tool:$tool,currentVersion:$currentVersion,latestVersion:$latestVersion,
      previousVersion:$previousVersion,status:$status,message:$message,
      source:$source,checkedAt:$checkedAt}]' "$RESULT_FILE" > "$NEXT_RESULT_FILE"
  mv "$NEXT_RESULT_FILE" "$RESULT_FILE"
}

prepare_agent_tool_operation() {
  JOB_FILE=$1
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  case "$DRIVER" in docker|boxlite|microsandbox) ;; *) return 1 ;; esac
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  TARGET=$(sandbox_container_name "$JOB_FILE")
  if [ "$DRIVER" = docker ]; then
    docker inspect "$TARGET" >/dev/null 2>&1 || return 1
  else
    runtime_call "$DRIVER" inspect "$TARGET" >/dev/null 2>&1 || return 1
  fi
  configure_proxy "$TARGET" "$JOB_FILE"
}

check_agent_tools() {
  JOB_FILE=$1
  RESULT_FILE=$2
  printf '[]\n' > "$RESULT_FILE"
  prepare_agent_tool_operation "$JOB_FILE" || return 1
  TOOLS=$(jq -r '.job.payload.requestedAgentTools[]?' "$JOB_FILE")
  [ -n "$TOOLS" ] || return 1
  PROGRESS_STAGE=agent-tools-check
  export PROGRESS_STAGE
  for TOOL_ID in $TOOLS; do
    report_agent_tool_progress "$TOOL_ID" running "正在检测 Agent 工具：$TOOL_ID"
    SOURCE=$(agent_tool_source "$TOOL_ID")
    COMMAND=$(agent_tool_command "$TOOL_ID") || return 1
    if ! docker exec "$TARGET" sh -lc 'command -v "$1" >/dev/null' agentbox "$COMMAND"; then
      append_agent_tool_state "$RESULT_FILE" "$TOOL_ID" "" "" "" \
        not-installed "Agent 工具尚未安装" "$SOURCE"
      report_agent_tool_progress "$TOOL_ID" succeeded "Agent 工具尚未安装：$TOOL_ID"
      continue
    fi
    if agent_tool_probe_usable "$TARGET" "$TOOL_ID"; then
      append_agent_tool_state "$RESULT_FILE" "$TOOL_ID" "" "" "" \
        installed "Agent 工具命令可用；为保护沙箱数据，未采集版本输出" "$SOURCE"
      report_agent_tool_progress "$TOOL_ID" succeeded "Agent 工具检测完成：$TOOL_ID"
    else
      append_agent_tool_state "$RESULT_FILE" "$TOOL_ID" "" "" "" \
        broken "命令存在，但可用性探测失败" "$SOURCE"
      report_agent_tool_progress "$TOOL_ID" failed "Agent 工具可用性检测失败：$TOOL_ID"
    fi
  done
}

update_agent_tools() {
  JOB_FILE=$1
  RESULT_FILE=$2
  printf '[]\n' > "$RESULT_FILE"
  prepare_agent_tool_operation "$JOB_FILE" || return 1
  TOOLS=$(jq -r '.job.payload.requestedAgentTools[]?' "$JOB_FILE")
  [ -n "$TOOLS" ] || return 1
  FAILURES=0
  PROGRESS_STAGE=agent-tools-update
  export PROGRESS_STAGE
  for TOOL_ID in $TOOLS; do
    BEFORE_USABLE=false
    agent_tool_probe_usable "$TARGET" "$TOOL_ID" && BEFORE_USABLE=true
    SOURCE=$(agent_tool_source "$TOOL_ID")
    TOOL_JOB_FILE=$(mktemp)
    jq --arg tool "$TOOL_ID" '.job.payload.agentTools = [$tool]' "$JOB_FILE" > "$TOOL_JOB_FILE"
    report_agent_tool_progress "$TOOL_ID" running "正在更新 Agent 工具：$TOOL_ID"
    PREVIOUS_MODE=${AGENTBOX_AGENT_TOOL_MODE:-}
    PREVIOUS_STAGE=${AGENTBOX_AGENT_TOOL_PROGRESS_STAGE:-}
    AGENTBOX_AGENT_TOOL_MODE=upgrade
    AGENTBOX_AGENT_TOOL_PROGRESS_STAGE=agent-tools-update
    export AGENTBOX_AGENT_TOOL_MODE AGENTBOX_AGENT_TOOL_PROGRESS_STAGE
    if install_agent_tools "$TARGET" "$TOOL_JOB_FILE"; then
      UPDATE_OK=true
    else
      UPDATE_OK=false
    fi
    AGENTBOX_AGENT_TOOL_MODE=$PREVIOUS_MODE
    AGENTBOX_AGENT_TOOL_PROGRESS_STAGE=$PREVIOUS_STAGE
    export AGENTBOX_AGENT_TOOL_MODE AGENTBOX_AGENT_TOOL_PROGRESS_STAGE
    rm -f "$TOOL_JOB_FILE"
    AFTER_USABLE=false
    agent_tool_probe_usable "$TARGET" "$TOOL_ID" && AFTER_USABLE=true
    if [ "$UPDATE_OK" != true ] || [ "$AFTER_USABLE" != true ]; then
      append_agent_tool_state "$RESULT_FILE" "$TOOL_ID" "" "" "" \
        failed "Agent 工具更新失败" "$SOURCE"
      report_agent_tool_progress "$TOOL_ID" failed "Agent 工具更新失败：$TOOL_ID"
      FAILURES=$((FAILURES + 1))
    elif [ "$BEFORE_USABLE" != true ]; then
      append_agent_tool_state "$RESULT_FILE" "$TOOL_ID" "" "" "" \
        updated "Agent 工具已安装；为保护沙箱数据，未采集版本输出" "$SOURCE"
      report_agent_tool_progress "$TOOL_ID" succeeded "Agent 工具已安装：$TOOL_ID"
    else
      append_agent_tool_state "$RESULT_FILE" "$TOOL_ID" "" "" "" \
        updated "Agent 工具更新命令已完成；为保护沙箱数据，未采集版本输出" "$SOURCE"
      report_agent_tool_progress "$TOOL_ID" succeeded "Agent 工具已更新：$TOOL_ID"
    fi
  done
  [ "$FAILURES" -eq 0 ]
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
  BUILD_CONTAINER="agentbox-runtime-build-$CACHE_KEY-${JOB_ID:-manual}-${LEASE_GENERATION:-0}"
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
    BUILD_OWNER=$(docker inspect -f '{{ index .Config.Labels "agentbox.runtime.build" }}' "$BUILD_CONTAINER")
    [ "$BUILD_OWNER" = "$CACHE_KEY" ] || { echo "runtime build container name collision: $BUILD_CONTAINER" >&2; return 1; }
    docker rm -f -v "$BUILD_CONTAINER" >/dev/null || return 1
  fi
  record_create_resource docker-build-delete "$BUILD_CONTAINER" "$CACHE_KEY"
  [ -z "${CREATE_STATE_DIR:-}" ] || : > "$CREATE_STATE_DIR/build-create-pending"
  if ! docker run -d --name "$BUILD_CONTAINER" \
    --label "agentbox.runtime.build=$CACHE_KEY" \
    "$BUILD_BASE_IMAGE" sh -lc 'trap : TERM INT; sleep infinity & wait' >/dev/null; then
    [ -z "${CREATE_STATE_DIR:-}" ] || rm -f "$CREATE_STATE_DIR/build-create-pending"
    return 1
  fi
  [ -z "${CREATE_STATE_DIR:-}" ] || rm -f "$CREATE_STATE_DIR/build-create-pending"
  record_create_resource docker-image "$REFRESH_IMAGE"
  if ! configure_proxy "$BUILD_CONTAINER" "$JOB_FILE"; then
    echo "Agent runtime build could not apply the selected network proxy" >&2
    docker rm -f -v "$BUILD_CONTAINER" >/dev/null 2>&1 || echo "Agent runtime build container cleanup failed: $BUILD_CONTAINER" >&2
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
    if ! docker rm -f -v "$BUILD_CONTAINER" >/dev/null; then
      echo "Agent runtime build container cleanup failed: $BUILD_CONTAINER" >&2
      return 1
    fi
    docker image rm "$REFRESH_IMAGE" >/dev/null 2>&1 || true
    report_job_progress agent-image "Agent 工具镜像缓存已更新" refreshed cache-created
    printf '%s' "$CACHE_IMAGE"
    return 0
  fi

  remove_proxy "$BUILD_CONTAINER"
  if ! docker rm -f -v "$BUILD_CONTAINER" >/dev/null; then
    echo "Agent runtime build container cleanup failed: $BUILD_CONTAINER" >&2
    return 1
  fi
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
  FILTERED=$(mktemp) || return 1
  if ! awk -v key="$KEY" 'index($0, key "=") != 1' "$ENV_FILE" > "$FILTERED" ||
     ! cat "$FILTERED" > "$ENV_FILE"; then
    rm -f "$FILTERED"
    return 1
  fi
  rm -f "$FILTERED" || return 1
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
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  for ENTRY in localhost 127.0.0.1 ::1 "$(server_url_host)" \
    "$(sandbox_control_plane_host "$DRIVER")" host.boxlite.internal; do
    [ -n "$ENTRY" ] || continue
    case ",$NO_PROXY," in
      *,$ENTRY,*) ;;
      *) NO_PROXY=${NO_PROXY:+$NO_PROXY,}$ENTRY ;;
    esac
  done
  PROXY_ENV=$(mktemp)
  PROXY_LOADER=$(mktemp)
  chmod 600 "$PROXY_ENV"
  for NAME in HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy; do
    append_env "$PROXY_ENV" "$NAME" "$PROXY_URL"
  done
  append_env "$PROXY_ENV" NO_PROXY "$NO_PROXY"
  append_env "$PROXY_ENV" no_proxy "$NO_PROXY"
  append_env "$PROXY_ENV" NODE_USE_ENV_PROXY 1
  append_env "$PROXY_ENV" CLAUDE_CODE_PROXY_RESOLVES_HOSTS 1
  printf '%s\n' \
    'if [ -r /opt/agentbox/secrets/proxy.env ]; then' \
    '  set -a' \
    '  . /opt/agentbox/secrets/proxy.env' \
    '  set +a' \
    'fi' > "$PROXY_LOADER"

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

append_env_file() {
  ENV_FILE=$1
  KEY=$2
  VALUE_FILE=$3
  VALUE_LINES=$(wc -l < "$VALUE_FILE") || return 1
  VALUE_LINES=$(printf '%s' "$VALUE_LINES" | tr -d '[:space:]') || return 1
  if [ "$VALUE_LINES" -gt 0 ]; then
    ENCODED_FILE=$(mktemp) || return 1
    if ! chmod 600 "$ENCODED_FILE" || ! base64 < "$VALUE_FILE" > "$ENCODED_FILE"; then
      rm -f "$ENCODED_FILE"
      return 1
    fi
    ENCODED_VALUE=$(tr -d '\r\n' < "$ENCODED_FILE") || {
      rm -f "$ENCODED_FILE"
      return 1
    }
    rm -f "$ENCODED_FILE" || return 1
    printf "%s=\$(printf '%%s' '%s' | base64 -d; printf x); %s=\${%s%%x}\n" \
      "$KEY" "$ENCODED_VALUE" "$KEY" "$KEY" >> "$ENV_FILE" || return 1
    return 0
  fi
  printf "%s='" "$KEY" >> "$ENV_FILE" || return 1
  sed "s/'/'\"'\"'/g" "$VALUE_FILE" >> "$ENV_FILE" || return 1
  printf "'\n" >> "$ENV_FILE" || return 1
}

strip_one_line_ending() {
  VALUE_FILE=$1
  VALUE_SIZE=$(wc -c < "$VALUE_FILE" | tr -d '[:space:]') || return 1
  [ "$VALUE_SIZE" -gt 0 ] || return 0
  LAST_BYTE=$(tail -c 1 "$VALUE_FILE" | od -An -tu1 | tr -d '[:space:]') || return 1
  [ "$LAST_BYTE" = 10 ] || return 0
  TRIMMED_FILE=$(mktemp) || return 1
  if ! head -c "$((VALUE_SIZE - 1))" "$VALUE_FILE" > "$TRIMMED_FILE"; then
    rm -f "$TRIMMED_FILE"
    return 1
  fi
  if ! mv "$TRIMMED_FILE" "$VALUE_FILE"; then
    rm -f "$TRIMMED_FILE"
    return 1
  fi
  VALUE_SIZE=$((VALUE_SIZE - 1))
  [ "$VALUE_SIZE" -gt 0 ] || return 0
  LAST_BYTE=$(tail -c 1 "$VALUE_FILE" | od -An -tu1 | tr -d '[:space:]') || return 1
  [ "$LAST_BYTE" = 13 ] || return 0
  TRIMMED_FILE=$(mktemp) || return 1
  if ! head -c "$((VALUE_SIZE - 1))" "$VALUE_FILE" > "$TRIMMED_FILE"; then
    rm -f "$TRIMMED_FILE"
    return 1
  fi
  if ! mv "$TRIMMED_FILE" "$VALUE_FILE"; then
    rm -f "$TRIMMED_FILE"
    return 1
  fi
}

worker_secret_path() {
  printf '/etc/agentbox-worker-secrets/%s' "$1"
}

worker_secret_metadata() {
  stat -c '%u:%a:%s' "$1"
}

resolve_worker_variables() (
  RESOLVE_JOB_FILE=$1
  RESOLVE_OUTPUT_FILE=$2
  RESOLVE_LIST=$(mktemp) || exit 1
  RESOLVE_ENTRIES=$(mktemp) || { rm -f "$RESOLVE_LIST"; exit 1; }
  RESOLVE_VALUE=$(mktemp) || { rm -f "$RESOLVE_LIST" "$RESOLVE_ENTRIES"; exit 1; }
  trap 'rm -f "$RESOLVE_LIST" "$RESOLVE_ENTRIES" "$RESOLVE_VALUE"' EXIT
  chmod 600 "$RESOLVE_LIST" "$RESOLVE_ENTRIES" "$RESOLVE_VALUE" || exit 1
  if ! jq -c '.job.payload.variables[]?' "$RESOLVE_JOB_FILE" > "$RESOLVE_LIST"; then
    echo 'AgentBox variable definitions are invalid' >&2
    exit 1
  fi
  : > "$RESOLVE_ENTRIES" || exit 1
  while IFS= read -r RESOLVE_VARIABLE; do
    RESOLVE_KEY=$(printf '%s' "$RESOLVE_VARIABLE" | jq -er '.spec.key') || exit 1
    RESOLVE_MODE=$(printf '%s' "$RESOLVE_VARIABLE" | jq -er '.spec.mode') || exit 1
    RESOLVE_REFERENCE=$(printf '%s' "$RESOLVE_VARIABLE" | jq -er '.spec.reference') || exit 1
    case "$RESOLVE_KEY" in ''|[0-9]*|*[!A-Za-z0-9_]*) echo "AgentBox variable key is invalid: $RESOLVE_KEY" >&2; exit 1 ;; esac
    case "$RESOLVE_REFERENCE" in
      env://*)
        [ "$RESOLVE_MODE" = value-ref ] || { echo "AgentBox variable mode does not match env reference: $RESOLVE_KEY" >&2; exit 1; }
        RESOLVE_SOURCE=${RESOLVE_REFERENCE#env://}
        case "$RESOLVE_SOURCE" in ''|[0-9]*|*[!A-Za-z0-9_]*) echo "AgentBox env reference is invalid: $RESOLVE_KEY" >&2; exit 1 ;; esac
        if ! printenv "$RESOLVE_SOURCE" > "$RESOLVE_VALUE"; then
          echo "AgentBox env reference is unavailable: $RESOLVE_KEY" >&2
          exit 1
        fi
        strip_one_line_ending "$RESOLVE_VALUE" || exit 1
        RESOLVE_SIZE=$(wc -c < "$RESOLVE_VALUE" | tr -d '[:space:]') || exit 1
        [ "$RESOLVE_SIZE" -le 16384 ] || { echo "AgentBox env value exceeds 16 KiB: $RESOLVE_KEY" >&2; exit 1; }
        ;;
      secret://*)
        [ "$RESOLVE_MODE" = secret-ref ] || { echo "AgentBox variable mode does not match secret reference: $RESOLVE_KEY" >&2; exit 1; }
        RESOLVE_SOURCE=${RESOLVE_REFERENCE#secret://}
        case "$RESOLVE_SOURCE" in ''|[0-9]*|*[!A-Za-z0-9_]*) echo "AgentBox secret reference is invalid: $RESOLVE_KEY" >&2; exit 1 ;; esac
        RESOLVE_SECRET=$(worker_secret_path "$RESOLVE_SOURCE") || exit 1
        if [ ! -f "$RESOLVE_SECRET" ] || [ -L "$RESOLVE_SECRET" ]; then
          echo "AgentBox secret reference is not a regular file: $RESOLVE_KEY" >&2
          exit 1
        fi
        RESOLVE_METADATA=$(worker_secret_metadata "$RESOLVE_SECRET") || { echo "AgentBox secret metadata is unavailable: $RESOLVE_KEY" >&2; exit 1; }
        RESOLVE_OWNER=${RESOLVE_METADATA%%:*}
        RESOLVE_REMAINDER=${RESOLVE_METADATA#*:}
        RESOLVE_MODE_BITS=${RESOLVE_REMAINDER%%:*}
        RESOLVE_SIZE=${RESOLVE_REMAINDER##*:}
        case "$RESOLVE_OWNER:$RESOLVE_MODE_BITS:$RESOLVE_SIZE" in
          *[!0-9:]*|::*|*::*) echo "AgentBox secret metadata is invalid: $RESOLVE_KEY" >&2; exit 1 ;;
        esac
        [ "$RESOLVE_OWNER" = 0 ] || { echo "AgentBox secret is not root-owned: $RESOLVE_KEY" >&2; exit 1; }
        RESOLVE_MODE_DECIMAL=$(printf '%s' "$RESOLVE_MODE_BITS" | sed 's/^0*//') || exit 1
        [ -n "$RESOLVE_MODE_DECIMAL" ] || RESOLVE_MODE_DECIMAL=0
        [ $((RESOLVE_MODE_DECIMAL % 100)) -eq 0 ] || { echo "AgentBox secret permissions are too broad: $RESOLVE_KEY" >&2; exit 1; }
        [ "$RESOLVE_SIZE" -le 16384 ] || { echo "AgentBox secret exceeds 16 KiB: $RESOLVE_KEY" >&2; exit 1; }
        if ! tr -d '\000' < "$RESOLVE_SECRET" | cmp -s - "$RESOLVE_SECRET"; then
          echo "AgentBox secret contains NUL: $RESOLVE_KEY" >&2
          exit 1
        fi
        cp "$RESOLVE_SECRET" "$RESOLVE_VALUE" || exit 1
        strip_one_line_ending "$RESOLVE_VALUE" || exit 1
        ;;
      *) echo "AgentBox variable reference scheme is invalid: $RESOLVE_KEY" >&2; exit 1 ;;
    esac
    if ! jq -Rsc --arg key "$RESOLVE_KEY" '{key: $key, value: .}' "$RESOLVE_VALUE" >> "$RESOLVE_ENTRIES"; then
      exit 1
    fi
  done < "$RESOLVE_LIST"
  if ! jq -se '
    if (map(.key) | length) != (map(.key) | unique | length)
    then error("duplicate AgentBox variable key")
    else map({key: .key, value: .value}) | from_entries
    end
  ' "$RESOLVE_ENTRIES" > "$RESOLVE_OUTPUT_FILE"; then
    echo 'AgentBox resolved variable set is invalid' >&2
    exit 1
  fi
)

write_container_file_atomic() {
  WRITE_CONTAINER=$1
  WRITE_PATH=$2
  WRITE_MODE=$3
  WRITE_SOURCE=$4
  docker exec -i "$WRITE_CONTAINER" sh -c '
    set -eu
    WRITE_PATH=$1
    WRITE_MODE=$2
    WRITE_PARENT=${WRITE_PATH%/*}
    mkdir -p "$WRITE_PARENT"
    WRITE_TEMP=$(mktemp "$WRITE_PARENT/.agentbox-next.XXXXXX")
    trap '\''rm -f "$WRITE_TEMP"'\'' EXIT HUP INT TERM
    cat > "$WRITE_TEMP"
    chmod "$WRITE_MODE" "$WRITE_TEMP"
    mv -f "$WRITE_TEMP" "$WRITE_PATH"
    trap - EXIT HUP INT TERM
  ' agentbox "$WRITE_PATH" "$WRITE_MODE" < "$WRITE_SOURCE"
}

managed_paths_reconcile() {
  MANAGED_CONTAINER=$1
  MANAGED_OPERATIONS=$2
  docker exec -i "$MANAGED_CONTAINER" sh -c '
    set -eu
    MANAGED_STATE_DIR=$1
    MANAGED_NEWLINE=$(printf "\nx")
    MANAGED_NEWLINE=${MANAGED_NEWLINE%x}
    managed_no_symlink_chain() {
      MANAGED_CHECK=$1
      while :; do
        [ ! -L "$MANAGED_CHECK" ] || return 1
        [ "$MANAGED_CHECK" != / ] || return 0
        MANAGED_CHECK_PARENT=${MANAGED_CHECK%/*}
        [ -n "$MANAGED_CHECK_PARENT" ] || MANAGED_CHECK_PARENT=/
        [ "$MANAGED_CHECK_PARENT" != "$MANAGED_CHECK" ] || return 1
        MANAGED_CHECK=$MANAGED_CHECK_PARENT
      done
    }
    if ! managed_no_symlink_chain "$MANAGED_STATE_DIR"; then
      echo "AgentBox managed state path has a symlink ancestor" >&2
      exit 1
    fi
    MANAGED_OPERATIONS=$(mktemp "$MANAGED_STATE_DIR/.managed-operations.XXXXXX")
    MANAGED_STAGED=$(mktemp "$MANAGED_STATE_DIR/.managed-staged.XXXXXX")
    MANAGED_APPLIED=$(mktemp "$MANAGED_STATE_DIR/.managed-applied.XXXXXX")
    MANAGED_SEEN=$(mktemp "$MANAGED_STATE_DIR/.managed-seen.XXXXXX")
    MANAGED_SUCCESS=false
    managed_cleanup() {
      MANAGED_STATUS=$?
      MANAGED_CLEANUP_FAILED=false
      trap - EXIT HUP INT TERM
      if [ "$MANAGED_SUCCESS" != true ]; then
        while IFS="$(printf '\''\t'\'')" read -r MANAGED_TARGET MANAGED_BACKUP MANAGED_HAD_OLD; do
          [ -n "$MANAGED_TARGET" ] || continue
          if ! rm -rf "$MANAGED_TARGET"; then
            echo "AgentBox managed rollback could not remove a staged target" >&2
            MANAGED_CLEANUP_FAILED=true
            continue
          fi
          if [ "$MANAGED_HAD_OLD" = 1 ]; then
            if ! mv "$MANAGED_BACKUP" "$MANAGED_TARGET"; then
              echo "AgentBox managed rollback could not restore a previous target" >&2
              MANAGED_CLEANUP_FAILED=true
            fi
          fi
        done < "$MANAGED_APPLIED"
      else
        while IFS="$(printf '\''\t'\'')" read -r MANAGED_TARGET MANAGED_BACKUP MANAGED_HAD_OLD; do
          if [ "$MANAGED_HAD_OLD" = 1 ] && ! rm -rf "$MANAGED_BACKUP"; then
            echo "AgentBox managed commit could not remove a backup target" >&2
            MANAGED_CLEANUP_FAILED=true
          fi
        done < "$MANAGED_APPLIED"
      fi
      while IFS="$(printf '\''\t'\'')" read -r MANAGED_ACTION MANAGED_TARGET MANAGED_NEXT MANAGED_BACKUP MANAGED_MODE; do
        if [ -n "$MANAGED_NEXT" ] && ! rm -rf "$MANAGED_NEXT"; then
          echo "AgentBox managed reconciliation could not remove a staged path" >&2
          MANAGED_CLEANUP_FAILED=true
        fi
      done < "$MANAGED_STAGED"
      if ! rm -f "$MANAGED_OPERATIONS" "$MANAGED_STAGED" "$MANAGED_APPLIED" "$MANAGED_SEEN"; then
        echo "AgentBox managed reconciliation could not remove transaction metadata" >&2
        MANAGED_CLEANUP_FAILED=true
      fi
      [ "$MANAGED_CLEANUP_FAILED" != true ] || MANAGED_STATUS=1
      exit "$MANAGED_STATUS"
    }
    trap managed_cleanup EXIT
    trap '\''exit 1'\'' HUP INT TERM
    cat > "$MANAGED_OPERATIONS"
    MANAGED_INDEX=0
    while IFS="$(printf '\''\t'\'')" read -r MANAGED_ACTION MANAGED_TARGET MANAGED_SOURCE MANAGED_REPLACE MANAGED_MODE; do
      [ -n "$MANAGED_ACTION" ] || continue
      case "$MANAGED_ACTION" in put|delete) ;; *) echo "invalid AgentBox managed operation" >&2; exit 1 ;; esac
      case "$MANAGED_TARGET" in /*) ;; *) echo "invalid AgentBox managed target" >&2; exit 1 ;; esac
      case "$MANAGED_TARGET" in *"/../"*|*/..|*"$MANAGED_NEWLINE"*) echo "unsafe AgentBox managed target" >&2; exit 1 ;; esac
      if printf '%s' "$MANAGED_TARGET" | LC_ALL=C grep -q '[[:cntrl:]]'; then
        echo "unsafe AgentBox managed target" >&2
        exit 1
      fi
      if grep -Fqx "$MANAGED_TARGET" "$MANAGED_SEEN"; then
        echo "duplicate AgentBox managed target" >&2
        exit 1
      fi
      printf "%s\n" "$MANAGED_TARGET" >> "$MANAGED_SEEN"
      MANAGED_PARENT=${MANAGED_TARGET%/*}
      [ -n "$MANAGED_PARENT" ] || MANAGED_PARENT=/
      MANAGED_BASE=${MANAGED_TARGET##*/}
      if ! managed_no_symlink_chain "$MANAGED_PARENT"; then
        echo "AgentBox managed target parent has a symlink ancestor" >&2
        exit 1
      fi
      mkdir -p "$MANAGED_PARENT"
      MANAGED_INDEX=$((MANAGED_INDEX + 1))
      MANAGED_NEXT="$MANAGED_PARENT/.agentbox-next-$MANAGED_BASE-$$-$MANAGED_INDEX"
      MANAGED_BACKUP="$MANAGED_PARENT/.agentbox-backup-$MANAGED_BASE-$$-$MANAGED_INDEX"
      rm -rf "$MANAGED_NEXT" "$MANAGED_BACKUP"
      if [ "$MANAGED_ACTION" = put ]; then
        case "$MANAGED_SOURCE" in /*) ;; *) echo "invalid AgentBox managed source" >&2; exit 1 ;; esac
        case "$MANAGED_SOURCE" in *"$MANAGED_NEWLINE"*) echo "unsafe AgentBox managed source" >&2; exit 1 ;; esac
        if printf '%s' "$MANAGED_SOURCE" | LC_ALL=C grep -q '[[:cntrl:]]'; then
          echo "unsafe AgentBox managed source" >&2
          exit 1
        fi
        if ! managed_no_symlink_chain "$MANAGED_SOURCE"; then
          echo "AgentBox managed source has a symlink ancestor" >&2
          exit 1
        fi
        if [ -d "$MANAGED_SOURCE" ]; then
          mkdir "$MANAGED_NEXT"
          cp -R "$MANAGED_SOURCE/." "$MANAGED_NEXT/"
        elif [ -f "$MANAGED_SOURCE" ]; then
          cp "$MANAGED_SOURCE" "$MANAGED_NEXT"
        else
          echo "AgentBox managed source is unavailable" >&2
          exit 1
        fi
        [ "$MANAGED_MODE" = - ] || chmod "$MANAGED_MODE" "$MANAGED_NEXT"
      else
        MANAGED_NEXT=
      fi
      if [ "$MANAGED_REPLACE" != 1 ] && { [ -e "$MANAGED_TARGET" ] || [ -L "$MANAGED_TARGET" ]; }; then
        echo "AgentBox managed target collides with an unmanaged path" >&2
        exit 1
      fi
      printf "%s\t%s\t%s\t%s\t%s\n" "$MANAGED_ACTION" "$MANAGED_TARGET" "$MANAGED_NEXT" "$MANAGED_BACKUP" "$MANAGED_MODE" >> "$MANAGED_STAGED"
    done < "$MANAGED_OPERATIONS"
    while IFS="$(printf '\''\t'\'')" read -r MANAGED_ACTION MANAGED_TARGET MANAGED_NEXT MANAGED_BACKUP MANAGED_MODE; do
      MANAGED_PARENT=${MANAGED_TARGET%/*}
      [ -n "$MANAGED_PARENT" ] || MANAGED_PARENT=/
      if ! managed_no_symlink_chain "$MANAGED_PARENT"; then
        echo "AgentBox managed target parent changed to a symlink" >&2
        exit 1
      fi
      MANAGED_HAD_OLD=0
      if [ -e "$MANAGED_TARGET" ] || [ -L "$MANAGED_TARGET" ]; then
        mv "$MANAGED_TARGET" "$MANAGED_BACKUP"
        MANAGED_HAD_OLD=1
      fi
      if ! printf "%s\t%s\t%s\n" "$MANAGED_TARGET" "$MANAGED_BACKUP" "$MANAGED_HAD_OLD" >> "$MANAGED_APPLIED"; then
        if [ "$MANAGED_HAD_OLD" = 1 ] && ! mv "$MANAGED_BACKUP" "$MANAGED_TARGET"; then
          echo "AgentBox managed transaction could not restore an unjournaled target" >&2
        fi
        exit 1
      fi
      if [ "$MANAGED_ACTION" = put ]; then
        mv "$MANAGED_NEXT" "$MANAGED_TARGET"
      fi
    done < "$MANAGED_STAGED"
    MANAGED_SUCCESS=true
  ' agentbox /opt/agentbox < "$MANAGED_OPERATIONS"
}

skill_target_for_tool() {
  case "$1" in
    antigravity) printf '%s\n' /root/.gemini/antigravity-cli/skills ;;
    claude-code) printf '%s\n' /root/.claude/skills ;;
    codebuddy) printf '%s\n' /root/.codebuddy/skills ;;
    codex) printf '%s\n' /root/.agents/skills ;;
    copilot-cli) printf '%s\n' /root/.copilot/skills ;;
    deepseek-harness) printf '%s\n' /root/.agents/skills ;;
    deveco) printf '%s\n' /root/.config/deveco/skills ;;
    gemini-cli) printf '%s\n' /root/.gemini/skills ;;
    grok) printf '%s\n' /root/.grok/skills ;;
    kimi) printf '%s\n' /root/.kimi/skills ;;
    omp) printf '%s\n' /root/.omp/agent/skills ;;
    openclaw) printf '%s\n' /root/.openclaw/skills ;;
    opencode) printf '%s\n' /root/.config/opencode/skills ;;
    pi) printf '%s\n' /root/.pi/agent/skills ;;
    qoder-cli) printf '%s\n' /root/.qoder/skills ;;
    qoder-cn) printf '%s\n' /root/.qoder-cn/skills ;;
    qwen-code) printf '%s\n' /root/.qwen/skills ;;
    qwenpaw) printf '%s\n' /root/.qwenpaw/skill_pool ;;
    reasonix) printf '%s\n' /root/.reasonix/skills ;;
    trae-cli) printf '%s\n' /root/.traecli/skills ;;
    cursor) printf '%s\n' /root/.cursor/skills ;;
    *) return 1 ;;
  esac
}

all_skill_targets() {
  for SKILL_TOOL in antigravity claude-code codebuddy codex copilot-cli deepseek-harness deveco gemini-cli grok kimi omp openclaw opencode pi qoder-cli qoder-cn qwen-code qwenpaw reasonix trae-cli cursor; do
    skill_target_for_tool "$SKILL_TOOL" || return 1
  done
  # Legacy Workers copied Codex skills here. Keep it only in the migration
  # cleanup set; current Codex discovers user skills from ~/.agents/skills.
  printf '%s\n' /root/.codex/skills
}

configure_variables() (
  VARIABLE_CONTAINER=$1
  VARIABLE_JOB_FILE=$2
  VARIABLE_SNAPSHOT=${3:-}
  VARIABLE_STAGE=
  VARIABLE_TEMP_DIR=$(mktemp -d) || exit 1
  chmod 700 "$VARIABLE_TEMP_DIR" || { rm -rf "$VARIABLE_TEMP_DIR"; exit 1; }
  VARIABLE_RESOLVED=$VARIABLE_TEMP_DIR/resolved.json
  VARIABLE_ENV=$VARIABLE_TEMP_DIR/agentbox.env
  VARIABLE_VALUE=$VARIABLE_TEMP_DIR/value
  VARIABLE_LIST=$VARIABLE_TEMP_DIR/list
  VARIABLE_DIRECT_KEYS=$VARIABLE_TEMP_DIR/direct-keys
  VARIABLE_PREVIOUS=$VARIABLE_TEMP_DIR/previous.json
  VARIABLE_DESIRED=$VARIABLE_TEMP_DIR/desired.json
  VARIABLE_OPERATIONS=$VARIABLE_TEMP_DIR/operations
  variable_cleanup() {
    rm -rf "$VARIABLE_TEMP_DIR"
    [ -z "$VARIABLE_STAGE" ] || docker exec "$VARIABLE_CONTAINER" rm -rf "$VARIABLE_STAGE" >/dev/null 2>&1 || true
  }
  trap variable_cleanup EXIT
  if [ -n "$VARIABLE_SNAPSHOT" ]; then
    cp "$VARIABLE_SNAPSHOT" "$VARIABLE_RESOLVED" || exit 1
  else
    resolve_worker_variables "$VARIABLE_JOB_FILE" "$VARIABLE_RESOLVED" || exit 1
  fi
  if ! jq -e 'type == "object" and all(to_entries[];
    (.key | test("^[A-Za-z_][A-Za-z0-9_]*$")) and (.value | type) == "string")' "$VARIABLE_RESOLVED" >/dev/null; then
    echo 'AgentBox resolved Variable snapshot is invalid' >&2
    exit 1
  fi
  if ! docker exec "$VARIABLE_CONTAINER" sh -c 'test ! -f "$1" || cat "$1"' agentbox /opt/agentbox/secrets/agentbox.env > "$VARIABLE_ENV"; then
    echo 'AgentBox could not read the sandbox environment file' >&2
    exit 1
  fi
  if read_container_file "$VARIABLE_CONTAINER" /opt/agentbox/variables.manifest.json "$VARIABLE_PREVIOUS" && [ -s "$VARIABLE_PREVIOUS" ]; then
    if ! jq -e 'type == "object" and .version == 1 and (.keys | type == "array") and all(.keys[]; type == "string" and test("^[A-Za-z_][A-Za-z0-9_]*$"))' "$VARIABLE_PREVIOUS" >/dev/null; then
      echo 'AgentBox Variable managed manifest is invalid' >&2
      exit 1
    fi
  else
    printf '{"version":1,"keys":[]}\n' > "$VARIABLE_PREVIOUS" || exit 1
  fi
  if ! jq -r '
    [(.job.payload.environmentVariables // [])[] |
      if (.name | type) == "string" and (.name | test("^[A-Za-z_][A-Za-z0-9_]*$"))
      then .name else error("invalid direct environment variable name") end] |
    if length == (unique | length) then unique[] else error("duplicate direct environment variable name") end
  ' "$VARIABLE_JOB_FILE" > "$VARIABLE_DIRECT_KEYS"; then
    echo 'AgentBox direct environment variable definitions are invalid' >&2
    exit 1
  fi
  if ! jq -e --slurpfile resolved "$VARIABLE_RESOLVED" --rawfile direct "$VARIABLE_DIRECT_KEYS" '
    ($direct | split("\n") | map(select(length > 0))) as $directKeys |
    all(($resolved[0] | keys)[]; . as $key | ($directKeys | index($key)) == null)
  ' "$VARIABLE_JOB_FILE" >/dev/null; then
    echo 'AgentBox Variable key conflicts with a direct environment variable' >&2
    exit 1
  fi
  jq -r '.keys[]' "$VARIABLE_PREVIOUS" > "$VARIABLE_LIST" || exit 1
  while IFS= read -r VARIABLE_KEY; do
    [ -n "$VARIABLE_KEY" ] || continue
    if ! jq -e --arg key "$VARIABLE_KEY" 'has($key)' "$VARIABLE_RESOLVED" >/dev/null &&
       grep -Fxq "$VARIABLE_KEY" "$VARIABLE_DIRECT_KEYS"; then
      continue
    fi
    remove_env "$VARIABLE_ENV" "$VARIABLE_KEY" || exit 1
  done < "$VARIABLE_LIST"
  if ! jq -r 'to_entries[] | [.key, (.value | @base64)] | @tsv' "$VARIABLE_RESOLVED" > "$VARIABLE_LIST"; then
    exit 1
  fi
  while IFS="$(printf '\t')" read -r VARIABLE_KEY VARIABLE_BASE64; do
    [ -n "$VARIABLE_KEY" ] || continue
    if ! printf '%s' "$VARIABLE_BASE64" | base64 -d > "$VARIABLE_VALUE"; then
      exit 1
    fi
    remove_env "$VARIABLE_ENV" "$VARIABLE_KEY" || exit 1
    append_env_file "$VARIABLE_ENV" "$VARIABLE_KEY" "$VARIABLE_VALUE" || exit 1
  done < "$VARIABLE_LIST"
  if ! sh -n "$VARIABLE_ENV"; then
    echo 'AgentBox resolved variables produced an invalid environment file' >&2
    exit 1
  fi
  jq '{version: 1, keys: (keys | sort)}' "$VARIABLE_RESOLVED" > "$VARIABLE_DESIRED" || exit 1
  if ! VARIABLE_STAGE=$(docker exec "$VARIABLE_CONTAINER" sh -c 'mkdir -p /opt/agentbox; mktemp -d /opt/agentbox/.variables-stage.XXXXXX'); then
    echo 'AgentBox could not create the Variable staging directory' >&2
    exit 1
  fi
  docker exec -i "$VARIABLE_CONTAINER" sh -c 'umask 077; cat > "$1"' agentbox "$VARIABLE_STAGE/agentbox.env" < "$VARIABLE_ENV" || exit 1
  docker exec -i "$VARIABLE_CONTAINER" sh -c 'umask 077; cat > "$1"' agentbox "$VARIABLE_STAGE/manifest.json" < "$VARIABLE_DESIRED" || exit 1
  {
    printf 'put\t/opt/agentbox/secrets/agentbox.env\t%s/agentbox.env\t1\t600\n' "$VARIABLE_STAGE"
    printf 'put\t/opt/agentbox/variables.manifest.json\t%s/manifest.json\t1\t600\n' "$VARIABLE_STAGE"
  } > "$VARIABLE_OPERATIONS" || exit 1
  managed_paths_reconcile "$VARIABLE_CONTAINER" "$VARIABLE_OPERATIONS" || exit 1
)

configure_skills() (
  SKILL_CONTAINER=$1
  SKILL_JOB_FILE=$2
  SKILL_STAGE=
  SKILL_TEMP_FILES=
  skill_cleanup() {
    [ -z "$SKILL_TEMP_FILES" ] || rm -f $SKILL_TEMP_FILES
    [ -z "$SKILL_STAGE" ] || docker exec "$SKILL_CONTAINER" rm -rf "$SKILL_STAGE" >/dev/null 2>&1 || true
  }
  trap skill_cleanup EXIT
  SKILL_LIST=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_LIST"
  SKILL_FILE=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_FILE"
  SKILL_ATTACHMENTS=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_ATTACHMENTS"
  SKILL_PREVIOUS=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_PREVIOUS"
  SKILL_DESIRED=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_DESIRED"
  SKILL_TARGETS=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_TARGETS"
  SKILL_TOOLS=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_TOOLS"
  SKILL_ENCODED=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_ENCODED"
  SKILL_OPERATIONS=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_OPERATIONS"
  chmod 600 $SKILL_TEMP_FILES || exit 1
  if ! jq -c '.job.payload.skills[]?' "$SKILL_JOB_FILE" > "$SKILL_LIST"; then
    echo 'AgentBox Skill definitions are invalid' >&2
    exit 1
  fi
  if ! SKILL_STAGE=$(docker exec "$SKILL_CONTAINER" sh -c 'mkdir -p /opt/agentbox; mktemp -d /opt/agentbox/.skills-stage.XXXXXX'); then
    echo 'AgentBox could not create the Skill staging directory' >&2
    exit 1
  fi
  docker exec "$SKILL_CONTAINER" mkdir -p "$SKILL_STAGE/skills" || exit 1
  while IFS= read -r SKILL; do
    if ! SKILL_ID=$(printf '%s' "$SKILL" | jq -er '
      .id | select(type == "string" and length >= 2 and length <= 64 and
        test("^[a-z0-9]+(-[a-z0-9]+)*$"))'); then
      echo 'AgentBox Skill id is invalid' >&2
      exit 1
    fi
    if ! printf '%s' "$SKILL" | jq -e '
      def valid_skill_path:
        type == "string" and utf8bytelength <= 240 and . != "." and
        (startswith("/") | not) and (contains("\\") | not) and (contains(":") | not) and
        (test("[\\x00-\\x1F\\x7F-\\x9F]") | not) and
        (split("/") | length > 0 and all(.[]; length > 0 and . != "." and . != ".."));
      (.spec.files // []) as $files |
      ($files | type) == "array" and ($files | length) < 128 and
      all($files[]; type == "object" and (.path | valid_skill_path) and
        (.path | ascii_downcase) != "skill.md" and (.content | type) == "string") and
      ((["skill.md"] + [$files[].path | ascii_downcase]) as $paths |
        ($paths | length) == ($paths | unique | length) and
        all($paths[]; . as $candidate |
          all($paths[]; . == $candidate or (startswith($candidate + "/") | not))))
    ' >/dev/null; then
      echo "AgentBox Skill attachment paths are invalid: $SKILL_ID" >&2
      exit 1
    fi
    SKILL_NAME=$(printf '%s' "$SKILL" | jq -r '.name | @json') || exit 1
    SKILL_DESCRIPTION=$(printf '%s' "$SKILL" | jq -r '.description | @json') || exit 1
    SKILL_INSTRUCTIONS=$(printf '%s' "$SKILL" | jq -r '.spec.instructions // empty') || exit 1
    [ -n "$SKILL_INSTRUCTIONS" ] || SKILL_INSTRUCTIONS=$(printf '%s' "$SKILL" | jq -r '.description') || exit 1
    if printf '%s' "$SKILL" | jq -e '(.spec.instructions // "") | test("^---\\r?\\n")' >/dev/null; then
      printf '%s' "$SKILL" | jq -j '.spec.instructions' > "$SKILL_FILE" || exit 1
    else
      printf '%s\n' '---' "name: $SKILL_NAME" "description: $SKILL_DESCRIPTION" '---' '' "$SKILL_INSTRUCTIONS" > "$SKILL_FILE" || exit 1
    fi
    docker exec "$SKILL_CONTAINER" mkdir -p "$SKILL_STAGE/skills/$SKILL_ID" || exit 1
    docker exec -i "$SKILL_CONTAINER" sh -c 'umask 077; cat > "$1"' agentbox "$SKILL_STAGE/skills/$SKILL_ID/SKILL.md" < "$SKILL_FILE" || exit 1
    printf '%s' "$SKILL" | jq -c '.spec.files[]?' > "$SKILL_ATTACHMENTS" || exit 1
    while IFS= read -r SKILL_ATTACHMENT; do
      SKILL_PATH=$(printf '%s' "$SKILL_ATTACHMENT" | jq -er '.path') || exit 1
      SKILL_PARENT=$(dirname -- "$SKILL_PATH") || exit 1
      docker exec "$SKILL_CONTAINER" mkdir -p "$SKILL_STAGE/skills/$SKILL_ID/$SKILL_PARENT" || exit 1
      printf '%s' "$SKILL_ATTACHMENT" | jq -er '.content' > "$SKILL_ENCODED" || exit 1
      base64 -d < "$SKILL_ENCODED" > "$SKILL_FILE" || exit 1
      docker exec -i "$SKILL_CONTAINER" sh -c 'umask 077; cat > "$1"' agentbox "$SKILL_STAGE/skills/$SKILL_ID/$SKILL_PATH" < "$SKILL_FILE" || exit 1
      if printf '%s' "$SKILL_ATTACHMENT" | jq -e '.executable == true' >/dev/null; then
        docker exec "$SKILL_CONTAINER" chmod 755 "$SKILL_STAGE/skills/$SKILL_ID/$SKILL_PATH" || exit 1
      fi
    done < "$SKILL_ATTACHMENTS"
  done < "$SKILL_LIST"
  : > "$SKILL_TARGETS" || exit 1
  if ! jq -r '.job.payload.agentTools[]?' "$SKILL_JOB_FILE" > "$SKILL_FILE" ||
     ! sort -u "$SKILL_FILE" > "$SKILL_TOOLS"; then
    echo 'AgentBox Agent tool selection is invalid for Skill sync' >&2
    exit 1
  fi
  while IFS= read -r SKILL_TOOL; do
    if ! skill_target_for_tool "$SKILL_TOOL" >> "$SKILL_TARGETS"; then
      echo "AgentBox warning: Skill sync is unsupported for Agent tool $SKILL_TOOL" >&2
    fi
  done < "$SKILL_TOOLS"
  sort -u -o "$SKILL_TARGETS" "$SKILL_TARGETS" || exit 1
  if docker exec "$SKILL_CONTAINER" sh -c 'test -f /opt/agentbox/skills.manifest.json && cat /opt/agentbox/skills.manifest.json' > "$SKILL_PREVIOUS" 2>/dev/null; then
    if ! jq -e 'type == "object" and .version == 1 and (.ids | type == "array") and (.targets | type == "array") and all(.ids[]; type == "string" and test("^[a-z0-9]+(-[a-z0-9]+)*$")) and all(.targets[]; type == "string")' "$SKILL_PREVIOUS" >/dev/null; then
      echo 'AgentBox Skill managed manifest is invalid' >&2
      exit 1
    fi
  else
    SKILL_LEGACY_IDS=$(mktemp) || exit 1; SKILL_TEMP_FILES="$SKILL_TEMP_FILES $SKILL_LEGACY_IDS"; chmod 600 "$SKILL_LEGACY_IDS" || exit 1
    docker exec "$SKILL_CONTAINER" sh -c 'if [ -d /opt/agentbox/skills ]; then for entry in /opt/agentbox/skills/*; do if [ -d "$entry" ]; then basename "$entry"; fi; done; fi' > "$SKILL_LEGACY_IDS" || exit 1
    if ! all_skill_targets > "$SKILL_FILE"; then exit 1; fi
    if ! jq -n --rawfile ids "$SKILL_LEGACY_IDS" --rawfile targets "$SKILL_FILE" '{version: 1, ids: ($ids | split("\n") | map(select(length > 0))), targets: (if ($ids | length) == 0 then [] else ($targets | split("\n") | map(select(length > 0))) end)}' > "$SKILL_PREVIOUS"; then
      exit 1
    fi
  fi
  if ! jq -n --argjson ids "$(jq -c '[.job.payload.skills[]?.id] | unique' "$SKILL_JOB_FILE")" --rawfile targets "$SKILL_TARGETS" '{version: 1, ids: $ids, targets: ($targets | split("\n") | map(select(length > 0)) | unique)}' > "$SKILL_DESIRED"; then
    exit 1
  fi
  docker exec -i "$SKILL_CONTAINER" sh -c 'umask 077; cat > "$1"' agentbox "$SKILL_STAGE/manifest.json" < "$SKILL_DESIRED" || exit 1
  if ! jq -nr --slurpfile previous "$SKILL_PREVIOUS" --slurpfile desired "$SKILL_DESIRED" --arg stage "$SKILL_STAGE" '
    (($previous[0].targets + $desired[0].targets) | unique)[] as $target |
    (($previous[0].ids + $desired[0].ids) | unique)[] as $id |
    (($previous[0].targets | index($target)) != null and ($previous[0].ids | index($id)) != null) as $was_managed |
    (($desired[0].targets | index($target)) != null and ($desired[0].ids | index($id)) != null) as $wanted |
    if $wanted then ["put", ($target + "/" + $id), ($stage + "/skills/" + $id), (if $was_managed then "1" else "0" end), "-"] | @tsv
    elif $was_managed then ["delete", ($target + "/" + $id), "-", "1", "-"] | @tsv
    else empty end
  ' > "$SKILL_OPERATIONS"; then
    exit 1
  fi
  printf 'put\t/opt/agentbox/skills\t%s/skills\t1\t-\n' "$SKILL_STAGE" >> "$SKILL_OPERATIONS" || exit 1
  printf 'put\t/opt/agentbox/skills.manifest.json\t%s/manifest.json\t1\t600\n' "$SKILL_STAGE" >> "$SKILL_OPERATIONS" || exit 1
  managed_paths_reconcile "$SKILL_CONTAINER" "$SKILL_OPERATIONS" || exit 1
)

read_container_file() {
  READ_CONTAINER=$1
  READ_PATH=$2
  READ_OUTPUT=$3
  docker exec "$READ_CONTAINER" sh -c '
    if [ -f "$1" ]; then cat "$1"; elif [ -e "$1" ]; then exit 1; fi
  ' agentbox "$READ_PATH" > "$READ_OUTPUT"
}

configure_mcp_servers() (
  MCP_CONTAINER=$1
  MCP_JOB_FILE=$2
  MCP_VARIABLE_SNAPSHOT=${3:-}
  MCP_STAGE=
  MCP_TEMP_DIR=$(mktemp -d) || exit 1
  chmod 700 "$MCP_TEMP_DIR" || { rm -rf "$MCP_TEMP_DIR"; exit 1; }
  mcp_cleanup() {
    rm -rf "$MCP_TEMP_DIR"
    [ -z "$MCP_STAGE" ] || docker exec "$MCP_CONTAINER" rm -rf "$MCP_STAGE" >/dev/null 2>&1 || true
  }
  trap mcp_cleanup EXIT
  MCP_NORMALIZED=$MCP_TEMP_DIR/normalized.json
  MCP_RESOLVED=$MCP_TEMP_DIR/resolved.json
  MCP_PREVIOUS=$MCP_TEMP_DIR/previous.json
  MCP_DESIRED=$MCP_TEMP_DIR/desired.json
  MCP_ADAPTERS=$MCP_TEMP_DIR/adapters
  MCP_TOOLS=$MCP_TEMP_DIR/tools
  MCP_IDS=$MCP_TEMP_DIR/ids
  MCP_OPERATIONS=$MCP_TEMP_DIR/operations
  MCP_GENERIC=$MCP_TEMP_DIR/mcp.json
  MCP_LEGACY_MANIFEST=$MCP_TEMP_DIR/legacy-manifest.json
  MCP_CURRENT=$MCP_TEMP_DIR/current
  MCP_NEXT=$MCP_TEMP_DIR/next
  MCP_AUX=$MCP_TEMP_DIR/aux
  MCP_COUNT=$(jq -er '(.job.payload.mcpServers // []) | if type == "array" then length else error("MCP server list must be an array") end' "$MCP_JOB_FILE") || exit 1
  if [ -n "$MCP_VARIABLE_SNAPSHOT" ]; then
    cp "$MCP_VARIABLE_SNAPSHOT" "$MCP_RESOLVED" || exit 1
  else
    resolve_worker_variables "$MCP_JOB_FILE" "$MCP_RESOLVED" || exit 1
  fi
  if ! jq -e 'type == "object" and all(to_entries[];
    (.key | test("^[A-Za-z_][A-Za-z0-9_]*$")) and (.value | type) == "string")' "$MCP_RESOLVED" >/dev/null; then
    echo 'AgentBox resolved Variable snapshot is invalid for MCP configuration' >&2
    exit 1
  fi
  if ! jq -er --slurpfile resolved "$MCP_RESOLVED" '
    def trim: gsub("^[[:space:]]+|[[:space:]]+$"; "");
    def arguments:
      (.spec.arguments // .spec.args // []) |
      if type == "array" then
        if all(.[]; type == "string") then . else error("MCP arguments must contain only strings") end
      elif type == "string" then [scan("[^[:space:]]+")]
      else error("MCP arguments must be an array or legacy string") end;
    def headers:
      (.spec.headers // []) |
      if type == "array" then map(
        if type == "object" and (.name | type) == "string" and (.valueFrom | type) == "string"
        then {name: (.name | trim), valueFrom: (.valueFrom | trim)}
        else error("MCP header entries must contain name and valueFrom") end)
      elif type == "string" then
        split("\n") | map(rtrimstr("\r") | trim | select(length > 0) |
          (index("=") as $separator |
           if $separator == null or $separator == 0 then error("legacy MCP header is invalid")
           else {name: (.[0:$separator] | trim), valueFrom: (.[$separator + 1:] | trim)} end))
      else error("MCP headers must be an array or legacy string") end;
    .job.payload as $payload |
    [($payload.mcpServers // [])[] |
      . as $server |
      if (.id | type) != "string" or (.id | test("^[a-z0-9]+(-[a-z0-9]+)*$") | not)
      then error("MCP server id is invalid") else . end |
      (arguments) as $arguments |
      (headers) as $headers |
      if (($headers | map(.name | ascii_downcase) | length) != ($headers | map(.name | ascii_downcase) | unique | length))
      then error("MCP header names must be unique") else . end |
      [$headers[] |
        . as $header |
        if (.name | test("^[!#$%&'\''*+.^_`|~0-9A-Za-z-]+$") | not) then error("MCP header name is invalid") else . end |
        (.valueFrom | capture("^(?<scheme>env|secret)://(?<key>[A-Za-z_][A-Za-z0-9_]*)$")) as $reference |
        [$payload.variables[]? | select(.spec.key == $reference.key)] as $variables |
        if ($variables | length) != 1 then error("MCP header must reference one selected Variable") else . end |
        $variables[0] as $variable |
        ($variable.spec.reference | capture("^(?<scheme>env|secret)://(?<source>[A-Za-z_][A-Za-z0-9_]*)$")) as $source |
        if (($reference.scheme == "env" and $variable.spec.mode != "value-ref") or
            ($reference.scheme == "secret" and $variable.spec.mode != "secret-ref") or
            $reference.scheme != $source.scheme)
        then error("MCP header reference does not match selected Variable mode") else . end |
        if ($resolved[0] | has($reference.key) | not) then error("MCP header Variable could not be resolved")
        elif ($resolved[0][$reference.key] | test("[\\x00-\\x1F\\x7F]")) then error("MCP header Variable contains a control character")
        else . end |
        {name: $header.name, valueFrom: $header.valueFrom, envKey: $reference.key, value: $resolved[0][$reference.key]}
      ] as $normalized_headers |
      if .spec.transport == "stdio" then
        if (.spec.command | type) != "string" or (.spec.command | length) == 0 then error("stdio MCP command is required")
        elif ($normalized_headers | length) > 0 then error("stdio MCP cannot define HTTP headers")
        else {id: .id, name: (.name // .id), transport: "stdio", command: .spec.command,
              arguments: $arguments,
              cwd: (if ((.spec.cwd // "") | type) == "string" and ((.spec.cwd // "") | length) > 0 then .spec.cwd
                    elif (($payload.workdir // "") | type) == "string" and (($payload.workdir // "") | length) > 0 then $payload.workdir
                    else "/workspace" end), headers: []} end
      elif .spec.transport == "http" then
        if (.spec.url | type) != "string" or (.spec.url | length) == 0 then error("HTTP MCP URL is required")
        else {id: .id, name: (.name // .id), transport: "http", url: .spec.url,
              headers: $normalized_headers} end
      else error("unsupported MCP transport") end
    ]
  ' "$MCP_JOB_FILE" > "$MCP_NORMALIZED"; then
    echo 'AgentBox MCP configuration is invalid' >&2
    exit 1
  fi
  : > "$MCP_ADAPTERS" || exit 1
  if ! jq -r '.job.payload.agentTools[]?' "$MCP_JOB_FILE" > "$MCP_AUX" ||
     ! sort -u "$MCP_AUX" > "$MCP_TOOLS"; then
    echo 'AgentBox Agent tool selection is invalid for MCP sync' >&2
    exit 1
  fi
  while IFS= read -r MCP_TOOL; do
    case "$MCP_TOOL" in
      deepseek-harness) printf '%s\n' dsh >> "$MCP_ADAPTERS" ;;
      codex) printf '%s\n' codex >> "$MCP_ADAPTERS" ;;
      claude-code) printf '%s\n' claude >> "$MCP_ADAPTERS" ;;
      gemini-cli) printf '%s\n' gemini >> "$MCP_ADAPTERS" ;;
      opencode) printf '%s\n' opencode >> "$MCP_ADAPTERS" ;;
      '') ;;
      *) [ "$MCP_COUNT" -eq 0 ] || echo "AgentBox warning: MCP sync is unsupported for Agent tool $MCP_TOOL" >&2 ;;
    esac
  done < "$MCP_TOOLS"
  sort -u -o "$MCP_ADAPTERS" "$MCP_ADAPTERS" || exit 1
  if [ "$MCP_COUNT" -gt 0 ] && [ ! -s "$MCP_ADAPTERS" ]; then
    echo 'AgentBox MCP configuration has no supported selected Agent tool' >&2
    exit 1
  fi
  if read_container_file "$MCP_CONTAINER" /opt/agentbox/mcp.manifest.json "$MCP_PREVIOUS" && [ -s "$MCP_PREVIOUS" ]; then
    if ! jq -e 'type == "object" and .version == 1 and (.ids | type == "array") and (.adapters | type == "array") and all(.ids[]; type == "string" and test("^[a-z0-9]+(-[a-z0-9]+)*$")) and all(.adapters[]; . == "dsh" or . == "codex" or . == "claude" or . == "gemini" or . == "opencode")' "$MCP_PREVIOUS" >/dev/null; then
      echo 'AgentBox MCP managed manifest is invalid' >&2
      exit 1
    fi
  else
    if ! read_container_file "$MCP_CONTAINER" /opt/agentbox/mcp.json "$MCP_CURRENT"; then
      echo 'AgentBox could not read the legacy MCP manifest' >&2
      exit 1
    fi
    if [ -s "$MCP_CURRENT" ]; then
      if ! jq -e 'type == "object" and ((.mcpServers // {}) | type == "object")' "$MCP_CURRENT" >/dev/null; then
        echo 'AgentBox legacy MCP manifest is invalid' >&2
        exit 1
      fi
      # Legacy Workers recorded the selected Agent tools in manifest.json and
      # projected every MCP id into each matching client. Recover ownership
      # from that durable provenance; guessing could delete a same-named user
      # entry or leave a removed AgentBox binding active.
      if ! read_container_file "$MCP_CONTAINER" /opt/agentbox/manifest.json "$MCP_LEGACY_MANIFEST" ||
         ! jq -e 'type == "object" and (.agentTools | type) == "array" and
           all(.agentTools[]; type == "string")' "$MCP_LEGACY_MANIFEST" >/dev/null; then
        echo 'AgentBox cannot safely migrate the legacy MCP configuration because its Agent tool manifest is unavailable' >&2
        exit 1
      fi
      if ! jq --slurpfile manifest "$MCP_LEGACY_MANIFEST" '
        {version: 1, ids: ((.mcpServers // {}) | keys), adapters:
          ([$manifest[0].agentTools[] |
            if . == "deepseek-harness" then "dsh"
            elif . == "codex" then "codex"
            elif . == "claude-code" then "claude"
            elif . == "gemini-cli" then "gemini"
            elif . == "opencode" then "opencode"
            else empty end] | unique)}
      ' "$MCP_CURRENT" > "$MCP_PREVIOUS"; then
        echo 'AgentBox could not migrate the legacy MCP ownership manifest' >&2
        exit 1
      fi
    else
      printf '{"version":1,"ids":[],"adapters":[]}\n' > "$MCP_PREVIOUS" || exit 1
    fi
  fi
  if ! jq -n --argjson ids "$(jq -c 'map(.id) | unique' "$MCP_NORMALIZED")" --rawfile adapters "$MCP_ADAPTERS" '{version: 1, ids: $ids, adapters: ($adapters | split("\n") | map(select(length > 0)) | unique)}' > "$MCP_DESIRED"; then
    exit 1
  fi
  if ! jq '{mcpServers: (map({key: .id, value:
      ({transport: .transport} +
       (if .transport == "stdio" then {command: .command, arguments: .arguments, cwd: .cwd}
        else {url: .url, headers: [.headers[] | {name: .name, valueFrom: .valueFrom}]} end))}) | from_entries)}' "$MCP_NORMALIZED" > "$MCP_GENERIC"; then
    exit 1
  fi
  if ! MCP_STAGE=$(docker exec "$MCP_CONTAINER" sh -c 'mkdir -p /opt/agentbox; mktemp -d /opt/agentbox/.mcp-stage.XXXXXX'); then
    echo 'AgentBox could not create the MCP staging directory' >&2
    exit 1
  fi
  mcp_stage_file() {
    MCP_STAGE_NAME=$1
    MCP_STAGE_SOURCE=$2
    docker exec -i "$MCP_CONTAINER" sh -c 'umask 077; cat > "$1"' agentbox "$MCP_STAGE/$MCP_STAGE_NAME" < "$MCP_STAGE_SOURCE"
  }
  mcp_stage_file mcp.json "$MCP_GENERIC" || exit 1
  mcp_stage_file manifest.json "$MCP_DESIRED" || exit 1
  : > "$MCP_OPERATIONS" || exit 1
  printf 'put\t/opt/agentbox/mcp.json\t%s/mcp.json\t1\t600\n' "$MCP_STAGE" >> "$MCP_OPERATIONS" || exit 1

  MCP_DSH_ENABLED=false
  grep -Fxq dsh "$MCP_ADAPTERS" && MCP_DSH_ENABLED=true
  if [ "$MCP_DSH_ENABLED" = true ] && [ "$MCP_COUNT" -gt 0 ]; then
    if ! jq -er '
      def dsh_name:
        (gsub("[^A-Za-z0-9_-]"; "_") | if length == 0 then "server" else . end) as $raw |
        if ($raw | length) <= 28 then "abx_" + $raw else "abx_" + $raw[:13] + "_" + $raw[-14:] end;
      [.[] | . + {dshName: (.id | dsh_name)}] as $servers |
      if (($servers | group_by(.dshName) | map(select(length > 1)) | length) > 0)
      then error("DSH MCP server names collide after normalization") else $servers[] end |
      "- insert:\n" +
      "  - id: " + (("agentbox-mcp-" + .dshName) | @json) + "\n" +
      "    name: \"@deepseek-ai/dsh-mcp-client\"\n" +
      "    config:\n" +
      "      serverName: " + (.dshName | @json) + "\n" +
      (if .transport == "stdio" then
        "      transport: stdio\n" +
        "      command: " + (.command | @json) + "\n" +
        "      args: " + (.arguments | tojson) + "\n" +
        "      env: {}\n" +
        "      cwd: " + (.cwd | @json)
      else
        "      transport: streamable-http\n" +
        "      url: " + (.url | @json) + "\n" +
        "      headers: " + ((.headers | map({key: .name, value: .value}) | from_entries) | tojson)
      end)
    ' "$MCP_NORMALIZED" > "$MCP_NEXT"; then
      echo 'DSH MCP configuration is invalid' >&2
      exit 1
    fi
    mcp_stage_file dsh-mcp.patch.yml "$MCP_NEXT" || exit 1
    printf 'put\t/opt/agentbox/dsh-mcp.patch.yml\t%s/dsh-mcp.patch.yml\t1\t600\n' "$MCP_STAGE" >> "$MCP_OPERATIONS" || exit 1
  else
    printf 'delete\t/opt/agentbox/dsh-mcp.patch.yml\t-\t1\t-\n' >> "$MCP_OPERATIONS" || exit 1
  fi

  MCP_PREVIOUS_IDS=$(jq -r '.ids | join(",")' "$MCP_PREVIOUS") || exit 1
  MCP_CODEX_ENABLED=false
  grep -Fxq codex "$MCP_ADAPTERS" && MCP_CODEX_ENABLED=true
  MCP_CODEX_WAS_ENABLED=false
  jq -e '.adapters | index("codex") != null' "$MCP_PREVIOUS" >/dev/null && MCP_CODEX_WAS_ENABLED=true
  if [ "$MCP_CODEX_ENABLED" = true ] || [ "$MCP_CODEX_WAS_ENABLED" = true ]; then
    MCP_CODEX_OLD_IDS=
    [ "$MCP_CODEX_WAS_ENABLED" != true ] || MCP_CODEX_OLD_IDS=$MCP_PREVIOUS_IDS
    read_container_file "$MCP_CONTAINER" /root/.codex/config.toml "$MCP_CURRENT" || exit 1
    if ! awk -v managed_ids="$MCP_CODEX_OLD_IDS" '
      BEGIN { split(managed_ids, values, ","); for (i in values) managed[values[i]] = 1; skip = 0 }
      $0 == "# BEGIN AGENTBOX MANAGED MCP" { next }
      $0 == "# END AGENTBOX MANAGED MCP" { next }
      /^[[:space:]]*\[[[:space:]]*mcp_servers[[:space:]]*\.[[:space:]]*"?[a-z0-9-]+"?[[:space:]]*\][[:space:]]*(#.*)?$/ {
        id = $0
        sub(/#.*/, "", id)
        gsub(/[[:space:]\[\]"]/, "", id)
        sub(/^mcp_servers\./, "", id)
        skip = (id in managed); if (skip) next
      }
      /^[[:space:]]*\[/ { skip = 0 }
      !skip { print }
    ' "$MCP_CURRENT" > "$MCP_NEXT"; then
      exit 1
    fi
    if [ "$MCP_CODEX_ENABLED" = true ] && [ "$MCP_COUNT" -gt 0 ]; then
      jq -r '.[].id' "$MCP_NORMALIZED" > "$MCP_IDS" || exit 1
      while IFS= read -r MCP_ID; do
        [ -n "$MCP_ID" ] || continue
        case ",$MCP_CODEX_OLD_IDS," in
          *",$MCP_ID,"*) ;;
          *)
            if grep -Eq "^[[:space:]]*\\[[[:space:]]*mcp_servers[[:space:]]*\\.[[:space:]]*\"?$MCP_ID\"?[[:space:]]*\\][[:space:]]*(#.*)?$" "$MCP_CURRENT"; then
              echo "AgentBox MCP id collides with an unmanaged Codex server: $MCP_ID" >&2
              exit 1
            fi
            ;;
        esac
      done < "$MCP_IDS"
      printf '\n# BEGIN AGENTBOX MANAGED MCP\n' >> "$MCP_NEXT" || exit 1
      if ! jq -r '.[] |
        "[mcp_servers." + .id + "]\n" +
        (if .transport == "stdio" then
          "command = " + (.command | @json) + "\n" +
          "args = " + (.arguments | tojson) + "\n" +
          (if (.cwd // "") == "" then "" else "cwd = " + (.cwd | @json) + "\n" end)
        else
          "url = " + (.url | @json) + "\n" +
          (if (.headers | length) == 0 then "" else
            "env_http_headers = { " + ([.headers[] | (.name | @json) + " = " + (.envKey | @json)] | join(", ")) + " }\n" end)
        end)' "$MCP_NORMALIZED" >> "$MCP_NEXT"; then
        exit 1
      fi
      printf '# END AGENTBOX MANAGED MCP\n' >> "$MCP_NEXT" || exit 1
    fi
    mcp_stage_file codex-config.toml "$MCP_NEXT" || exit 1
    printf 'put\t/root/.codex/config.toml\t%s/codex-config.toml\t1\t600\n' "$MCP_STAGE" >> "$MCP_OPERATIONS" || exit 1
  fi

  MCP_CLAUDE_ENABLED=false
  grep -Fxq claude "$MCP_ADAPTERS" && MCP_CLAUDE_ENABLED=true
  MCP_CLAUDE_WAS_ENABLED=false
  jq -e '.adapters | index("claude") != null' "$MCP_PREVIOUS" >/dev/null && MCP_CLAUDE_WAS_ENABLED=true
  if [ "$MCP_CLAUDE_ENABLED" = true ] || [ "$MCP_CLAUDE_WAS_ENABLED" = true ]; then
    read_container_file "$MCP_CONTAINER" /root/.claude.json "$MCP_CURRENT" || exit 1
    [ -s "$MCP_CURRENT" ] || printf '{}\n' > "$MCP_CURRENT"
    if ! jq --slurpfile previous "$MCP_PREVIOUS" --slurpfile desired "$MCP_NORMALIZED" --argjson enabled "$MCP_CLAUDE_ENABLED" --arg adapter claude '
      if type != "object" or ((.mcpServers // {}) | type) != "object" then error("Claude MCP config must be an object") else . end |
      . as $config |
      ($previous[0] | if (.adapters | index($adapter)) != null then .ids else [] end) as $old |
      ($desired[0] | map(.id)) as $wanted |
      if any($wanted[]; . as $id | (($old | index($id)) == null and (($config.mcpServers // {}) | has($id)))) then error("Claude MCP id collides with unmanaged config") else . end |
      reduce $old[] as $id (. ; del(.mcpServers[$id])) |
      if $enabled then .mcpServers = ((.mcpServers // {}) + ($desired[0] | map({key: .id, value:
        (if .transport == "stdio" then
          {type: "stdio", command: "sh",
           args: (["-c", "cd -- \"$1\" && shift && exec \"$@\"", "agentbox-mcp", .cwd, .command] + .arguments)}
         else ({type: "http", url: .url} + (if (.headers | length) == 0 then {} else
           {headers: (.headers | map({key: .name, value: ("${" + .envKey + "}")}) | from_entries)} end)) end)}) | from_entries)) else . end |
      if ((.mcpServers // {}) | length) == 0 then del(.mcpServers) else . end
    ' "$MCP_CURRENT" > "$MCP_NEXT"; then
      echo 'Claude MCP configuration is invalid' >&2
      exit 1
    fi
    mcp_stage_file claude.json "$MCP_NEXT" || exit 1
    printf 'put\t/root/.claude.json\t%s/claude.json\t1\t600\n' "$MCP_STAGE" >> "$MCP_OPERATIONS" || exit 1
  fi

  MCP_GEMINI_ENABLED=false
  grep -Fxq gemini "$MCP_ADAPTERS" && MCP_GEMINI_ENABLED=true
  MCP_GEMINI_WAS_ENABLED=false
  jq -e '.adapters | index("gemini") != null' "$MCP_PREVIOUS" >/dev/null && MCP_GEMINI_WAS_ENABLED=true
  if [ "$MCP_GEMINI_ENABLED" = true ] || [ "$MCP_GEMINI_WAS_ENABLED" = true ]; then
    read_container_file "$MCP_CONTAINER" /root/.gemini/settings.json "$MCP_CURRENT" || exit 1
    [ -s "$MCP_CURRENT" ] || printf '{}\n' > "$MCP_CURRENT"
    if ! jq --slurpfile previous "$MCP_PREVIOUS" --slurpfile desired "$MCP_NORMALIZED" --argjson enabled "$MCP_GEMINI_ENABLED" --arg adapter gemini '
      if type != "object" or ((.mcpServers // {}) | type) != "object" then error("Gemini config must be an object") else . end |
      . as $config |
      ($previous[0] | if (.adapters | index($adapter)) != null then .ids else [] end) as $old |
      ($desired[0] | map(.id)) as $wanted |
      if any($wanted[]; . as $id | (($old | index($id)) == null and (($config.mcpServers // {}) | has($id)))) then error("Gemini MCP id collides with unmanaged config") else . end |
      reduce $old[] as $id (. ; del(.mcpServers[$id])) |
      if $enabled then .mcpServers = ((.mcpServers // {}) + ($desired[0] | map({key: .id, value:
        (if .transport == "stdio" then
          ({command: .command, args: .arguments} + (if (.cwd // "") == "" then {} else {cwd: .cwd} end))
         else ({httpUrl: .url} + (if (.headers | length) == 0 then {} else
           {headers: (.headers | map({key: .name, value: ("$" + .envKey)}) | from_entries)} end)) end)}) | from_entries)) else . end |
      if ((.mcpServers // {}) | length) == 0 then del(.mcpServers) else . end
    ' "$MCP_CURRENT" > "$MCP_NEXT"; then
      echo 'Gemini MCP configuration is invalid' >&2
      exit 1
    fi
    mcp_stage_file gemini-settings.json "$MCP_NEXT" || exit 1
    printf 'put\t/root/.gemini/settings.json\t%s/gemini-settings.json\t1\t600\n' "$MCP_STAGE" >> "$MCP_OPERATIONS" || exit 1
  fi

  MCP_OPENCODE_ENABLED=false
  grep -Fxq opencode "$MCP_ADAPTERS" && MCP_OPENCODE_ENABLED=true
  MCP_OPENCODE_WAS_ENABLED=false
  jq -e '.adapters | index("opencode") != null' "$MCP_PREVIOUS" >/dev/null && MCP_OPENCODE_WAS_ENABLED=true
  if [ "$MCP_OPENCODE_ENABLED" = true ] || [ "$MCP_OPENCODE_WAS_ENABLED" = true ]; then
    read_container_file "$MCP_CONTAINER" /root/.config/opencode/opencode.json "$MCP_CURRENT" || exit 1
    [ -s "$MCP_CURRENT" ] || printf '{}\n' > "$MCP_CURRENT"
    if ! jq --slurpfile previous "$MCP_PREVIOUS" --slurpfile desired "$MCP_NORMALIZED" --argjson enabled "$MCP_OPENCODE_ENABLED" --arg adapter opencode '
      if type != "object" or ((.mcp // {}) | type) != "object" then error("OpenCode config must be an object") else . end |
      . as $config |
      ($previous[0] | if (.adapters | index($adapter)) != null then .ids else [] end) as $old |
      ($desired[0] | map(.id)) as $wanted |
      if any($wanted[]; . as $id | (($old | index($id)) == null and (($config.mcp // {}) | has($id)))) then error("OpenCode MCP id collides with unmanaged config") else . end |
      reduce $old[] as $id (. ; del(.mcp[$id])) |
      if $enabled then .mcp = ((.mcp // {}) + ($desired[0] | map({key: .id, value:
        (if .transport == "stdio" then {type: "local", command: ([.command] + .arguments), cwd: .cwd, enabled: true}
         else ({type: "remote", url: .url, enabled: true} + (if (.headers | length) == 0 then {} else
           {headers: (.headers | map({key: .name, value: ("{env:" + .envKey + "}")}) | from_entries)} end)) end)}) | from_entries)) else . end |
      if ((.mcp // {}) | length) == 0 then del(.mcp) else . end
    ' "$MCP_CURRENT" > "$MCP_NEXT"; then
      echo 'OpenCode MCP configuration is invalid' >&2
      exit 1
    fi
    mcp_stage_file opencode.json "$MCP_NEXT" || exit 1
    printf 'put\t/root/.config/opencode/opencode.json\t%s/opencode.json\t1\t600\n' "$MCP_STAGE" >> "$MCP_OPERATIONS" || exit 1
  fi
  printf 'put\t/opt/agentbox/mcp.manifest.json\t%s/manifest.json\t1\t600\n' "$MCP_STAGE" >> "$MCP_OPERATIONS" || exit 1
  managed_paths_reconcile "$MCP_CONTAINER" "$MCP_OPERATIONS" || exit 1
)

configure_sandbox_agent_config() (
  CONFIG_CONTAINER=$1
  CONFIG_JOB_FILE=$2
  CONFIG_VARIABLES=${3:-}
  CONFIG_RESOLVE_VARIABLES=false
  if [ -z "$CONFIG_VARIABLES" ]; then
    CONFIG_VARIABLES=$(mktemp) || exit 1
    trap 'rm -f "$CONFIG_VARIABLES"' EXIT
    chmod 600 "$CONFIG_VARIABLES" || exit 1
    CONFIG_RESOLVE_VARIABLES=true
  fi
  CONFIG_IMAGE=$(jq -r '.job.payload.image // empty' "$CONFIG_JOB_FILE") || exit 1
  if [ "$CONFIG_RESOLVE_VARIABLES" = true ] && ! resolve_worker_variables "$CONFIG_JOB_FILE" "$CONFIG_VARIABLES"; then
    echo "stage variables failed: image $CONFIG_IMAGE could not resolve AgentBox variable references" >&2
    exit 1
  fi
  if ! configure_credentials "$CONFIG_CONTAINER" "$CONFIG_JOB_FILE"; then
    echo "stage credentials failed: image $CONFIG_IMAGE could not accept AgentBox configuration" >&2
    exit 1
  fi
  if ! configure_variables "$CONFIG_CONTAINER" "$CONFIG_JOB_FILE" "$CONFIG_VARIABLES"; then
    echo "stage variables failed: image $CONFIG_IMAGE could not accept AgentBox variable references" >&2
    exit 1
  fi
  if ! configure_skills "$CONFIG_CONTAINER" "$CONFIG_JOB_FILE"; then
    echo "stage skills failed: image $CONFIG_IMAGE could not accept AgentBox skills" >&2
    exit 1
  fi
  if ! configure_mcp_servers "$CONFIG_CONTAINER" "$CONFIG_JOB_FILE" "$CONFIG_VARIABLES"; then
    echo "stage mcp failed: image $CONFIG_IMAGE could not accept MCP configuration" >&2
    exit 1
  fi
  if ! install_agent_wrappers "$CONFIG_CONTAINER" "$CONFIG_JOB_FILE"; then
    echo "stage agent-wrappers failed: image $CONFIG_IMAGE could not install Agent wrappers" >&2
    exit 1
  fi
)

install_agent_wrappers() {
  CONTAINER=$1
  JOB_FILE=$2
  docker exec "$CONTAINER" test -f /opt/agentbox/secrets/agentbox.env || return 0
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE")
  printf '%s\n' "$TOOLS" | while IFS= read -r TOOL; do
    COMMAND=$(agent_tool_command "$TOOL") || continue
    docker exec "$CONTAINER" test -x "/usr/bin/$COMMAND" || continue
    WRAPPER=$(mktemp)
    if [ "$TOOL" = deepseek-harness ]; then
      printf '%s\n' \
        '#!/bin/sh' \
        'set -a' \
        '. /opt/agentbox/secrets/agentbox.env' \
        '[ ! -r /opt/agentbox/secrets/proxy.env ] || . /opt/agentbox/secrets/proxy.env' \
        'set +a' \
        'if [ ! -s /opt/agentbox/dsh-mcp.patch.yml ]; then exec /usr/bin/dsh "$@"; fi' \
        'for arg in "$@"; do' \
        '  case "$arg" in plugin|-V|--version|-h|--help|--dump-default-config) exec /usr/bin/dsh "$@" ;; esac' \
        'done' \
        'if [ "${1:-}" = web ]; then' \
        '  shift' \
        '  exec /usr/bin/dsh web --patch /opt/agentbox/dsh-mcp.patch.yml "$@"' \
        'fi' \
        'exec /usr/bin/dsh --patch /opt/agentbox/dsh-mcp.patch.yml "$@"' > "$WRAPPER"
    else
      printf '#!/bin/sh\nset -a\n. /opt/agentbox/secrets/agentbox.env\n[ ! -r /opt/agentbox/secrets/proxy.env ] || . /opt/agentbox/secrets/proxy.env\nset +a\nexec /usr/bin/%s "$@"\n' "$COMMAND" > "$WRAPPER"
    fi
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

prepare_job_variable_snapshot() (
  SNAPSHOT_JOB_FILE=$1
  SNAPSHOT_OUTPUT_FILE=$2
  SNAPSHOT_NEXT=$(mktemp "${SNAPSHOT_OUTPUT_FILE}.XXXXXX") || exit 1
  trap 'rm -f "$SNAPSHOT_NEXT"' EXIT HUP INT TERM
  chmod 600 "$SNAPSHOT_NEXT" || exit 1
  resolve_worker_variables "$SNAPSHOT_JOB_FILE" "$SNAPSHOT_NEXT" || exit 1
  jq -e 'type == "object" and all(to_entries[];
    (.key | test("^[A-Za-z_][A-Za-z0-9_]*$")) and (.value | type) == "string")' \
    "$SNAPSHOT_NEXT" >/dev/null || exit 1
  mv -f "$SNAPSHOT_NEXT" "$SNAPSHOT_OUTPUT_FILE" || exit 1
  trap - EXIT HUP INT TERM
)

redact_job_output() {
  # Sandbox commands and extensions can encode image, workspace, or rotated
  # secrets that are absent from the current job snapshot. Never export
  # free-form command output across the Worker API boundary.
  printf '%s' '[job output withheld: Worker does not export command output]'
}

redact_extension_output() {
  printf '%s' '[extension output withheld: Worker does not export sandbox output]'
}

report_extension_progress() (
  [ -n "${JOB_ID:-}" ] || return 0
  case "$2" in
    installing) EXTENSION_MESSAGE='正在安装沙箱扩展' ;;
    verifying) EXTENSION_MESSAGE='正在验证沙箱扩展' ;;
    succeeded) EXTENSION_MESSAGE='沙箱扩展安装验证完成' ;;
    failed) EXTENSION_MESSAGE='沙箱扩展安装或验证失败' ;;
    *) return 1 ;;
  esac
  EXTENSION_BODY=$(jq -n --argjson leaseGeneration "${LEASE_GENERATION:-0}" \
    --arg extensionId "$1" --arg extensionStatus "$2" --arg extensionOutput "${3:-}" \
    --arg message "$EXTENSION_MESSAGE" \
    '{leaseGeneration:$leaseGeneration,stage:"extensions",message:$message,
      extensionId:$extensionId,extensionStatus:$extensionStatus,extensionOutput:$extensionOutput}') || return 1
  EXTENSION_HTTP_STATUS=$(worker_request /dev/null -X POST \
    "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/progress" \
    -H "Authorization: Bearer $CREDENTIAL" -H 'Content-Type: application/json' \
    --data "$EXTENSION_BODY" 2>/dev/null) || return 1
  [ "$EXTENSION_HTTP_STATUS" = 200 ]
)

run_extension_step() (
  EXTENSION_CONTAINER=$1
  EXTENSION_JOB=$2
  EXTENSION_ID=$3
  EXTENSION_STATUS=$4
  EXTENSION_SCRIPT=$5
  EXTENSION_LOG=$6
  EXTENSION_TIMEOUT=$7
  EXTENSION_WORKDIR=$8
  EXTENSION_VARIABLE_SNAPSHOT=${9:-}
  EXTENSION_CONTROL=$(basename "$(dirname "$EXTENSION_SCRIPT")")-$EXTENSION_STATUS
  EXTENSION_EXIT_FILE=$EXTENSION_SCRIPT.exit
  : >> "$EXTENSION_LOG"
  EXTENSION_REMAINING=$((1048576 - $(wc -c < "$EXTENSION_LOG")))
  [ "$EXTENSION_REMAINING" -gt 0 ] || return 1
  rm -f "$EXTENSION_EXIT_FILE"
  # GNU timeout runs in the guest and kills the step process group, including
  # ordinary child processes. Timing out only the host exec client is unsafe.
  # head bounds collection on the host even if a script produces endless output.
  (if docker exec -i -w "$EXTENSION_WORKDIR" "$EXTENSION_CONTAINER" sh -c '
    set -eu
    if ! command -v timeout >/dev/null 2>&1 ||
       ! timeout --version 2>/dev/null | head -n 1 | grep -q "GNU coreutils"; then
      printf "%s\n" "GNU coreutils timeout is required inside the sandbox" >&2
      exit 125
    fi
    umask 077
    STEP_SCRIPT=$(mktemp)
    STEP_CONTROL="/tmp/agentbox-extension-$2"
    trap '\''rm -f "$STEP_SCRIPT" "$STEP_CONTROL"'\'' EXIT
    cat > "$STEP_SCRIPT"
    set -a
    [ ! -r /opt/agentbox/secrets/agentbox.env ] || . /opt/agentbox/secrets/agentbox.env
    [ ! -r /opt/agentbox/secrets/proxy.env ] || . /opt/agentbox/secrets/proxy.env
    set +a
    timeout --signal=KILL "$1" sh -eu "$STEP_SCRIPT" &
    STEP_PID=$!
    printf "%s\n" "$STEP_PID" > "$STEP_CONTROL"
    wait "$STEP_PID"
  ' agentbox "$EXTENSION_TIMEOUT" "$EXTENSION_CONTROL" < "$EXTENSION_SCRIPT"; then
    printf '0' > "$EXTENSION_EXIT_FILE"
  else
    printf '%s' "$?" > "$EXTENSION_EXIT_FILE"
  fi) 2>&1 | head -c "$EXTENSION_REMAINING" >> "$EXTENSION_LOG" &
  EXTENSION_PID=$!
  while kill -0 "$EXTENSION_PID" 2>/dev/null; do
    [ "$(wc -c < "$EXTENSION_LOG")" -lt 1048576 ] || break
    report_extension_progress "$EXTENSION_ID" "$EXTENSION_STATUS" \
      "$(redact_extension_output "$EXTENSION_JOB" "$EXTENSION_LOG" "$EXTENSION_VARIABLE_SNAPSHOT")" || true
    sleep 2
  done
  if [ "$(wc -c < "$EXTENSION_LOG")" -ge 1048576 ]; then
    docker exec "$EXTENSION_CONTAINER" sh -c '
      STEP_CONTROL="/tmp/agentbox-extension-$1"
      [ -f "$STEP_CONTROL" ] || exit 0
      STEP_PID=$(cat "$STEP_CONTROL")
      case "$STEP_PID" in ""|*[!0-9]*|0|1) exit 1 ;; esac
      kill -KILL -"$STEP_PID" 2>/dev/null || true
    ' agentbox "$EXTENSION_CONTROL" >/dev/null 2>&1 || true
    wait "$EXTENSION_PID" || true
    return 1
  fi
  wait "$EXTENSION_PID" || true
  [ -f "$EXTENSION_EXIT_FILE" ] && [ "$(cat "$EXTENSION_EXIT_FILE")" = 0 ]
)

install_sandbox_extensions() (
  EXTENSION_CONTAINER=$1
  EXTENSION_JOB=$2
  EXTENSION_VARIABLE_SNAPSHOT=${3:-}
  EXTENSION_COUNT=$(jq -er '.job.payload.extensions // [] | length' "$EXTENSION_JOB" 2>/dev/null) || return 1
  [ "$EXTENSION_COUNT" -gt 0 ] || return 0
  EXTENSION_DIR=$(mktemp -d) || return 1
  trap 'rm -rf "$EXTENSION_DIR"' EXIT
  EXTENSION_WORKDIR=$(jq -r '.job.payload.workdir // "/workspace"' "$EXTENSION_JOB")
  EXTENSION_INDEX=0
  while [ "$EXTENSION_INDEX" -lt "$EXTENSION_COUNT" ]; do
    if ! jq -e --argjson index "$EXTENSION_INDEX" '.job.payload.extensions[$index] |
      select((.id | type == "string" and length > 0) and
        (.spec.installScript | type == "string" and length > 0) and
        (.spec.verifyScript | type == "string" and length > 0) and
        ((.spec.timeoutSeconds // 600) | type == "number" and . == floor and . >= 30 and . <= 1800))
    ' "$EXTENSION_JOB" > "$EXTENSION_DIR/definition.json" 2>/dev/null; then
      echo 'stage extensions failed: invalid extension snapshot' >&2
      return 1
    fi
    EXTENSION_ID=$(jq -r '.id' "$EXTENSION_DIR/definition.json")
    EXTENSION_TIMEOUT=$(jq -r '.spec.timeoutSeconds // 600' "$EXTENSION_DIR/definition.json")
    : > "$EXTENSION_DIR/output.log"
    for EXTENSION_STATUS in installing verifying; do
      if [ "$EXTENSION_STATUS" = installing ]; then
        EXTENSION_SCRIPT_KEY=installScript
      else
        EXTENSION_SCRIPT_KEY=verifyScript
      fi
      jq -r --arg key "$EXTENSION_SCRIPT_KEY" '.spec[$key]' "$EXTENSION_DIR/definition.json" > "$EXTENSION_DIR/step.sh" || return 1
      report_extension_progress "$EXTENSION_ID" "$EXTENSION_STATUS" \
        "$(redact_extension_output "$EXTENSION_JOB" "$EXTENSION_DIR/output.log" "$EXTENSION_VARIABLE_SNAPSHOT")" || true
      if ! run_extension_step "$EXTENSION_CONTAINER" "$EXTENSION_JOB" "$EXTENSION_ID" "$EXTENSION_STATUS" \
        "$EXTENSION_DIR/step.sh" "$EXTENSION_DIR/output.log" "$EXTENSION_TIMEOUT" "$EXTENSION_WORKDIR" \
        "$EXTENSION_VARIABLE_SNAPSHOT"; then
        report_extension_progress "$EXTENSION_ID" failed \
          "$(redact_extension_output "$EXTENSION_JOB" "$EXTENSION_DIR/output.log" "$EXTENSION_VARIABLE_SNAPSHOT")" || true
        echo 'stage extensions failed: extension installation or verification failed; inspect redacted extension progress' >&2
        return 1
      fi
    done
    if ! report_extension_progress "$EXTENSION_ID" succeeded \
      "$(redact_extension_output "$EXTENSION_JOB" "$EXTENSION_DIR/output.log" "$EXTENSION_VARIABLE_SNAPSHOT")"; then
      echo 'stage extensions failed: Server did not acknowledge extension verification' >&2
      return 1
    fi
    EXTENSION_INDEX=$((EXTENSION_INDEX + 1))
  done
)

validate_sandbox_network_policy_value() {
  case "$1" in
    none|egress) ;;
    restricted) ;;
    *)
      echo "stage network-policy failed: unsupported network policy: $1" >&2
      return 1
      ;;
  esac
}

validate_sandbox_network_policy() {
  validate_sandbox_network_policy_value "$2" || return 1
  if [ "$2" = restricted ] && [ "$1" != boxlite ]; then
    echo "stage network-policy failed: restricted network is only supported by BoxLite" >&2
    return 1
  fi
}

effective_sandbox_network_policy() {
  if [ -n "$2" ]; then
    printf '%s' "$2"
  elif [ "$1" = boxlite ]; then
    printf restricted
  else
    printf none
  fi
}

enforce_docker_network_policy() {
  TARGET=$1
  EXPECTED_NETWORK=$2
  if ! NETWORK_SNAPSHOT=$(docker inspect -f '{{.HostConfig.NetworkMode}}|{{range $name, $config := .NetworkSettings.Networks}}{{printf "%s," $name}}{{end}}' "$TARGET"); then
    if ! docker stop --time 10 "$TARGET" >/dev/null 2>&1; then
      echo "stage network-policy failed: Docker could not inspect network attachments for $TARGET or stop it" >&2
      return 1
    fi
    echo "stage network-policy failed: Docker could not inspect network attachments for $TARGET; the sandbox was stopped" >&2
    return 1
  fi
  case "$NETWORK_SNAPSHOT" in
    *'|'*) ;;
    *)
      if ! docker stop --time 10 "$TARGET" >/dev/null 2>&1; then
        echo "stage network-policy failed: Docker returned an invalid network snapshot for $TARGET and it could not be stopped" >&2
        return 1
      fi
      echo "stage network-policy failed: Docker returned an invalid network snapshot for $TARGET; the sandbox was stopped" >&2
      return 1
      ;;
  esac
  ACTUAL_NETWORK=${NETWORK_SNAPSHOT%%|*}
  ACTUAL_NETWORKS=${NETWORK_SNAPSHOT#*|}
  case "$EXPECTED_NETWORK:$ACTUAL_NETWORK:$ACTUAL_NETWORKS" in
    none:none:|none:none:none,|restricted:none:|restricted:none:none,|egress:default:|egress:default:bridge,|egress:bridge:|egress:bridge:bridge,) return 0 ;;
  esac
  if ! docker stop --time 10 "$TARGET" >/dev/null 2>&1; then
    echo "stage network-policy failed: existing Docker sandbox uses mode $ACTUAL_NETWORK with attached networks ${ACTUAL_NETWORKS:-none} but requires $EXPECTED_NETWORK, and could not be stopped" >&2
    return 1
  fi
  echo "stage network-policy failed: existing Docker sandbox uses mode $ACTUAL_NETWORK with attached networks ${ACTUAL_NETWORKS:-none} but requires $EXPECTED_NETWORK; recreate it before reuse" >&2
  return 1
}

build_sandbox_manifest() {
  MANIFEST_JOB_FILE=$1
  MANIFEST_OUTPUT=$2
  jq '
    .job.payload as $payload |
    {
      manifestVersion: 1,
      sandboxId: $payload.sandboxId,
      name: $payload.name,
      driver: $payload.driver,
      image: $payload.image,
      workdir: $payload.workdir,
      cpu: $payload.cpu,
      memory: $payload.memory,
      desktop: $payload.desktop,
      network: $payload.network,
      workspace: $payload.workspace,
      proxyId: $payload.proxyId,
      agentTools: ($payload.agentTools // []),
      credentialIds: ($payload.credentialIds // []),
      skillIds: ($payload.skillIds // []),
      mcpServerIds: ($payload.mcpServerIds // []),
      variableIds: ($payload.variableIds // []),
      extensionIds: ($payload.extensionIds // []),
      capabilityDigest: $payload.capabilityDigest,
      mcpAllowNet: ($payload.mcpAllowNet // []),
      controlPlane: {allowNet: ($payload.controlPlane.allowNet // [])},
      variables: [($payload.variables // [])[] | {id: .id, key: .spec.key}],
      skills: [($payload.skills // [])[] | {id: .id, name: .name,
        digest: (.spec.bundleDigest // ""), fileCount: (((.spec.files // []) | length) + 1)}],
      mcpServers: [($payload.mcpServers // [])[] | {id: .id, name: .name, transport: .spec.transport}]
    } |
    with_entries(select(.value != null))
  ' "$MANIFEST_JOB_FILE" > "$MANIFEST_OUTPUT"
}

create_sandbox() {
  JOB_FILE=$1
  CREATE_VARIABLE_SNAPSHOT=${2:-}
  CREATE_VARIABLE_SNAPSHOT_OWNED=false
  if jq -e '(.job.payload.extensions // [] | length) > 0 and .job.action != "create-sandbox"' "$JOB_FILE" >/dev/null; then
    echo 'stage extensions failed: extensions can only run in a new create-sandbox job; create a new sandbox' >&2
    return 1
  fi
  SANDBOX_ID=$(jq -r '.job.payload.sandboxId' "$JOB_FILE")
  DRIVER=$(jq -r '.job.payload.driver' "$JOB_FILE")
  case "$DRIVER" in docker|boxlite|microsandbox) ;; *) echo "unsupported runtime driver: $DRIVER" >&2; return 1 ;; esac
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  IMAGE=$(jq -r '.job.payload.image' "$JOB_FILE")
  WORKDIR=$(jq -r '.job.payload.workdir // "/workspace"' "$JOB_FILE")
  CPU=$(jq -r '.job.payload.cpu // empty' "$JOB_FILE")
  MEMORY_VALUE=$(jq -r '.job.payload.memory // empty' "$JOB_FILE")
  NETWORK=$(effective_sandbox_network_policy "$DRIVER" "$(jq -r '.job.payload.network // empty' "$JOB_FILE")")
  SETUP=$(jq -r '.job.payload.setup // empty' "$JOB_FILE")
  TARGET=$(sandbox_created_container_name "$JOB_FILE") || return 1
  VOLUME="agentbox-$SANDBOX_ID-workspace"
  SANDBOX_PREEXISTED=false
  SANDBOX_VOLUME_PREEXISTED=false

  # Reject corrupt policy values before inspecting or stopping any runtime.
  validate_sandbox_network_policy_value "$NETWORK" || return 1
  # Docker cannot create "restricted", but an existing bridge-attached
  # container must be quarantined before returning that capability error.
  if [ "$DRIVER" != docker ]; then
    validate_sandbox_network_policy "$DRIVER" "$NETWORK" || return 1
  fi

  report_job_progress runtime-check "正在检查 $DRIVER 运行时"

  cleanup_failed_create() {
    [ "$SANDBOX_PREEXISTED" = false ] || return 0
    if [ "$DRIVER" = docker ]; then
      FOUND=$(command docker container ls -a --filter "name=^/$TARGET$" --format '{{.Names}}' 2>/dev/null) || return 0
      if [ "$FOUND" = "$TARGET" ]; then
        OWNER=$(command docker inspect -f '{{ index .Config.Labels "agentbox.sandbox" }}' "$TARGET" 2>/dev/null) || OWNER=
        [ "$OWNER" != "$SANDBOX_ID" ] || command docker rm -f -v "$TARGET" >/dev/null 2>&1 || true
      fi
      if [ "$SANDBOX_VOLUME_PREEXISTED" = false ]; then
        FOUND=$(command docker volume ls --filter "name=^$VOLUME$" --format '{{.Name}}' 2>/dev/null) || return 0
        if [ "$FOUND" = "$VOLUME" ]; then
          VOLUME_OWNER=$(command docker volume inspect -f '{{ index .Labels "agentbox.sandbox" }}' "$VOLUME" 2>/dev/null) || VOLUME_OWNER=
          [ "$VOLUME_OWNER" != "$SANDBOX_ID" ] || command docker volume rm "$VOLUME" >/dev/null 2>&1 || true
        fi
      fi
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
      if ! enforce_docker_network_policy "$TARGET" "$NETWORK"; then
        if [ "$NETWORK" = restricted ]; then
          validate_sandbox_network_policy "$DRIVER" "$NETWORK" || true
        fi
        return 1
      fi
      validate_sandbox_network_policy "$DRIVER" "$NETWORK" || return 1
      record_create_resource docker-stop "$TARGET" "$SANDBOX_ID"
      report_job_progress runtime-create "正在启动已有沙箱实例"
      docker start "$TARGET" >/dev/null
    else
      validate_sandbox_network_policy "$DRIVER" "$NETWORK" || return 1
      RUNTIME_IMAGE=$(prepare_agent_image "$IMAGE" "$JOB_FILE")
      report_job_progress runtime-create "正在创建 Docker 沙箱实例"
      if ! FOUND=$(command docker volume ls --filter "name=^$VOLUME$" --format '{{.Name}}'); then
        echo "Docker could not inspect sandbox volume $VOLUME" >&2
        return 1
      fi
      if [ -n "$FOUND" ]; then
        [ "$FOUND" = "$VOLUME" ] || { echo "Docker returned an unexpected sandbox volume: $FOUND" >&2; return 1; }
        VOLUME_OWNER=$(command docker volume inspect -f '{{ index .Labels "agentbox.sandbox" }}' "$VOLUME") || return 1
        [ "$VOLUME_OWNER" = "$SANDBOX_ID" ] || { echo "workspace volume name collision: $VOLUME" >&2; return 1; }
        SANDBOX_VOLUME_PREEXISTED=true
      else
        record_create_resource docker-volume "$VOLUME" "$SANDBOX_ID"
        command docker volume create --label "agentbox.sandbox=$SANDBOX_ID" "$VOLUME" >/dev/null || return 1
        VOLUME_OWNER=$(command docker volume inspect -f '{{ index .Labels "agentbox.sandbox" }}' "$VOLUME") || return 1
        [ "$VOLUME_OWNER" = "$SANDBOX_ID" ] || { echo "workspace volume ownership was not applied: $VOLUME" >&2; return 1; }
      fi
      record_create_resource docker-delete "$TARGET" "$SANDBOX_ID"
      set -- docker run -d --name "$TARGET" \
        --label "agentbox.sandbox=$SANDBOX_ID" \
        --restart unless-stopped --user 0:0 --workdir "$WORKDIR" \
        -v "$VOLUME:$WORKDIR"
      [ -n "$CPU" ] && set -- "$@" --cpus "$CPU"
      [ -n "$MEMORY" ] && set -- "$@" --memory "$MEMORY"
      [ "$NETWORK" = none ] && set -- "$@" --network none
      set -- "$@" "$RUNTIME_IMAGE" sh -lc 'trap : TERM INT; sleep infinity & wait'
      [ -z "${CREATE_STATE_DIR:-}" ] || : > "$CREATE_STATE_DIR/runtime-create-pending"
      if ! "$@" >/dev/null; then
        [ -z "${CREATE_STATE_DIR:-}" ] || rm -f "$CREATE_STATE_DIR/runtime-create-pending"
        cleanup_failed_create
        echo "stage runtime-create failed: Docker could not create the sandbox" >&2
        return 1
      fi
      [ -z "${CREATE_STATE_DIR:-}" ] || rm -f "$CREATE_STATE_DIR/runtime-create-pending"
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
      record_create_resource "$DRIVER-stop" "$TARGET"
    else
      record_create_resource "$DRIVER-delete" "$TARGET"
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
        '.job.payload.controlPlane.allowNet[]?, .job.payload.proxy.allowNet[]?, .job.payload.mcpAllowNet[]?' "$JOB_FILE" | sort -u); do
        set -- "$@" --allow-net "$ALLOWED_HOST"
      done
    fi
    [ -z "${CREATE_STATE_DIR:-}" ] || : > "$CREATE_STATE_DIR/runtime-create-pending"
    if ! "$@" >/dev/null; then
      [ -z "${CREATE_STATE_DIR:-}" ] || rm -f "$CREATE_STATE_DIR/runtime-create-pending"
      cleanup_failed_create
      echo "stage runtime-create failed: $DRIVER could not create the sandbox" >&2
      return 1
    fi
    [ -z "${CREATE_STATE_DIR:-}" ] || rm -f "$CREATE_STATE_DIR/runtime-create-pending"
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
  if ! build_sandbox_manifest "$JOB_FILE" "$MANIFEST" ||
     ! jq -e 'type == "object"' "$MANIFEST" >/dev/null ||
     ! write_container_file_atomic "$TARGET" /opt/agentbox/manifest.json 600 "$MANIFEST"; then
    rm -f "$MANIFEST"
    cleanup_failed_create
    echo "stage manifest-write failed: could not provision the sandbox manifest" >&2
    return 1
  fi
  rm -f "$MANIFEST"
  if [ -z "$CREATE_VARIABLE_SNAPSHOT" ]; then
    CREATE_VARIABLE_SNAPSHOT=$(mktemp) || {
      cleanup_failed_create
      echo "stage variables failed: could not create a protected Variable snapshot" >&2
      return 1
    }
    CREATE_VARIABLE_SNAPSHOT_OWNED=true
    if ! chmod 600 "$CREATE_VARIABLE_SNAPSHOT" ||
       ! resolve_worker_variables "$JOB_FILE" "$CREATE_VARIABLE_SNAPSHOT"; then
      rm -f "$CREATE_VARIABLE_SNAPSHOT"
      cleanup_failed_create
      echo "stage variables failed: image $IMAGE could not resolve AgentBox variable references" >&2
      return 1
    fi
  fi
  if ! configure_sandbox_agent_config "$TARGET" "$JOB_FILE" "$CREATE_VARIABLE_SNAPSHOT"; then
    [ "$CREATE_VARIABLE_SNAPSHOT_OWNED" != true ] || rm -f "$CREATE_VARIABLE_SNAPSHOT"
    cleanup_failed_create
    return 1
  fi
  if ! install_sandbox_extensions "$TARGET" "$JOB_FILE" "$CREATE_VARIABLE_SNAPSHOT"; then
    [ "$CREATE_VARIABLE_SNAPSHOT_OWNED" != true ] || rm -f "$CREATE_VARIABLE_SNAPSHOT"
    cleanup_failed_create
    echo 'stage extensions failed: sandbox extension provisioning did not complete' >&2
    return 1
  fi
  [ "$CREATE_VARIABLE_SNAPSHOT_OWNED" != true ] || rm -f "$CREATE_VARIABLE_SNAPSHOT"
  if [ -n "$SETUP" ]; then
    report_job_progress setup "正在执行模板初始化命令"
    if ! docker exec "$TARGET" sh -lc "$SETUP" >/dev/null 2>&1; then
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

sandbox_external_id() {
  SANDBOX_EXTERNAL_ID=$1
  SANDBOX_EXTERNAL_ID_SIZE=$(printf '%s' "$SANDBOX_EXTERNAL_ID" | wc -c | tr -d '[:space:]') || return 1
  case "$SANDBOX_EXTERNAL_ID" in
    ''|*[!A-Za-z0-9_.-]*) echo 'sandbox external id is invalid' >&2; return 1 ;;
  esac
  [ "$SANDBOX_EXTERNAL_ID_SIZE" -le 128 ] || { echo 'sandbox external id is invalid' >&2; return 1; }
  printf '%s' "$SANDBOX_EXTERNAL_ID"
}

sandbox_created_container_name() {
  SANDBOX_CREATED_ID=$(jq -er '.job.payload.sandboxId |
    if type == "string" and test("^[a-z0-9]+(-[a-z0-9]+)*$") then . else error("invalid sandbox id") end' "$1") || {
    echo 'sandbox id is invalid' >&2
    return 1
  }
  sandbox_external_id "agentbox-$SANDBOX_CREATED_ID"
}

sandbox_container_name() {
  JOB_FILE=$1
  EXTERNAL_ID=$(jq -r '.job.payload.externalId // empty' "$JOB_FILE")
  if [ -n "$EXTERNAL_ID" ]; then
    sandbox_external_id "$EXTERNAL_ID"
  else
    sandbox_created_container_name "$JOB_FILE"
  fi
}

start_sandbox() {
  JOB_FILE=$1
  START_VARIABLE_SNAPSHOT=${2:-}
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  NETWORK=$(effective_sandbox_network_policy "$DRIVER" "$(jq -r '.job.payload.network // empty' "$JOB_FILE")")
  validate_sandbox_network_policy_value "$NETWORK" || return 1
  EXTERNAL_ID=$(jq -r '.job.payload.externalId // empty' "$JOB_FILE")
  if [ -z "$EXTERNAL_ID" ]; then
    create_sandbox "$JOB_FILE" "$START_VARIABLE_SNAPSHOT"
    return
  fi
  TARGET=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  report_job_progress runtime-check "正在检查 $DRIVER 运行时"
  report_job_progress runtime-start "正在启动沙箱实例"
  if [ "$DRIVER" = docker ]; then
    docker inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
    if ! enforce_docker_network_policy "$TARGET" "$NETWORK"; then
      if [ "$NETWORK" = restricted ]; then
        validate_sandbox_network_policy "$DRIVER" "$NETWORK" || true
      fi
      return 1
    fi
    validate_sandbox_network_policy "$DRIVER" "$NETWORK" || return 1
    docker start "$TARGET" >/dev/null || return 1
  else
    validate_sandbox_network_policy "$DRIVER" "$NETWORK" || return 1
    runtime_call "$DRIVER" start "$TARGET" >/dev/null || return 1
    WORKDIR=$(jq -r '.job.payload.workdir // "/workspace"' "$JOB_FILE")
    if [ "$DRIVER" = boxlite ]; then
      install_boxlite_guest "$TARGET" "$WORKDIR" >/dev/null || return 1
    else
      runtime_call "$DRIVER" fs-mkdir "$TARGET" "$WORKDIR" >/dev/null || return 1
    fi
  fi

  if ! configure_proxy "$TARGET" "$JOB_FILE"; then
    echo "stage proxy-config failed: sandbox could not apply the selected network proxy" >&2
    return 1
  fi

  if jq -e '.job.payload.credentials? and .job.payload.agentTools?' "$JOB_FILE" >/dev/null; then
    report_job_progress configuration "正在同步沙箱配置"
    install_agent_tools "$TARGET" "$JOB_FILE" || return 1
    configure_sandbox_agent_config "$TARGET" "$JOB_FILE" "$START_VARIABLE_SNAPSHOT" || return 1
  fi
  ensure_desktop "$TARGET" "$JOB_FILE" || return 1
  report_job_progress verify "正在验证沙箱可用性"
  printf '%s' "$TARGET"
}

restart_sandbox() {
  JOB_FILE=$1
  RESTART_VARIABLE_SNAPSHOT=${2:-}
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  NETWORK=$(effective_sandbox_network_policy "$DRIVER" "$(jq -r '.job.payload.network // empty' "$JOB_FILE")")
  validate_sandbox_network_policy_value "$NETWORK" || return 1
  if [ "$DRIVER" = docker ]; then
    TARGET=$(sandbox_container_name "$JOB_FILE")
    docker inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
    if ! enforce_docker_network_policy "$TARGET" "$NETWORK"; then
      if [ "$NETWORK" = restricted ]; then
        validate_sandbox_network_policy "$DRIVER" "$NETWORK" || true
      fi
      return 1
    fi
    validate_sandbox_network_policy "$DRIVER" "$NETWORK" || return 1
  else
    validate_sandbox_network_policy "$DRIVER" "$NETWORK" || return 1
  fi
  stop_sandbox "$JOB_FILE" >/dev/null || return 1
  start_sandbox "$JOB_FILE" "$RESTART_VARIABLE_SNAPSHOT"
}

apply_sandbox_proxy() {
  JOB_FILE=$1
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  TARGET=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  report_job_progress proxy-config "正在应用沙箱网络出口"
  if [ "$DRIVER" = docker ]; then
    docker inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
  else
    runtime_call "$DRIVER" inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox runtime not found" >&2; return 1; }
  fi
  configure_proxy "$TARGET" "$JOB_FILE" || return 1
  if jq -e '.job.payload.proxy? and (.job.payload.proxy.url // "") != ""' "$JOB_FILE" >/dev/null; then
    docker exec "$TARGET" test -s /opt/agentbox/secrets/proxy.env || return 1
  else
    if docker exec "$TARGET" test -e /opt/agentbox/secrets/proxy.env; then
      echo "sandbox proxy configuration was not removed" >&2
      return 1
    fi
  fi
  printf '%s' "$TARGET"
}

stop_sandbox() {
  JOB_FILE=$1
  DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
  TARGET=$(sandbox_container_name "$JOB_FILE")
  AGENTBOX_RUNTIME_DRIVER=$DRIVER
  export AGENTBOX_RUNTIME_DRIVER
  if [ "$DRIVER" = docker ]; then
    docker inspect "$TARGET" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
    docker stop --time 10 "$TARGET" >/dev/null || return 1
  else
    runtime_call "$DRIVER" stop "$TARGET" >/dev/null || return 1
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
    VOLUME="agentbox-$SANDBOX_ID-workspace"
    VOLUME_OWNERSHIP_DIR="$STATE_DIR/docker-volume-ownership"
    VOLUME_OWNERSHIP_MARKER="$VOLUME_OWNERSHIP_DIR/$SANDBOX_ID"
    mkdir -p "$VOLUME_OWNERSHIP_DIR" || return 1
    chmod 700 "$VOLUME_OWNERSHIP_DIR" || return 1
    if ! timeout 10 docker info >/dev/null 2>&1; then
      echo "Docker daemon is unavailable" >&2
      return 1
    fi
    if ! FOUND=$(docker container ls -a --filter "name=^/$TARGET$" --format '{{.Names}}'); then
      echo "Docker could not inspect sandbox container $TARGET" >&2
      return 1
    fi
    if [ -n "$FOUND" ]; then
      if [ "$FOUND" != "$TARGET" ]; then
        echo "Docker returned an unexpected sandbox container: $FOUND" >&2
        return 1
      fi
      if ! OWNER=$(docker inspect -f '{{ index .Config.Labels "agentbox.sandbox" }}' "$TARGET"); then
        echo "Docker could not inspect sandbox ownership for $TARGET" >&2
        return 1
      fi
      if [ "$OWNER" != "$SANDBOX_ID" ]; then
        echo "refusing to delete container $TARGET owned by ${OWNER:-unknown}" >&2
        return 1
      fi
      if ! MOUNTS=$(docker inspect -f '{{ range .Mounts }}{{ println .Name }}{{ end }}' "$TARGET"); then
        echo "Docker could not inspect sandbox mounts for $TARGET" >&2
        return 1
      fi
      if printf '%s\n' "$MOUNTS" | grep -Fx "$VOLUME" >/dev/null; then
        if ! VOLUME_FINGERPRINT=$(docker volume inspect -f '{{.Mountpoint}}|{{.CreatedAt}}' "$VOLUME"); then
          echo "Docker could not fingerprint sandbox volume $VOLUME before deleting its container" >&2
          return 1
        fi
        VOLUME_MARKER_TMP=$(mktemp "$VOLUME_OWNERSHIP_DIR/$SANDBOX_ID.XXXXXX") || return 1
        if ! printf '%s\t%s\n' "$VOLUME" "$VOLUME_FINGERPRINT" > "$VOLUME_MARKER_TMP" ||
           ! mv "$VOLUME_MARKER_TMP" "$VOLUME_OWNERSHIP_MARKER"; then
          rm -f "$VOLUME_MARKER_TMP"
          echo "Docker could not persist sandbox volume ownership for $VOLUME" >&2
          return 1
        fi
      fi
      if ! docker rm -f -v "$TARGET" >/dev/null; then
        echo "Docker could not delete sandbox container $TARGET" >&2
        return 1
      fi
    fi
    if ! FOUND=$(docker volume ls --filter "name=^$VOLUME$" --format '{{.Name}}'); then
      echo "Docker could not inspect sandbox volume $VOLUME" >&2
      return 1
    fi
    if [ -n "$FOUND" ]; then
      if [ "$FOUND" != "$VOLUME" ]; then
        echo "Docker returned an unexpected sandbox volume: $FOUND" >&2
        return 1
      fi
      if ! VOLUME_OWNER=$(docker volume inspect -f '{{ index .Labels "agentbox.sandbox" }}' "$VOLUME"); then
        echo "Docker could not inspect sandbox volume ownership for $VOLUME" >&2
        return 1
      fi
      if ! VOLUME_FINGERPRINT=$(docker volume inspect -f '{{.Mountpoint}}|{{.CreatedAt}}' "$VOLUME"); then
        echo "Docker could not fingerprint sandbox volume $VOLUME" >&2
        return 1
      fi
      MARKER_OWNED_VOLUME=false
      if [ -f "$VOLUME_OWNERSHIP_MARKER" ]; then
        TAB=$(printf '\t')
        MARKER_VOLUME=
        MARKER_FINGERPRINT=
        IFS="$TAB" read -r MARKER_VOLUME MARKER_FINGERPRINT < "$VOLUME_OWNERSHIP_MARKER" || true
        if [ "$MARKER_VOLUME" = "$VOLUME" ] && [ "$MARKER_FINGERPRINT" = "$VOLUME_FINGERPRINT" ]; then
          MARKER_OWNED_VOLUME=true
        else
          rm -f "$VOLUME_OWNERSHIP_MARKER"
        fi
      fi
      if [ -n "$VOLUME_OWNER" ] && [ "$VOLUME_OWNER" != "$SANDBOX_ID" ]; then
        echo "refusing to delete workspace volume $VOLUME owned by $VOLUME_OWNER" >&2
        return 1
      fi
      if [ -z "$VOLUME_OWNER" ] && [ "$MARKER_OWNED_VOLUME" != true ]; then
        echo "warning: leaving unverifiable legacy workspace volume $VOLUME for manual cleanup" >&2
        return 0
      fi
      if ! docker volume rm "$VOLUME" >/dev/null; then
        echo "Docker could not delete sandbox volume $VOLUME" >&2
        return 1
      fi
      rm -f "$VOLUME_OWNERSHIP_MARKER"
    else
      rm -f "$VOLUME_OWNERSHIP_MARKER"
    fi
  else
    runtime_call "$DRIVER" delete "$TARGET"
  fi
}

check_network_proxy() {
  JOB_FILE=$1
  PROXY_URL=$(jq -r '.job.payload.proxy.url // empty' "$JOB_FILE")
  TARGET=$(jq -r '.job.payload.target // empty' "$JOB_FILE")
  CHECKED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  if [ -z "$PROXY_URL" ] || [ -z "$TARGET" ]; then
    jq -n --arg target "$TARGET" --arg checkedAt "$CHECKED_AT" \
      '{ok:false,latencyMs:0,target:$target,error:"Worker 收到的代理检测任务无效",checkedAt:$checkedAt}'
    return
  fi

  METRICS=$(
    HTTP_PROXY="$PROXY_URL" HTTPS_PROXY="$PROXY_URL" ALL_PROXY="$PROXY_URL" \
    http_proxy="$PROXY_URL" https_proxy="$PROXY_URL" all_proxy="$PROXY_URL" \
    NO_PROXY='' no_proxy='' \
    curl -sS -o /dev/null -w '%{http_code}\t%{time_total}' \
      --connect-timeout 5 --max-time 10 "$TARGET" 2>/dev/null
  )
  CURL_STATUS=$?
  HTTP_STATUS=$(printf '%s' "$METRICS" | awk -F '\t' '{print $1}')
  TOTAL_SECONDS=$(printf '%s' "$METRICS" | awk -F '\t' '{print $2}')
  LATENCY_MS=0
  if [ -n "$TOTAL_SECONDS" ]; then
    LATENCY_MS=$(awk -v seconds="$TOTAL_SECONDS" 'BEGIN {
      value = int(seconds * 1000); if (value < 1) value = 1; print value
    }')
  fi

  OK=false
  ERROR_MESSAGE=
  if [ "$CURL_STATUS" -eq 0 ] && [ "$HTTP_STATUS" = 204 ]; then
    OK=true
  elif [ "$CURL_STATUS" -eq 0 ]; then
    ERROR_MESSAGE="检测地址返回 HTTP $HTTP_STATUS"
  else
    case "$CURL_STATUS" in
      5) ERROR_MESSAGE="Worker 无法解析代理主机" ;;
      7) ERROR_MESSAGE="Worker 无法连接代理" ;;
      28) ERROR_MESSAGE="Worker 代理检测超时（10 秒）" ;;
      35|60) ERROR_MESSAGE="Worker 代理 TLS 校验失败" ;;
      *) ERROR_MESSAGE="Worker 无法通过代理访问检测地址" ;;
    esac
  fi
  jq -n --argjson ok "$OK" --argjson latencyMs "$LATENCY_MS" \
    --arg target "$TARGET" --arg statusCode "$HTTP_STATUS" \
    --arg error "$ERROR_MESSAGE" --arg checkedAt "$CHECKED_AT" \
    '{ok:$ok,latencyMs:$latencyMs,target:$target,
      statusCode:(if $statusCode == "" or $statusCode == "000" then null else ($statusCode | tonumber) end),
      error:(if $error == "" then null else $error end),checkedAt:$checkedAt}'
}

record_create_resource() {
  [ -n "${CREATE_STATE_DIR:-}" ] || return 0
  printf '%s\t%s\t%s\n' "$1" "$2" "${3:-}" >> "$CREATE_STATE_DIR/resources"
}

settle_create_pending_marker() {
  PENDING_MARKER=$1
  RESET_SETTLE=${2:-false}
  SETTLE_MARKER="$PENDING_MARKER.settling"
  if [ ! -f "$PENDING_MARKER" ]; then
    rm -f "$SETTLE_MARKER"
    return 0
  fi
  SETTLE_SECONDS=${AGENTBOX_CREATE_PENDING_SETTLE_SECONDS:-30}
  case "$SETTLE_SECONDS" in ''|*[!0-9]*) SETTLE_SECONDS=30 ;; esac
  if [ "$RESET_SETTLE" = true ]; then
    date +%s > "$SETTLE_MARKER" || return 1
    return 1
  fi
  if [ ! -f "$SETTLE_MARKER" ]; then
    date +%s > "$SETTLE_MARKER" || return 1
    return 1
  fi
  SETTLE_STARTED=$(cat "$SETTLE_MARKER") || return 1
  case "$SETTLE_STARTED" in
    ''|*[!0-9]*) rm -f "$SETTLE_MARKER"; return 1 ;;
  esac
  SETTLE_NOW=$(date +%s) || return 1
  [ "$SETTLE_NOW" -ge "$SETTLE_STARTED" ] || return 1
  [ "$((SETTLE_NOW - SETTLE_STARTED))" -ge "$SETTLE_SECONDS" ] || return 1
  rm -f "$PENDING_MARKER" "$SETTLE_MARKER"
}

# Freeze parents before walking their children so an installer cannot spawn a
# retry while cancellation is killing its processes. Runtime exec processes are
# stopped separately by removing/stopping the recorded container or VM.
kill_create_process_tree() (
  TREE_PID=$1
  # A create operation may have restarted the shared BoxLite service. It and
  # its other sandboxes are not part of this installation's process tree.
  SHARED_PID=$(cat "${BOXLITE_SERVER_PID_FILE:-/nonexistent}" 2>/dev/null || true)
  [ "$TREE_PID" != "$SHARED_PID" ] || return 0
  kill -STOP "$TREE_PID" 2>/dev/null || return 0
  for CHILD_PID in $(ps -eo pid=,ppid= | awk -v parent="$TREE_PID" '$2 == parent { print $1 }'); do
    kill_create_process_tree "$CHILD_PID"
  done
  kill -KILL "$TREE_PID" 2>/dev/null || true
)

poll_create_control() (
  CONTROL_BODY=$(jq -cn --argjson leaseGeneration "$LEASE_GENERATION" '{leaseGeneration:$leaseGeneration}')
  CONTROL_STATUS=$(worker_request "$CREATE_STATE_DIR/control" --max-time 5 -X POST \
    "$SERVER_URL/api/servers/$SERVER_ID/jobs/$JOB_ID/control" \
    -H "Authorization: Bearer $CREDENTIAL" -H 'Content-Type: application/json' \
    --data "$CONTROL_BODY") || { printf unavailable; return; }
  case "$CONTROL_STATUS" in
    200)
      if jq -e '.cancelRequested == true' "$CREATE_STATE_DIR/control" >/dev/null 2>&1; then
        printf cancel
      elif jq -e '.cancelRequested == false' "$CREATE_STATE_DIR/control" >/dev/null 2>&1; then
        printf continue
      else
        printf unavailable
      fi
      ;;
    404) printf unsupported ;;
    401|403|409) printf rejected ;;
    *) printf unavailable ;;
  esac
)

cleanup_cancelled_create() (
  CLEANUP_FAILED=false
  RUNTIME_RESOURCE_OBSERVED=false
  BUILD_RESOURCE_OBSERVED=false
  # Reverse creation order: remove containers before their new workspace
  # volumes. Never remove a pre-existing workspace, sandbox or successful cache.
  awk '{ lines[NR] = $0 } END { for (i = NR; i > 0; i--) print lines[i] }' \
    "$CREATE_STATE_DIR/resources" > "$CREATE_STATE_DIR/cleanup"
  TAB=$(printf '\t')
  while IFS="$TAB" read -r KIND TARGET EXPECTED_OWNER; do
    [ -n "$TARGET" ] || continue
    case "$KIND" in
      docker-delete|docker-stop)
        if ! FOUND=$(command docker container ls -a --filter "name=^/$TARGET$" --format '{{.Names}}'); then
          CLEANUP_FAILED=true
          continue
        fi
        [ "$FOUND" = "$TARGET" ] || continue
        [ "$KIND" != docker-delete ] || RUNTIME_RESOURCE_OBSERVED=true
        if ! OWNER=$(command docker inspect -f '{{ index .Config.Labels "agentbox.sandbox" }}' "$TARGET"); then
          CLEANUP_FAILED=true
          continue
        fi
        if [ -z "$EXPECTED_OWNER" ] || [ "$OWNER" != "$EXPECTED_OWNER" ]; then
          CLEANUP_FAILED=true
          continue
        fi
        if [ "$KIND" = docker-delete ]; then
          command docker rm -f -v "$TARGET" >/dev/null || CLEANUP_FAILED=true
        else
          command docker stop --time 10 "$TARGET" >/dev/null || CLEANUP_FAILED=true
        fi
        ;;
      docker-build-delete)
        if ! FOUND=$(command docker container ls -a --filter "name=^/$TARGET$" --format '{{.Names}}'); then
          CLEANUP_FAILED=true
          continue
        fi
        [ "$FOUND" = "$TARGET" ] || continue
        BUILD_RESOURCE_OBSERVED=true
        if ! OWNER=$(command docker inspect -f '{{ index .Config.Labels "agentbox.runtime.build" }}' "$TARGET"); then
          CLEANUP_FAILED=true
          continue
        fi
        if [ -z "$EXPECTED_OWNER" ] || [ "$OWNER" != "$EXPECTED_OWNER" ]; then
          CLEANUP_FAILED=true
          continue
        fi
        command docker rm -f -v "$TARGET" >/dev/null || CLEANUP_FAILED=true
        ;;
      docker-volume)
        if ! FOUND=$(command docker volume ls --filter "name=^$TARGET$" --format '{{.Name}}'); then
          CLEANUP_FAILED=true
          continue
        fi
        [ "$FOUND" = "$TARGET" ] || continue
        RUNTIME_RESOURCE_OBSERVED=true
        if ! OWNER=$(command docker volume inspect -f '{{ index .Labels "agentbox.sandbox" }}' "$TARGET"); then
          CLEANUP_FAILED=true
          continue
        fi
        if [ -z "$EXPECTED_OWNER" ] || [ "$OWNER" != "$EXPECTED_OWNER" ]; then
          CLEANUP_FAILED=true
          continue
        fi
        command docker volume rm "$TARGET" >/dev/null || CLEANUP_FAILED=true
        ;;
      docker-image)
        if ! FOUND=$(command docker image ls --filter "reference=$TARGET" --format '{{.ID}}'); then
          CLEANUP_FAILED=true
          continue
        fi
        [ -n "$FOUND" ] || continue
        BUILD_RESOURCE_OBSERVED=true
        command docker image rm "$TARGET" >/dev/null || CLEANUP_FAILED=true
        ;;
      boxlite-delete|microsandbox-delete)
        RUNTIME_DRIVER=${KIND%-delete}
        RUNTIME_WAS_PRESENT=false
        if runtime_call "$RUNTIME_DRIVER" inspect "$TARGET" >/dev/null 2>&1; then
          RUNTIME_WAS_PRESENT=true
        fi
        if ! runtime_call "$RUNTIME_DRIVER" delete "$TARGET" >/dev/null; then
          CLEANUP_FAILED=true
        elif runtime_call "$RUNTIME_DRIVER" inspect "$TARGET" >/dev/null 2>&1; then
          RUNTIME_RESOURCE_OBSERVED=true
          CLEANUP_FAILED=true
        elif [ "$RUNTIME_WAS_PRESENT" = true ]; then
          RUNTIME_RESOURCE_OBSERVED=true
        fi
        ;;
      boxlite-stop|microsandbox-stop)
        runtime_call "${KIND%-stop}" stop "$TARGET" >/dev/null || CLEANUP_FAILED=true
        ;;
      *) CLEANUP_FAILED=true ;;
    esac
  done < "$CREATE_STATE_DIR/cleanup"
  # A detached runtime create RPC may still be finishing after its client dies.
  # Keep retrying through a quiet period, then allow the journal to converge.
  settle_create_pending_marker "$CREATE_STATE_DIR/runtime-create-pending" "$RUNTIME_RESOURCE_OBSERVED" || CLEANUP_FAILED=true
  settle_create_pending_marker "$CREATE_STATE_DIR/build-create-pending" "$BUILD_RESOURCE_OBSERVED" || CLEANUP_FAILED=true
  [ "$CLEANUP_FAILED" = false ]
)

complete_cancelled_create() {
  if cleanup_cancelled_create 2>>"$CREATE_STATE_DIR/log"; then
    CLEANUP_CONFIRMED=true
    CANCEL_ERROR=job_cancelled
    CANCEL_MESSAGE='安装已取消，已清理本次创建的临时资源'
  else
    CLEANUP_CONFIRMED=false
    : > "$CREATE_STATE_DIR/cleanup-required"
    CANCEL_ERROR=cancellation_cleanup_failed
    CANCEL_MESSAGE='安装已中断，但未能确认临时资源清理完成，请检查 Worker 后删除沙箱'
  fi
  # Keep the lease and retry the acknowledgement after a transient network
  # failure. The Server must not release this job before cleanup is confirmed.
  while ! complete_job_failure "$JOB_ID" "$CANCEL_ERROR" cancel false "$CANCEL_MESSAGE" create-sandbox; do
    CONTROL=$(poll_create_control)
    case "$CONTROL" in rejected|unsupported) return 1 ;; esac
    sleep 2
  done
  if [ "$CLEANUP_CONFIRMED" = true ]; then
    CREATE_FINISHED=true
  fi
}

process_create_job() (
  CREATE_STATE_DIR=$(mktemp -d "$STATE_DIR/create-$JOB_ID.XXXXXX")
  chmod 700 "$CREATE_STATE_DIR"
  : > "$CREATE_STATE_DIR/resources"
  cp "$JOB_FILE" "$CREATE_STATE_DIR/job.json"
  CREATE_FINISHED=false
  CREATE_PID=
  cleanup_create_supervisor() {
    if [ -n "$CREATE_PID" ]; then
      kill_create_process_tree "$CREATE_PID"
      wait "$CREATE_PID" 2>/dev/null || true
      cleanup_cancelled_create 2>/dev/null || true
    fi
    # Keep the root-only journal until Server acknowledgement. A restarted
    # Worker can finish cancellation, including after cleanup but before ACK.
    [ "$CREATE_FINISHED" != true ] || rm -rf "$CREATE_STATE_DIR"
  }
  trap cleanup_create_supervisor EXIT
  trap 'exit 1' INT TERM
  CONTROL_SUPPORTED=false
  CONTROL=$(poll_create_control)
  [ "$CONTROL" != rejected ] || return 1
  [ "$CONTROL" != continue ] || CONTROL_SUPPORTED=true
  if [ "$CONTROL" = cancel ]; then
    complete_cancelled_create
    return
  fi
  (
    CREATE_VARIABLE_SNAPSHOT=$CREATE_STATE_DIR/variables.snapshot.json
    if ! prepare_job_variable_snapshot "$JOB_FILE" "$CREATE_VARIABLE_SNAPSHOT"; then
      # Resolution errors contain only the target key. Keep an empty protected
      # snapshot so the final error can still redact direct env/credential data.
      printf '%s\n' '{}' > "$CREATE_VARIABLE_SNAPSHOT"
      chmod 600 "$CREATE_VARIABLE_SNAPSHOT"
      : > "$CREATE_STATE_DIR/variable-resolution-failed"
      echo 'stage variables failed: could not resolve AgentBox variable references' >&2
      exit 1
    fi
    create_sandbox "$JOB_FILE" "$CREATE_VARIABLE_SNAPSHOT"
  ) >"$CREATE_STATE_DIR/output" 2>"$CREATE_STATE_DIR/log" &
  CREATE_PID=$!
  while kill -0 "$CREATE_PID" 2>/dev/null; do
    CONTROL=$(poll_create_control)
    [ "$CONTROL" != continue ] || CONTROL_SUPPORTED=true
    if [ "$CONTROL" = unsupported ] && [ "$CONTROL_SUPPORTED" = true ]; then CONTROL=rejected; fi
    if [ "$CONTROL" = cancel ] || [ "$CONTROL" = rejected ]; then
      kill_create_process_tree "$CREATE_PID"
      wait "$CREATE_PID" 2>/dev/null || true
      CREATE_PID=
      if [ "$CONTROL" = cancel ]; then
        complete_cancelled_create
      else
        cleanup_cancelled_create
      fi
      return
    fi
    sleep 1
  done
  CREATE_SUCCESS=true
  wait "$CREATE_PID" || CREATE_SUCCESS=false
  CREATE_PID=
  EXTERNAL_ID=
  if [ "$CREATE_SUCCESS" = true ] &&
     ! EXTERNAL_ID=$(sandbox_created_container_name "$JOB_FILE"); then
    CREATE_SUCCESS=false
    echo 'stage create-result failed: sandbox external id is invalid' >> "$CREATE_STATE_DIR/log"
  fi
  MESSAGE='Sandbox created'
  ERROR_CODE=
  ERROR_STAGE=
  ERROR_RETRYABLE=false
  CLEANUP_CONFIRMED=true
  if [ "$CREATE_SUCCESS" = false ]; then
    EXTERNAL_ID=
    if [ -f "$CREATE_STATE_DIR/variable-resolution-failed" ]; then
      ERROR_STAGE=variables
      MESSAGE='stage variables failed: could not resolve AgentBox variable references'
    else
      ERROR_STAGE=$(worker_error_stage_from_log "$CREATE_STATE_DIR/log" || true)
      [ -n "$ERROR_STAGE" ] || ERROR_STAGE=create
      MESSAGE=$(worker_safe_stage_message "$ERROR_STAGE")
    fi
    ERROR_CODE=sandbox_create_failed
    ERROR_RETRYABLE=$(worker_error_retryable "$ERROR_STAGE")
    if ! cleanup_cancelled_create 2>>"$CREATE_STATE_DIR/log"; then
      CLEANUP_CONFIRMED=false
      : > "$CREATE_STATE_DIR/cleanup-required"
      ERROR_CODE=sandbox_create_cleanup_failed
      ERROR_STAGE=cleanup
      ERROR_RETRYABLE=false
      MESSAGE="$MESSAGE
Worker could not confirm cleanup of temporary create resources; cleanup will be retried from the journal."
    fi
  fi
  while ! complete_job "$JOB_ID" "$CREATE_SUCCESS" "$EXTERNAL_ID" "$MESSAGE" "" /dev/null false false \
      "$ERROR_CODE" "$ERROR_STAGE" "$ERROR_RETRYABLE" '{"action":"create-sandbox"}'; do
    # Cancellation may win the transaction just as installation finishes.
    CONTROL=$(poll_create_control)
    if [ "$CONTROL" = cancel ]; then
      complete_cancelled_create
      return
    fi
    case "$CONTROL" in rejected|unsupported) return 1 ;; esac
    sleep 2
  done
  if [ "$CREATE_SUCCESS" = true ] || [ "$CLEANUP_CONFIRMED" = true ]; then
    CREATE_FINISHED=true
  fi
)

recover_create_jobs() (
  for CREATE_STATE_DIR in "$STATE_DIR"/create-*; do
    [ -f "$CREATE_STATE_DIR/job.json" ] || continue
    JOB_FILE="$CREATE_STATE_DIR/job.json"
    JOB_ID=$(jq -r '.job.id' "$JOB_FILE")
    LEASE_GENERATION=$(jq -r '.job.leaseGeneration' "$JOB_FILE")
    CREATE_FINISHED=false
    CONTROL=$(poll_create_control)
    case "$CONTROL" in
      cancel) complete_cancelled_create || true ;;
      continue)
        # The service process group was stopped, but detached runtime execs
        # may remain. Clean them before failing this interrupted creation.
        if cleanup_cancelled_create; then
          RECOVERY_CLEANUP_CONFIRMED=true
          RECOVERY_ERROR=worker_interrupted
          RECOVERY_MESSAGE='Worker 重启中断了安装，已清理本次临时资源，请重新创建沙箱'
        else
          RECOVERY_CLEANUP_CONFIRMED=false
          : > "$CREATE_STATE_DIR/cleanup-required"
          RECOVERY_ERROR=worker_interrupted_cleanup_failed
          RECOVERY_MESSAGE='Worker 重启中断了安装，未能确认清理完成，请检查 Worker 后删除沙箱'
        fi
        if complete_job_failure "$JOB_ID" "$RECOVERY_ERROR" create false "$RECOVERY_MESSAGE" create-sandbox; then
          if [ "$RECOVERY_CLEANUP_CONFIRMED" = true ]; then
            CREATE_FINISHED=true
          fi
        elif [ "$(poll_create_control)" = cancel ]; then
          complete_cancelled_create || true
        fi
        ;;
      rejected|unsupported)
        if [ -f "$CREATE_STATE_DIR/cleanup-required" ]; then
          if cleanup_cancelled_create; then
            rm -f "$CREATE_STATE_DIR/cleanup-required"
            CREATE_FINISHED=true
          elif [ ! -f "$CREATE_STATE_DIR/recovery-warned" ]; then
            echo "warning: creation $JOB_ID cleanup is still incomplete" >&2
            : > "$CREATE_STATE_DIR/recovery-warned"
          fi
        elif [ ! -f "$CREATE_STATE_DIR/recovery-warned" ]; then
          echo "warning: creation $JOB_ID is no longer active; retaining its unclassified journal" >&2
          : > "$CREATE_STATE_DIR/recovery-warned"
        fi
        ;;
      *)
        if [ ! -f "$CREATE_STATE_DIR/recovery-warned" ]; then
          echo "warning: creation $JOB_ID awaits lease verification before recovery" >&2
          : > "$CREATE_STATE_DIR/recovery-warned"
        fi
        ;;
    esac
    [ "$CREATE_FINISHED" != true ] || rm -rf "$CREATE_STATE_DIR"
  done
)

process_job() {
  JOB_FILE=$1
  JOB_ID=$(jq -r '.job.id' "$JOB_FILE")
  LEASE_GENERATION=$(jq -r '.job.leaseGeneration // 0' "$JOB_FILE")
  ACTION=$(jq -r '.job.action' "$JOB_FILE")
  JOB_VARIABLE_SNAPSHOT=
  JOB_VARIABLE_SNAPSHOT_ERROR=
  JOB_VARIABLE_SNAPSHOT_READY=true
  if [ "$ACTION" != create-sandbox ]; then
    JOB_VARIABLE_SNAPSHOT=$(mktemp) || return 1
    JOB_VARIABLE_SNAPSHOT_ERROR=$(mktemp) || { rm -f "$JOB_VARIABLE_SNAPSHOT"; return 1; }
    rm -f "$JOB_VARIABLE_SNAPSHOT"
    if ! prepare_job_variable_snapshot "$JOB_FILE" "$JOB_VARIABLE_SNAPSHOT" 2>"$JOB_VARIABLE_SNAPSHOT_ERROR"; then
      JOB_VARIABLE_SNAPSHOT_READY=false
      printf '%s\n' '{}' > "$JOB_VARIABLE_SNAPSHOT" || return 1
      chmod 600 "$JOB_VARIABLE_SNAPSHOT" || return 1
    fi
  fi
  case "$ACTION" in
    check-network-proxy)
      RESULT_FILE=$(mktemp)
      if check_network_proxy "$JOB_FILE" > "$RESULT_FILE"; then
        ERROR_DETAILS=$(jq -cn --arg action "$ACTION" '{action:$action}')
        if jq -e '.ok == true' "$RESULT_FILE" >/dev/null; then
          complete_job "$JOB_ID" true "" "Network proxy check succeeded" "" "$RESULT_FILE"
        else
          complete_job "$JOB_ID" false "" "Network proxy check failed" "" "$RESULT_FILE" \
            false false network_proxy_check_failed proxy-check true "$ERROR_DETAILS"
        fi
      else
        complete_job_failure "$JOB_ID" network_proxy_check_failed proxy-check true \
          "Network proxy check failed" "$ACTION"
      fi
      rm -f "$RESULT_FILE"
      ;;
    check-sandbox-agent-tools)
      RESULT_FILE=$(mktemp)
      LOG_FILE=$(mktemp)
      if check_agent_tools "$JOB_FILE" "$RESULT_FILE" 2>"$LOG_FILE"; then
        complete_agent_tool_job "$JOB_ID" true "Agent 工具检测完成" "$RESULT_FILE" "$ACTION"
      else
        MESSAGE=$(redact_job_output "$JOB_FILE" "$LOG_FILE" "$JOB_VARIABLE_SNAPSHOT" 3500)
        if [ -z "$MESSAGE" ] || worker_output_was_withheld "$MESSAGE"; then
          MESSAGE="Agent 工具检测失败；详细命令输出仅保留在 Worker 本机"
        fi
        complete_agent_tool_job "$JOB_ID" false "$MESSAGE" "$RESULT_FILE" "$ACTION" \
          sandbox_agent_tools_check_failed
      fi
      rm -f "$RESULT_FILE" "$LOG_FILE"
      ;;
    update-sandbox-agent-tools)
      RESULT_FILE=$(mktemp)
      LOG_FILE=$(mktemp)
      UPDATE_OK=false
      UPDATE_DRIVER=$(jq -r '.job.payload.driver // "docker"' "$JOB_FILE")
      UPDATE_TARGET=$(sandbox_container_name "$JOB_FILE")
      UPDATE_ONLY_CODEX=$(jq -r \
        '[.job.payload.requestedAgentTools[]?] == ["codex"]' "$JOB_FILE")
      [ "$UPDATE_DRIVER" != boxlite ] || pause_session_worker
      if update_agent_tools "$JOB_FILE" "$RESULT_FILE" >"$LOG_FILE" 2>&1; then
        UPDATE_OK=true
      fi
      if [ "$UPDATE_DRIVER" = boxlite ]; then
        report_job_progress agent-tools-update "正在恢复 BoxLite 终端连接"
        if [ "$UPDATE_OK" = true ]; then
          if [ "$UPDATE_ONLY_CODEX" = true ]; then
            ensure_boxlite_server >>"$LOG_FILE" 2>&1 || UPDATE_OK=false
          elif ! docker exec "$UPDATE_TARGET" sh -lc \
              'setsid -f sh -lc '\''sync'\'' >/dev/null 2>&1' \
              >>"$LOG_FILE" 2>&1 ||
               ! checkpoint_boxlite_agent_update "$UPDATE_TARGET" \
                 >>"$LOG_FILE" 2>&1; then
            echo "BoxLite update checkpoint or terminal recovery failed" >>"$LOG_FILE"
            UPDATE_OK=false
          fi
        elif ! recover_boxlite_agent_tool_install \
              "$UPDATE_TARGET" \
              >>"$LOG_FILE" 2>&1; then
          echo "BoxLite sandbox recovery failed after Agent update" >>"$LOG_FILE"
        fi
        resume_session_worker
      fi
      if [ "$UPDATE_OK" = true ]; then
        complete_agent_tool_job "$JOB_ID" true "Agent 工具更新完成" "$RESULT_FILE" "$ACTION"
      else
        MESSAGE=$(redact_job_output "$JOB_FILE" "$LOG_FILE" "$JOB_VARIABLE_SNAPSHOT" 3500)
        if [ -z "$MESSAGE" ] || worker_output_was_withheld "$MESSAGE"; then
          MESSAGE="部分 Agent 工具更新失败；详细命令输出仅保留在 Worker 本机"
        fi
        complete_agent_tool_job "$JOB_ID" false "$MESSAGE" "$RESULT_FILE" "$ACTION" \
          sandbox_agent_tools_update_failed
      fi
      rm -f "$RESULT_FILE" "$LOG_FILE"
      ;;
    update-worker)
      LOG_FILE=$(mktemp)
      if ! update_worker "$JOB_FILE" 2>"$LOG_FILE"; then
        MESSAGE=$(redact_job_output "$JOB_FILE" "$LOG_FILE" "$JOB_VARIABLE_SNAPSHOT" 3500)
        if [ -z "$MESSAGE" ] || worker_output_was_withheld "$MESSAGE"; then
          MESSAGE="Worker 更新失败；详细命令输出仅保留在 Worker 本机"
        fi
        if [ -e "$STATE_DIR/worker-update.json" ]; then
          # Activation may have failed after the durable journal was created.
          # Leave the lease unresolved so the finalizer can restore and report
          # the exact rollback result instead of racing a generic completion.
          printf '%s\n' "$MESSAGE" >&2
        else
          complete_job_failure "$JOB_ID" worker_update_failed worker-update true "$MESSAGE" "$ACTION"
        fi
      fi
      rm -f "$LOG_FILE"
      ;;
    configure-sandbox-proxy)
      LOG_FILE=$(mktemp)
      OPERATION_OUTPUT=$(mktemp)
      PROXY_OK=false
      if apply_sandbox_proxy "$JOB_FILE" >"$OPERATION_OUTPUT" 2>"$LOG_FILE"; then
        if EXTERNAL_ID=$(sandbox_container_name "$JOB_FILE"); then
          PROXY_OK=true
        else
          echo 'stage proxy-config failed: sandbox external id is invalid' >> "$LOG_FILE"
        fi
      fi
      if [ "$PROXY_OK" = true ]; then
        if jq -e '.job.payload.proxy? and (.job.payload.proxy.url // "") != ""' "$JOB_FILE" >/dev/null; then
          MESSAGE="沙箱网络代理已应用；新的 Agent 和终端进程将使用该代理"
        else
          MESSAGE="沙箱已切换为直连；新的 Agent 和终端进程将不再使用代理"
        fi
        complete_job "$JOB_ID" true "$EXTERNAL_ID" "$MESSAGE"
      else
        MESSAGE=$(redact_job_output "$JOB_FILE" "$LOG_FILE" "$JOB_VARIABLE_SNAPSHOT" 3500)
        if [ -z "$MESSAGE" ] || worker_output_was_withheld "$MESSAGE"; then
          MESSAGE="沙箱网络出口应用失败；详细命令输出仅保留在 Worker 本机"
        fi
        complete_job_failure "$JOB_ID" sandbox_proxy_apply_failed proxy-config true "$MESSAGE" "$ACTION"
      fi
      rm -f "$LOG_FILE" "$OPERATION_OUTPUT"
      ;;
    create-sandbox)
      process_create_job
      ;;
    start-sandbox|stop-sandbox|restart-sandbox|delete-sandbox)
      LOG_FILE=$(mktemp)
      OPERATION_OUTPUT=$(mktemp)
      case "$ACTION" in
        start-sandbox) OPERATION=start_sandbox ;;
        stop-sandbox) OPERATION=stop_sandbox ;;
        restart-sandbox) OPERATION=restart_sandbox ;;
        delete-sandbox) OPERATION=delete_sandbox ;;
      esac
      OPERATION_READY=true
      OPERATION_SAFE_FAILURE_MESSAGE=
      case "$ACTION" in
        start-sandbox|restart-sandbox)
          if [ "$JOB_VARIABLE_SNAPSHOT_READY" != true ]; then
            cat "$JOB_VARIABLE_SNAPSHOT_ERROR" > "$LOG_FILE"
            echo 'stage variables failed: could not resolve AgentBox variable references' >> "$LOG_FILE"
            OPERATION_READY=false
            OPERATION_SAFE_FAILURE_MESSAGE='stage variables failed: could not resolve AgentBox variable references'
          fi
          ;;
      esac
      OPERATION_OK=false
      if [ "$OPERATION_READY" = true ] &&
         "$OPERATION" "$JOB_FILE" "$JOB_VARIABLE_SNAPSHOT" >"$OPERATION_OUTPUT" 2>"$LOG_FILE"; then
        OPERATION_OK=true
        if [ "$ACTION" = delete-sandbox ]; then
          EXTERNAL_ID=
        elif ! EXTERNAL_ID=$(sandbox_container_name "$JOB_FILE"); then
          echo "stage ${ACTION%-sandbox} failed: sandbox external id is invalid" >> "$LOG_FILE"
          OPERATION_OK=false
        fi
      fi
      if [ "$OPERATION_OK" = true ]; then
        case "$ACTION" in
          start-sandbox) MESSAGE="Sandbox started" ;;
          stop-sandbox) MESSAGE="Sandbox stopped" ;;
          restart-sandbox) MESSAGE="Sandbox restarted" ;;
          delete-sandbox) MESSAGE="Sandbox deleted" ;;
        esac
        complete_job "$JOB_ID" true "$EXTERNAL_ID" "$MESSAGE"
      else
        if [ -n "$OPERATION_SAFE_FAILURE_MESSAGE" ]; then
          MESSAGE=$OPERATION_SAFE_FAILURE_MESSAGE
          ERROR_STAGE=variables
        else
          ERROR_STAGE=$(worker_error_stage_from_log "$LOG_FILE" || true)
          [ -n "$ERROR_STAGE" ] || ERROR_STAGE=${ACTION%-sandbox}
          MESSAGE=$(redact_job_output "$JOB_FILE" "$LOG_FILE" "$JOB_VARIABLE_SNAPSHOT" 3500)
          if [ -z "$MESSAGE" ] || worker_output_was_withheld "$MESSAGE"; then
            MESSAGE=$(worker_safe_stage_message "$ERROR_STAGE")
          fi
        fi
        ERROR_CODE=$(printf '%s_failed' "$ACTION" | tr '-' '_')
        complete_job_failure "$JOB_ID" "$ERROR_CODE" "$ERROR_STAGE" false "$MESSAGE" "$ACTION"
      fi
      rm -f "$LOG_FILE" "$OPERATION_OUTPUT"
      ;;
    *) complete_job_failure "$JOB_ID" worker_action_unsupported dispatch false \
         "Unsupported worker action: $ACTION" "$ACTION" ;;
  esac
  case "$ACTION" in
    create-sandbox|start-sandbox|restart-sandbox) mark_worker_inventory_dirty || true ;;
  esac
  rm -f "$JOB_VARIABLE_SNAPSHOT" "$JOB_VARIABLE_SNAPSHOT_ERROR"
}

mark_worker_inventory_dirty() {
  INVENTORY_MARKER=$(mktemp "$STATE_DIR/inventory-generation.XXXXXX") || return 1
  printf '%s\n' "$INVENTORY_MARKER" > "$INVENTORY_MARKER"
  mv "$INVENTORY_MARKER" "$STATE_DIR/inventory-generation"
}

heartbeat_loop() {
  INVENTORY_REPORTED_AT=0
  INVENTORY_REPORTED_GENERATION=
  while :; do
    CAPS=$(worker_capabilities)
    HEARTBEAT_NOW=$(date +%s)
    INVENTORY_GENERATION=$(cat "$STATE_DIR/inventory-generation" 2>/dev/null || true)
    INVENTORY=null
    if [ "$INVENTORY_REPORTED_AT" -eq 0 ] ||
       [ "$((HEARTBEAT_NOW - INVENTORY_REPORTED_AT))" -ge 120 ] ||
       [ "$INVENTORY_GENERATION" != "$INVENTORY_REPORTED_GENERATION" ]; then
      INVENTORY=$(worker_inventory "$CAPS") || INVENTORY=null
    fi
    HEARTBEAT=$(jq -n --argjson capabilities "$CAPS" --argjson inventory "$INVENTORY" \
      --arg workerVersion "$AGENTBOX_WORKER_VERSION" \
      '{capabilities:$capabilities,workerVersion:$workerVersion}
       + (if $inventory == null then {} else {inventory:$inventory} end)')
    HEARTBEAT_SENT=true
    if ! worker_origin_curl -fsS -X POST "$SERVER_URL/api/servers/$SERVER_ID/heartbeat" \
      -H "Authorization: Bearer $CREDENTIAL" \
      -H 'Content-Type: application/json' --data "$HEARTBEAT" >/dev/null 2>&1; then
      LEGACY_HEARTBEAT=$(printf '%s' "$HEARTBEAT" | jq 'del(.workerVersion)')
      worker_origin_curl -fsS -X POST "$SERVER_URL/api/servers/$SERVER_ID/heartbeat" \
        -H "Authorization: Bearer $CREDENTIAL" \
        -H 'Content-Type: application/json' --data "$LEGACY_HEARTBEAT" >/dev/null 2>&1 || HEARTBEAT_SENT=false
    fi
    if [ "$HEARTBEAT_SENT" = true ] && [ "$INVENTORY" != null ]; then
      INVENTORY_REPORTED_AT=$(date +%s)
      # A job may finish during collection or reporting. Acknowledge only the
      # generation read before collection, leaving newer invalidations pending.
      INVENTORY_REPORTED_GENERATION=$INVENTORY_GENERATION
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
  SERVER_URL=$(normalize_worker_origin "$SERVER_URL") || exit 1
  [ -n "$SERVER_ID" ] && [ -n "$CREDENTIAL" ] || { echo "worker configuration is invalid" >&2; exit 1; }
  # Confirm a freshly installed Worker before any potentially slow runtime
  # discovery. The rollback timer is intentionally short so a broken binary
  # cannot leave the server unmanaged.
  FINALIZE_STATUS=0
  finalize_worker_update || FINALIZE_STATUS=$?
  [ "$FINALIZE_STATUS" -ne 75 ] || exit 0
  prepare_boxlite_config
  AGENTBOX_BOXLITE_SERVER_MODE=1
  export AGENTBOX_BOXLITE_SERVER_MODE
  ensure_boxlite_server || echo "warning: BoxLite service is unavailable" >&2
  heartbeat_loop &
  HEARTBEAT_PID=$!
  SESSION_PID=
  ensure_session_worker() {
    SESSION_STATE=
    if [ -n "$SESSION_PID" ] && kill -0 "$SESSION_PID" >/dev/null 2>&1; then
      SESSION_STATE=$(ps -o stat= -p "$SESSION_PID" 2>/dev/null | tr -d ' ' || true)
    fi
    case "$SESSION_STATE" in
      ''|Z*) ;;
      *) return 0 ;;
    esac
    if [ -n "$SESSION_PID" ]; then
      wait "$SESSION_PID" >/dev/null 2>&1 || true
      SESSION_PID=
    fi
    if [ -x /usr/local/bin/agentbox-worker ]; then
      /usr/local/bin/agentbox-worker session "$CONFIG" &
      SESSION_PID=$!
    else
      return 0
    fi
  }
  pause_session_worker() {
    [ -n "$SESSION_PID" ] || return 0
    kill "$SESSION_PID" >/dev/null 2>&1 || true
    SESSION_STOP_ATTEMPTS=0
    while [ "$SESSION_STOP_ATTEMPTS" -lt 50 ]; do
      SESSION_STATE=$(ps -o stat= -p "$SESSION_PID" 2>/dev/null | tr -d ' ' || true)
      case "$SESSION_STATE" in
        ''|Z*) break ;;
      esac
      SESSION_STOP_ATTEMPTS=$((SESSION_STOP_ATTEMPTS + 1))
      sleep 0.1
    done
    case "$SESSION_STATE" in
      ''|Z*) ;;
      *)
        kill -KILL "$SESSION_PID" >/dev/null 2>&1 || true
        SESSION_STOP_ATTEMPTS=0
        while [ "$SESSION_STOP_ATTEMPTS" -lt 20 ]; do
          SESSION_STATE=$(ps -o stat= -p "$SESSION_PID" 2>/dev/null | tr -d ' ' || true)
          case "$SESSION_STATE" in
            ''|Z*) break ;;
          esac
          SESSION_STOP_ATTEMPTS=$((SESSION_STOP_ATTEMPTS + 1))
          sleep 0.1
        done
        case "$SESSION_STATE" in ''|Z*) ;; *) return 1 ;; esac
        ;;
    esac
    wait "$SESSION_PID" >/dev/null 2>&1 || true
    SESSION_PID=
  }
  resume_session_worker() {
    ensure_session_worker
  }
  [ -e "$STATE_DIR/worker-update.json" ] || ensure_session_worker
  cleanup_worker() {
    kill "$HEARTBEAT_PID" >/dev/null 2>&1 || true
    [ -z "$SESSION_PID" ] || kill "$SESSION_PID" >/dev/null 2>&1 || true
    stop_boxlite_server
    wait "$HEARTBEAT_PID" >/dev/null 2>&1 || true
    [ -z "$SESSION_PID" ] || wait "$SESSION_PID" >/dev/null 2>&1 || true
  }
  trap cleanup_worker EXIT
  trap 'exit 0' INT TERM
  recover_create_jobs
  CREATE_RECOVERY_AT=$(date +%s)
  # Image discovery can take tens of seconds on a busy BoxLite host. Keep the
  # heartbeat and interactive session available while the inventory refreshes.
  refresh_boxlite_images || {
    [ -s "$BOXLITE_IMAGES_FILE" ] || printf '[]\n' > "$BOXLITE_IMAGES_FILE"
    echo "warning: BoxLite image inventory is unavailable" >&2
  }
  mark_worker_inventory_dirty || true
  while :; do
    FINALIZE_STATUS=0
    finalize_worker_update || FINALIZE_STATUS=$?
    [ "$FINALIZE_STATUS" -ne 75 ] || exit 0
    if [ -e "$STATE_DIR/worker-update.json" ]; then
      # Heartbeats run independently, but no new job may cross an unresolved
      # update journal or observe a mixed Worker/driver generation.
      if ! worker_update_marker_valid "$STATE_DIR/worker-update.json" &&
          [ ! -e "$STATE_DIR/worker-update-degraded-warned" ]; then
        echo "ERROR: Worker update journal is corrupt; claims are blocked. Inspect it, then run: agentbox-worker recover-update --restore-previous" >&2
        : > "$STATE_DIR/worker-update-degraded-warned"
      fi
      sleep 2
      continue
    fi
    rm -f "$STATE_DIR/worker-update-degraded-warned"
    ensure_session_worker
    [ "${AGENTBOX_BOXLITE_SERVER_MODE:-}" != 1 ] || ensure_boxlite_server || true
    if [ "$(($(date +%s) - CREATE_RECOVERY_AT))" -ge 15 ]; then
      recover_create_jobs
      CREATE_RECOVERY_AT=$(date +%s)
    fi
    JOB_FILE=$(mktemp)
    HTTP_STATUS=$(worker_request "$JOB_FILE" -X POST \
      "$SERVER_URL/api/servers/$SERVER_ID/jobs/claim" \
      -H "Authorization: Bearer $CREDENTIAL" || true)
    if [ "$HTTP_STATUS" = 200 ]; then
      process_job "$JOB_FILE" || true
    fi
    rm -f "$JOB_FILE"
    if [ "$HTTP_STATUS" = 426 ]; then
      sleep 30
    else
      sleep 1
    fi
  done
}

case "${1:-}" in
  setup) setup_worker "$@" ;;
  run) run_worker ;;
  status) systemctl status agentbox-worker.service ;;
  recover-update) shift; recover_worker_update "$@" ;;
  *) usage ;;
esac
