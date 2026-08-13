CREATE TABLE IF NOT EXISTS agents (
  id UUID PRIMARY KEY,
  project_id TEXT NOT NULL,
  name TEXT NOT NULL,
  slug TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  avatar TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  model_id TEXT NOT NULL,
  credential_id TEXT,
  system_prompt TEXT NOT NULL,
  skill_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
  mcp_server_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
  temperature DOUBLE PRECISION NOT NULL DEFAULT 0.4,
  max_steps INTEGER NOT NULL DEFAULT 12,
  status TEXT NOT NULL CHECK (status IN ('draft', 'active', 'archived')),
  version INTEGER NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_revisions (
  id BIGSERIAL PRIMARY KEY,
  agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  snapshot JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (agent_id, version)
);

CREATE INDEX IF NOT EXISTS idx_agents_project_status
  ON agents(project_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS control_resources (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN (
    'project', 'image', 'runtime', 'skill', 'mcp', 'sandbox', 'schedule', 'webhook', 'variable'
  )),
  project_id TEXT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  spec JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_control_resources_kind_project
  ON control_resources(kind, project_id, updated_at DESC);

ALTER TABLE control_resources DROP CONSTRAINT IF EXISTS control_resources_kind_check;
ALTER TABLE control_resources ADD CONSTRAINT control_resources_kind_check CHECK (kind IN (
  'project', 'image', 'runtime', 'skill', 'mcp', 'sandbox', 'schedule', 'webhook', 'variable'
));

CREATE TABLE IF NOT EXISTS managed_servers (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  hostname TEXT NOT NULL,
  os TEXT NOT NULL,
  arch TEXT NOT NULL,
  capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
  inventory JSONB NOT NULL DEFAULT '{"dockerImages":[],"vmImages":[],"vmImageDirectory":"/var/lib/agentbox/vm-images"}'::jsonb,
  credential_hash BYTEA NOT NULL UNIQUE,
  last_seen_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE managed_servers ADD COLUMN IF NOT EXISTS inventory JSONB NOT NULL
  DEFAULT '{"dockerImages":[],"vmImages":[],"vmImageDirectory":"/var/lib/agentbox/vm-images"}'::jsonb;

CREATE TABLE IF NOT EXISTS server_pairings (
  id UUID PRIMARY KEY,
  token_hash BYTEA NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  server_id UUID REFERENCES managed_servers(id) ON DELETE SET NULL,
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_server_pairings_expiry
  ON server_pairings(expires_at);

CREATE TABLE IF NOT EXISTS provider_credentials (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  protocol TEXT NOT NULL DEFAULT 'openai-chat',
  endpoint TEXT NOT NULL DEFAULT '',
  model_id TEXT NOT NULL DEFAULT '',
  models JSONB NOT NULL DEFAULT '[]'::jsonb,
  secret_ciphertext BYTEA NOT NULL,
  secret_nonce BYTEA NOT NULL,
  secret_last_four TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  last_check_at TIMESTAMPTZ,
  last_check_ok BOOLEAN,
  last_check_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE provider_credentials ADD COLUMN IF NOT EXISTS protocol TEXT NOT NULL DEFAULT 'openai-chat';
ALTER TABLE provider_credentials ADD COLUMN IF NOT EXISTS model_id TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_credentials ADD COLUMN IF NOT EXISTS models JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE provider_credentials ADD COLUMN IF NOT EXISTS last_check_at TIMESTAMPTZ;
ALTER TABLE provider_credentials ADD COLUMN IF NOT EXISTS last_check_ok BOOLEAN;
ALTER TABLE provider_credentials ADD COLUMN IF NOT EXISTS last_check_error TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_provider_credentials_provider
  ON provider_credentials(provider_id, enabled, updated_at DESC);

CREATE TABLE IF NOT EXISTS worker_jobs (
  id UUID PRIMARY KEY,
  server_id UUID NOT NULL REFERENCES managed_servers(id) ON DELETE CASCADE,
  resource_id TEXT NOT NULL REFERENCES control_resources(id) ON DELETE CASCADE,
  action TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'leased', 'succeeded', 'failed')),
  payload JSONB NOT NULL,
  lease_until TIMESTAMPTZ,
  attempts INTEGER NOT NULL DEFAULT 0,
  result_message TEXT NOT NULL DEFAULT '',
  external_id TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_worker_jobs_claim
  ON worker_jobs(server_id, status, created_at);

CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT NOT NULL UNIQUE,
  password_hash BYTEA NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
  status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
  preferences JSONB NOT NULL DEFAULT '{"successNotifications":true,"density":"comfortable","showCapabilities":true,"showInfrastructure":true,"showGovernance":true}'::jsonb,
  last_login_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_status_updated
  ON users(status, updated_at DESC);

ALTER TABLE users ADD COLUMN IF NOT EXISTS preferences JSONB NOT NULL
  DEFAULT '{"successNotifications":true,"density":"comfortable","showCapabilities":true,"showInfrastructure":true,"showGovernance":true}'::jsonb;

CREATE TABLE IF NOT EXISTS user_sessions (
  token_hash BYTEA PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_expiry
  ON user_sessions(expires_at);

ALTER TABLE agents ADD COLUMN IF NOT EXISTS runtime_id TEXT NOT NULL DEFAULT 'docker-agent';
ALTER TABLE agents ALTER COLUMN runtime_id SET DEFAULT 'docker-agent';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS variable_ids JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS custom_args JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS concurrency INTEGER NOT NULL DEFAULT 1;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS sandbox_policy TEXT NOT NULL DEFAULT 'new';

UPDATE agents SET variable_ids = '[]'::jsonb WHERE variable_ids = 'null'::jsonb;
UPDATE agents SET custom_args = '[]'::jsonb WHERE custom_args = 'null'::jsonb;

UPDATE agents
SET runtime_id = 'docker-agent'
WHERE runtime_id IN (
  SELECT id FROM control_resources WHERE kind = 'runtime' AND spec->>'driver' = 'process'
);

UPDATE agent_revisions
SET snapshot = jsonb_set(snapshot, '{runtimeId}', to_jsonb('docker-agent'::text), TRUE)
WHERE snapshot->>'runtimeId' IN (
  SELECT id FROM control_resources WHERE kind = 'runtime' AND spec->>'driver' = 'process'
);

UPDATE control_resources
SET spec = jsonb_set(spec, '{runtimeId}', to_jsonb('docker-agent'::text), TRUE)
WHERE kind = 'sandbox' AND spec->>'runtimeId' IN (
  SELECT id FROM control_resources WHERE kind = 'runtime' AND spec->>'driver' = 'process'
);

DELETE FROM control_resources
WHERE kind = 'runtime' AND spec->>'driver' = 'process';

UPDATE managed_servers
SET capabilities = capabilities - 'process'
WHERE capabilities ? 'process';

INSERT INTO control_resources
  (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
SELECT
  CASE
    WHEN spec->>'image' = 'ubuntu:24.04' THEN 'image-ubuntu-2404'
    ELSE 'image-' || substr(md5(spec->>'image'), 1, 12)
  END,
  'image',
  NULL,
  CASE
    WHEN spec->>'image' = 'ubuntu:24.04' THEN 'Ubuntu 24.04'
    ELSE left(spec->>'image', 80)
  END,
  'Migrated from an existing Runtime image reference',
  TRUE,
  jsonb_build_object(
    'reference', spec->>'image',
    'architecture', 'all',
    'modes', CASE
      WHEN spec->>'image' = 'ubuntu:24.04'
        THEN '["docker", "vm"]'::jsonb
      WHEN bool_or(spec->>'driver' = 'docker') AND bool_or(spec->>'driver' IN ('boxlite', 'microsandbox'))
        THEN '["docker", "vm"]'::jsonb
      WHEN bool_or(spec->>'driver' IN ('boxlite', 'microsandbox'))
        THEN '["vm"]'::jsonb
      ELSE '["docker"]'::jsonb
    END
  ),
  min(created_at),
  max(updated_at)
FROM control_resources
WHERE kind = 'runtime' AND COALESCE(spec->>'image', '') <> ''
GROUP BY spec->>'image'
ON CONFLICT (id) DO NOTHING;

UPDATE control_resources
SET spec = (spec - 'image') || jsonb_build_object(
  'imageId', CASE
    WHEN spec->>'image' = 'ubuntu:24.04' THEN 'image-ubuntu-2404'
    ELSE 'image-' || substr(md5(spec->>'image'), 1, 12)
  END
)
WHERE kind = 'runtime' AND COALESCE(spec->>'image', '') <> '';

UPDATE control_resources
SET spec = jsonb_set(spec, '{driver}', to_jsonb('vm'::text), TRUE)
WHERE kind = 'runtime' AND spec->>'driver' IN ('boxlite', 'microsandbox');

UPDATE control_resources
SET spec = jsonb_set(spec, '{imageId}', to_jsonb('image-ubuntu-2404'::text), TRUE)
WHERE kind = 'runtime'
  AND COALESCE(spec->>'imageId', '') = ''
  AND EXISTS (
    SELECT 1 FROM control_resources image
    WHERE image.id = 'image-ubuntu-2404' AND image.kind = 'image'
  );

UPDATE control_resources
SET spec = spec || jsonb_build_object(
  'agentTools', COALESCE(spec->'agentTools', '[]'::jsonb),
  'skillIds', COALESCE(spec->'skillIds', '[]'::jsonb),
  'mcpServerIds', COALESCE(spec->'mcpServerIds', '[]'::jsonb),
  'variableIds', COALESCE(spec->'variableIds', '[]'::jsonb),
  'credentialIds', COALESCE(spec->'credentialIds', '[]'::jsonb)
)
WHERE kind = 'runtime';

UPDATE control_resources runtime
SET spec = runtime.spec || jsonb_build_object('imageReference', image.spec->>'reference')
FROM control_resources image
WHERE runtime.kind = 'runtime'
  AND image.kind = 'image'
  AND runtime.spec->>'imageId' = image.id
  AND COALESCE(runtime.spec->>'imageReference', '') = '';

UPDATE control_resources
SET
  name = 'Codex 开发环境',
  description = '预装 Codex、Git 与常用 Agent 能力的标准 Linux 环境',
  spec = spec || jsonb_build_object(
    'agentTools', '["codex"]'::jsonb,
    'skillIds', '["code-review", "task-planner"]'::jsonb,
    'mcpServerIds', '["filesystem"]'::jsonb,
    'variableIds', '["github-token"]'::jsonb
  )
WHERE id = 'docker-agent'
  AND kind = 'runtime'
  AND name = 'Docker Agent'
  AND COALESCE(jsonb_array_length(spec->'agentTools'), 0) = 0;

UPDATE control_resources
SET description = 'Docker 与 VM 环境模板可复用的基础 OCI 镜像'
WHERE id = 'image-ubuntu-2404'
  AND kind = 'image'
  AND description = 'Docker 与 VM Runtime 可用的基础 OCI 镜像';

UPDATE control_resources
SET spec = jsonb_set(spec, '{url}', to_jsonb('runtime://' || id), TRUE)
WHERE kind = 'mcp' AND spec->>'transport' = 'http' AND COALESCE(spec->>'url', '') = '';

UPDATE control_resources
SET spec = spec || jsonb_build_object(
  'transport', 'stdio',
  'command', 'npx',
  'args', '-y @modelcontextprotocol/server-filesystem /workspace'
)
WHERE id = 'filesystem' AND kind = 'mcp'
  AND COALESCE(spec->>'command', '') IN ('', 'filesystem');

UPDATE control_resources
SET spec = spec || jsonb_build_object(
  'transport', 'stdio',
  'command', 'npx',
  'args', '-y @playwright/mcp@latest --headless'
)
WHERE id = 'browser' AND kind = 'mcp'
  AND COALESCE(spec->>'url', '') IN ('', 'runtime://browser');

UPDATE control_resources
SET enabled = FALSE,
    spec = spec || jsonb_build_object(
      'transport', 'http',
      'url', 'https://api.githubcopilot.com/mcp/',
      'headers', 'Authorization=env://GITHUB_TOKEN'
    )
WHERE id = 'github' AND kind = 'mcp'
  AND COALESCE(spec->>'url', '') IN ('', 'runtime://github');

UPDATE control_resources
SET spec = '{}'::jsonb
WHERE kind = 'project' AND spec <> '{}'::jsonb;
