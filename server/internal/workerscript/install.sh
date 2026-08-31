#!/bin/sh
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

require github.com/superradcompany/microsandbox/sdk/go v0.6.15
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
