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

const workerInstall = `#!/bin/sh
set -eu

SERVER_URL=${1:-}
case "$SERVER_URL" in
  http://*|https://*) ;;
  *) echo "usage: install.sh <agentbox-url>" >&2; exit 2 ;;
esac

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL "$SERVER_URL/api/worker/agentbox-worker" -o "$tmp"
install -m 0755 "$tmp" /usr/local/bin/agentbox-worker
echo "AgentBox Worker installed."
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
  CAPS=''
  command -v docker >/dev/null && CAPS='"docker"'
  if [ -c /dev/kvm ]; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"kvm-device\""
  fi
  if command -v qemu-system-x86_64 >/dev/null && command -v qemu-img >/dev/null; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"qemu\""
  fi
  if command -v virsh >/dev/null; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"libvirt\""
  fi
  BODY=$(printf '{"pairingToken":"%s","serverId":"%s","name":"%s","hostname":"%s","os":"linux","arch":"%s","capabilities":[%s]}' "$TOKEN" "$SERVER_ID" "$HOST" "$HOST" "$ARCH" "$CAPS")
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

worker_capabilities() {
  CAPS=
  command -v docker >/dev/null && CAPS='"docker"'
  if [ -c /dev/kvm ]; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"kvm-device\""
  fi
  if command -v qemu-system-x86_64 >/dev/null && command -v qemu-img >/dev/null; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"qemu\""
  fi
  if command -v virsh >/dev/null; then
    [ -n "$CAPS" ] && CAPS="$CAPS,"
    CAPS="$CAPS\"libvirt\""
  fi
  printf '[%s]' "$CAPS"
}

worker_inventory() {
  ARCH=$(uname -m)
  [ "$ARCH" = x86_64 ] && ARCH=amd64
  [ "$ARCH" = aarch64 ] && ARCH=arm64
  DOCKER_IMAGES='[]'
  if command -v docker >/dev/null; then
    DOCKER_IMAGES=$(docker image ls --no-trunc --format '{{json .}}' 2>/dev/null | jq -s --arg arch "$ARCH" '
      map({
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

install_agent_tools() {
  CONTAINER=$1
  JOB_FILE=$2
  TOOLS=$(jq -r '.job.payload.agentTools[]?' "$JOB_FILE")
  [ -n "$TOOLS" ] || return 0
  docker exec "$CONTAINER" sh -lc \
    'export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get install -y curl ca-certificates git; curl -fsSL https://deb.nodesource.com/setup_22.x | sh; apt-get install -y nodejs' >&2
  printf '%s\n' "$TOOLS" | while IFS= read -r TOOL; do
    case "$TOOL" in
      codex) PACKAGE='@openai/codex' ;;
      claude-code) PACKAGE='@anthropic-ai/claude-code' ;;
      gemini-cli) PACKAGE='@google/gemini-cli' ;;
      opencode) PACKAGE='opencode-ai' ;;
      *) echo "unsupported Agent tool: $TOOL" >&2; return 1 ;;
    esac
    docker exec "$CONTAINER" npm install -g "$PACKAGE" >&2
  done
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
    case "$PROVIDER" in
      openai)
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
      google)
        grep -q '^GEMINI_API_KEY=' "$ENV_FILE" || {
          append_env "$ENV_FILE" GEMINI_API_KEY "$SECRET"
          [ -z "$ENDPOINT" ] || append_env "$ENV_FILE" GOOGLE_GEMINI_BASE_URL "$ENDPOINT"
          [ -z "$MODEL" ] || append_env "$ENV_FILE" GEMINI_MODEL "$MODEL"
        }
        ;;
      deepseek)
        grep -q '^DEEPSEEK_API_KEY=' "$ENV_FILE" || {
          append_env "$ENV_FILE" DEEPSEEK_API_KEY "$SECRET"
          [ -z "$ENDPOINT" ] || append_env "$ENV_FILE" DEEPSEEK_BASE_URL "$ENDPOINT"
          [ -z "$MODEL" ] || append_env "$ENV_FILE" DEEPSEEK_MODEL "$MODEL"
        }
        ;;
    esac
  done
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
    if [ -n "$CODEX_ENDPOINT" ]; then
      printf 'model_provider = "agentbox"\n\n[model_providers.agentbox]\n' >> "$CODEX_CONFIG"
      printf 'name = "AgentBox"\nbase_url = %s\nenv_key = "AGENTBOX_KEY_%s"\nwire_api = "responses"\n' \
        "$(printf '%s' "$CODEX_ENDPOINT" | jq -Rs .)" "$CODEX_ENV_ID" >> "$CODEX_CONFIG"
    fi
    docker exec "$CONTAINER" mkdir -p /root/.codex
    docker exec -i "$CONTAINER" sh -c 'umask 077; cat > /root/.codex/config.toml' < "$CODEX_CONFIG"
    rm -f "$CODEX_CONFIG"
  fi

  if jq -e '.job.payload.agentTools | index("gemini-cli")' "$JOB_FILE" >/dev/null; then
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
        .[$credential.id] = ({
          npm: (if $credential.protocol == "anthropic" then "@ai-sdk/anthropic"
                elif $credential.protocol == "gemini" then "@ai-sdk/google"
                else "@ai-sdk/openai-compatible" end),
          name: $credential.id,
          options: ({apiKey: ("{env:AGENTBOX_KEY_" + $envId + "}")}
            + (if ($credential.endpoint // "") == "" then {} else {baseURL: $credential.endpoint} end)),
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
      "/root/.codex/skills/$ID" \
      "/root/.claude/skills/$ID" \
      "/root/.gemini/skills/$ID" \
      "/root/.config/opencode/skill/$ID"; do
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
    case "$TOOL" in
      codex) COMMAND=codex ;;
      claude-code) COMMAND=claude ;;
      gemini-cli) COMMAND=gemini ;;
      opencode) COMMAND=opencode ;;
      *) continue ;;
    esac
    docker exec "$CONTAINER" test -x "/usr/bin/$COMMAND" || continue
    WRAPPER=$(mktemp)
    printf '#!/bin/sh\nset -a\n. /opt/agentbox/secrets/agentbox.env\nset +a\nexec /usr/bin/%s "$@"\n' "$COMMAND" > "$WRAPPER"
    docker exec -i "$CONTAINER" sh -c "cat > /usr/local/bin/$COMMAND && chmod 0755 /usr/local/bin/$COMMAND" < "$WRAPPER"
    rm -f "$WRAPPER"
  done
}

create_sandbox() {
  JOB_FILE=$1
  SANDBOX_ID=$(jq -r '.job.payload.sandboxId' "$JOB_FILE")
  DRIVER=$(jq -r '.job.payload.driver' "$JOB_FILE")
  [ "$DRIVER" = docker ] || {
    echo "VM backend is not installed on this worker" >&2
    return 1
  }
  command -v docker >/dev/null || { echo "Docker is not installed" >&2; return 1; }
  IMAGE=$(jq -r '.job.payload.image' "$JOB_FILE")
  WORKDIR=$(jq -r '.job.payload.workdir // "/workspace"' "$JOB_FILE")
  CPU=$(jq -r '.job.payload.cpu // empty' "$JOB_FILE")
  MEMORY=$(jq -r '.job.payload.memory // empty' "$JOB_FILE" | tr -d ' ' | sed -e 's/GiB$/g/' -e 's/MiB$/m/')
  NETWORK=$(jq -r '.job.payload.network // "restricted"' "$JOB_FILE")
  SETUP=$(jq -r '.job.payload.setup // empty' "$JOB_FILE")
  CONTAINER="agentbox-$SANDBOX_ID"
  VOLUME="agentbox-$SANDBOX_ID-workspace"

  if docker inspect "$CONTAINER" >/dev/null 2>&1; then
    OWNER=$(docker inspect -f '{{ index .Config.Labels "agentbox.sandbox" }}' "$CONTAINER")
    [ "$OWNER" = "$SANDBOX_ID" ] || { echo "container name collision: $CONTAINER" >&2; return 1; }
    docker start "$CONTAINER" >/dev/null
  else
    set -- docker run -d --name "$CONTAINER" \
      --label "agentbox.sandbox=$SANDBOX_ID" \
      --restart unless-stopped --workdir "$WORKDIR" \
      -v "$VOLUME:$WORKDIR"
    [ -n "$CPU" ] && set -- "$@" --cpus "$CPU"
    [ -n "$MEMORY" ] && set -- "$@" --memory "$MEMORY"
    [ "$NETWORK" = none ] && set -- "$@" --network none
    set -- "$@" "$IMAGE" sh -lc 'trap : TERM INT; sleep infinity & wait'
    "$@" >/dev/null
  fi

  docker exec "$CONTAINER" mkdir -p /opt/agentbox
  MANIFEST=$(mktemp)
  jq '.job.payload | del(.credentials)' "$JOB_FILE" > "$MANIFEST"
  docker cp "$MANIFEST" "$CONTAINER:/opt/agentbox/manifest.json" >/dev/null
  rm -f "$MANIFEST"
  install_agent_tools "$CONTAINER" "$JOB_FILE"
  configure_credentials "$CONTAINER" "$JOB_FILE"
  configure_skills "$CONTAINER" "$JOB_FILE"
  configure_mcp_servers "$CONTAINER" "$JOB_FILE"
  install_agent_wrappers "$CONTAINER" "$JOB_FILE"
  [ -z "$SETUP" ] || docker exec "$CONTAINER" sh -lc "$SETUP" >&2
  printf '%s' "$CONTAINER"
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
  CONTAINER=$(sandbox_container_name "$1")
  docker inspect "$CONTAINER" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
  docker start "$CONTAINER" >/dev/null
  printf '%s' "$CONTAINER"
}

stop_sandbox() {
  CONTAINER=$(sandbox_container_name "$1")
  docker inspect "$CONTAINER" >/dev/null 2>&1 || { echo "sandbox container not found" >&2; return 1; }
  docker stop --time 10 "$CONTAINER" >/dev/null
  printf '%s' "$CONTAINER"
}

delete_sandbox() {
  JOB_FILE=$1
  SANDBOX_ID=$(jq -r '.job.payload.sandboxId' "$JOB_FILE")
  CONTAINER=$(sandbox_container_name "$JOB_FILE")
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "agentbox-$SANDBOX_ID-workspace" >/dev/null 2>&1 || true
}

login_agent() {
  JOB_FILE=$1
  CONTAINER=$(sandbox_container_name "$JOB_FILE")
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
    start-sandbox|stop-sandbox|delete-sandbox)
      LOG_FILE=$(mktemp)
      case "$ACTION" in
        start-sandbox) OPERATION=start_sandbox ;;
        stop-sandbox) OPERATION=stop_sandbox ;;
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
  trap 'kill "$HEARTBEAT_PID" >/dev/null 2>&1 || true' EXIT INT TERM
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
