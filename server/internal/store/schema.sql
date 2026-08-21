DROP TABLE IF EXISTS agent_revisions;
DROP TABLE IF EXISTS agents;

CREATE TABLE IF NOT EXISTS control_resources (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN (
    'project', 'image', 'runtime', 'skill', 'mcp', 'sandbox', 'variable'
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
DELETE FROM control_resources WHERE kind IN ('schedule', 'webhook');
ALTER TABLE control_resources ADD CONSTRAINT control_resources_kind_check CHECK (kind IN (
  'project', 'image', 'runtime', 'skill', 'mcp', 'sandbox', 'variable'
));

CREATE TABLE IF NOT EXISTS managed_servers (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  hostname TEXT NOT NULL,
  os TEXT NOT NULL,
  arch TEXT NOT NULL,
  capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
  inventory JSONB NOT NULL DEFAULT '{"dockerImages":[],"vmImages":[],"vmImageDirectory":"/var/lib/agentbox/vm-images"}'::jsonb,
  worker_version TEXT NOT NULL DEFAULT '',
  worker_update_status TEXT NOT NULL DEFAULT '',
  worker_update_target TEXT NOT NULL DEFAULT '',
  worker_update_message TEXT NOT NULL DEFAULT '',
  credential_hash BYTEA NOT NULL UNIQUE,
  last_seen_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE managed_servers ADD COLUMN IF NOT EXISTS inventory JSONB NOT NULL
  DEFAULT '{"dockerImages":[],"vmImages":[],"vmImageDirectory":"/var/lib/agentbox/vm-images"}'::jsonb;
ALTER TABLE managed_servers ADD COLUMN IF NOT EXISTS worker_version TEXT NOT NULL DEFAULT '';
ALTER TABLE managed_servers ADD COLUMN IF NOT EXISTS worker_update_status TEXT NOT NULL DEFAULT '';
ALTER TABLE managed_servers ADD COLUMN IF NOT EXISTS worker_update_target TEXT NOT NULL DEFAULT '';
ALTER TABLE managed_servers ADD COLUMN IF NOT EXISTS worker_update_message TEXT NOT NULL DEFAULT '';

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

-- Move legacy credential defaults to the sandbox that actually consumes them.
-- This is idempotent: existing explicit bindings are never overwritten.
UPDATE control_resources sandbox
SET spec = jsonb_set(
  sandbox.spec,
  '{modelBindings}',
  COALESCE((
    SELECT jsonb_object_agg(credential.id, credential.model_id)
    FROM provider_credentials credential
    WHERE credential.model_id <> ''
      AND sandbox.spec->'credentialIds' ? credential.id
  ), '{}'::jsonb),
  TRUE
)
WHERE sandbox.kind = 'sandbox' AND NOT (sandbox.spec ? 'modelBindings');

UPDATE provider_credentials SET model_id = '' WHERE model_id <> '';

CREATE INDEX IF NOT EXISTS idx_provider_credentials_provider
  ON provider_credentials(provider_id, enabled, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_proxies (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  scheme TEXT NOT NULL CHECK (scheme IN ('http', 'https')),
  host TEXT NOT NULL,
  port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
  username TEXT NOT NULL DEFAULT '',
  password_ciphertext BYTEA NOT NULL,
  password_nonce BYTEA NOT NULL,
  password_last_four TEXT NOT NULL DEFAULT '',
  no_proxy JSONB NOT NULL DEFAULT '[]'::jsonb,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_network_proxies_enabled
  ON network_proxies(enabled, updated_at DESC);

CREATE TABLE IF NOT EXISTS worker_jobs (
  id UUID PRIMARY KEY,
  server_id UUID NOT NULL REFERENCES managed_servers(id) ON DELETE CASCADE,
  resource_id TEXT REFERENCES control_resources(id) ON DELETE CASCADE,
  action TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('blocked', 'pending', 'leased', 'succeeded', 'failed')),
  payload JSONB NOT NULL,
  lease_until TIMESTAMPTZ,
  attempts INTEGER NOT NULL DEFAULT 0,
  result_message TEXT NOT NULL DEFAULT '',
  external_id TEXT NOT NULL DEFAULT '',
  result_exit_code INTEGER,
  result_output TEXT NOT NULL DEFAULT '',
  result_truncated BOOLEAN NOT NULL DEFAULT FALSE,
  result_timed_out BOOLEAN NOT NULL DEFAULT FALSE,
  automation_run_id UUID,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE worker_jobs ALTER COLUMN resource_id DROP NOT NULL;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS result_exit_code INTEGER;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS result_output TEXT NOT NULL DEFAULT '';
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS result_truncated BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS result_timed_out BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE worker_jobs ADD COLUMN IF NOT EXISTS automation_run_id UUID;
ALTER TABLE worker_jobs DROP CONSTRAINT IF EXISTS worker_jobs_status_check;
ALTER TABLE worker_jobs ADD CONSTRAINT worker_jobs_status_check
  CHECK (status IN ('blocked', 'pending', 'leased', 'succeeded', 'failed'));

CREATE INDEX IF NOT EXISTS idx_worker_jobs_claim
  ON worker_jobs(server_id, status, created_at);

DELETE FROM worker_jobs WHERE action = 'login-agent';

UPDATE control_resources
SET spec = spec - 'loginStatus' - 'loginMessage' - 'loginTool'
WHERE kind = 'sandbox'
  AND spec ?| ARRAY['loginStatus', 'loginMessage', 'loginTool'];

CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  username TEXT NOT NULL,
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

ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT;

WITH username_bases AS (
  SELECT id, created_at,
    CASE
      WHEN LENGTH(BTRIM(REGEXP_REPLACE(LOWER(SPLIT_PART(email, '@', 1)), '[^a-z0-9._-]+', '-', 'g'), '._-')) >= 3
        THEN LEFT(BTRIM(REGEXP_REPLACE(LOWER(SPLIT_PART(email, '@', 1)), '[^a-z0-9._-]+', '-', 'g'), '._-'), 64)
      ELSE 'user-' || LEFT(REPLACE(id::text, '-', ''), 8)
    END AS base
  FROM users
  WHERE username IS NULL OR BTRIM(username) = ''
), username_candidates AS (
  SELECT id, base,
    ROW_NUMBER() OVER (PARTITION BY base ORDER BY created_at, id) AS position
  FROM username_bases
), resolved_usernames AS (
  SELECT candidate.id,
    CASE
      WHEN candidate.position = 1 AND NOT EXISTS (
        SELECT 1 FROM users existing
        WHERE existing.username IS NOT NULL
          AND LOWER(existing.username) = candidate.base
      ) THEN candidate.base
      ELSE LEFT(candidate.base, 31) || '-' || REPLACE(candidate.id::text, '-', '')
    END AS username
  FROM username_candidates candidate
)
UPDATE users
SET username = resolved.username
FROM resolved_usernames resolved
WHERE users.id = resolved.id;

ALTER TABLE users ALTER COLUMN username SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_lower
  ON users(LOWER(username));

CREATE TABLE IF NOT EXISTS user_sessions (
  token_hash BYTEA PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_expiry
  ON user_sessions(expires_at);

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
      WHEN bool_or(spec->>'driver' = 'vm') AND bool_or(spec->>'driver' IN ('docker', 'boxlite', 'microsandbox'))
        THEN '["docker", "vm"]'::jsonb
      WHEN bool_or(spec->>'driver' IN ('boxlite', 'microsandbox'))
        THEN '["docker"]'::jsonb
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
  'environmentVariables', COALESCE(spec->'environmentVariables', '[]'::jsonb),
  'credentialIds', COALESCE(spec->'credentialIds', '[]'::jsonb)
)
WHERE kind = 'runtime';

UPDATE control_resources
SET spec = spec || jsonb_build_object(
  'environmentVariables', COALESCE(spec->'environmentVariables', '[]'::jsonb)
)
WHERE kind = 'sandbox';

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
    'variableIds', '[]'::jsonb
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

DELETE FROM control_resources runtime
WHERE runtime.id = 'docker-agent'
  AND runtime.kind = 'runtime'
  AND COALESCE(runtime.spec->>'serverId', '') = ''
  AND NOT EXISTS (
    SELECT 1
    FROM control_resources sandbox
    WHERE sandbox.kind = 'sandbox'
      AND sandbox.spec->>'runtimeId' = runtime.id
  );

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
      'url', 'https://api.githubcopilot.com/mcp/'
    )
WHERE id = 'github' AND kind = 'mcp'
  AND COALESCE(spec->>'url', '') IN ('', 'runtime://github');

UPDATE control_resources
SET spec = jsonb_set(
  spec,
  '{variableIds}',
  COALESCE(
    (
      SELECT jsonb_agg(variable_id)
      FROM jsonb_array_elements_text(COALESCE(spec->'variableIds', '[]'::jsonb)) AS variables(variable_id)
      WHERE variable_id <> 'github-token'
    ),
    '[]'::jsonb
  ),
  TRUE
)
WHERE kind IN ('runtime', 'sandbox')
  AND spec->'variableIds' ? 'github-token';

UPDATE control_resources
SET spec = spec - 'headers'
WHERE id = 'github'
  AND kind = 'mcp'
  AND spec->>'headers' = 'Authorization=env://GITHUB_TOKEN';

DELETE FROM control_resources
WHERE id = 'github-token'
  AND kind = 'variable'
  AND name = 'GITHUB_TOKEN'
  AND spec->>'reference' = 'env://GITHUB_TOKEN';

UPDATE control_resources
SET spec = '{}'::jsonb
WHERE kind = 'project' AND spec <> '{}'::jsonb;

CREATE TABLE IF NOT EXISTS automations (
  id UUID PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES control_resources(id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  trigger_type TEXT NOT NULL CHECK (trigger_type = 'webhook'),
  action_type TEXT NOT NULL CHECK (action_type = 'create-sandbox'),
  auth_mode TEXT NOT NULL CHECK (auth_mode IN ('bearer', 'hmac-sha256', 'github-sha256', 'gitlab-token', 'standard-webhooks')),
  endpoint_id UUID NOT NULL UNIQUE,
  secret_hash BYTEA NOT NULL,
  secret_ciphertext BYTEA NOT NULL,
  secret_nonce BYTEA NOT NULL,
  secret_last_four TEXT NOT NULL,
  template_id TEXT REFERENCES control_resources(id) ON DELETE RESTRICT,
  model_bindings JSONB NOT NULL DEFAULT '{}'::jsonb,
  input_template TEXT NOT NULL DEFAULT '{}',
  created_by UUID,
  updated_by UUID,
  last_triggered_at TIMESTAMPTZ,
  secret_rotated_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE automations ADD COLUMN IF NOT EXISTS input_template TEXT NOT NULL DEFAULT '{}';
ALTER TABLE automations ADD COLUMN IF NOT EXISTS model_bindings JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE automations ALTER COLUMN input_template SET DEFAULT '{}';
ALTER TABLE automations ALTER COLUMN template_id DROP NOT NULL;
ALTER TABLE automations DROP CONSTRAINT IF EXISTS automations_action_type_check;
ALTER TABLE automations ADD CONSTRAINT automations_action_type_check
	CHECK (action_type = 'create-sandbox') NOT VALID;
ALTER TABLE automations DROP CONSTRAINT IF EXISTS automations_auth_mode_check;
ALTER TABLE automations ADD CONSTRAINT automations_auth_mode_check
  CHECK (auth_mode IN ('bearer', 'hmac-sha256', 'github-sha256', 'gitlab-token', 'standard-webhooks'));

CREATE INDEX IF NOT EXISTS idx_automations_project_updated
  ON automations(project_id, enabled DESC, updated_at DESC);

ALTER TABLE automations DROP CONSTRAINT IF EXISTS automations_created_by_fkey;
ALTER TABLE automations DROP CONSTRAINT IF EXISTS automations_updated_by_fkey;

CREATE TABLE IF NOT EXISTS automation_runs (
  id UUID PRIMARY KEY,
  automation_id UUID REFERENCES automations(id) ON DELETE SET NULL,
  project_id TEXT NOT NULL,
  automation_name TEXT NOT NULL,
  endpoint_id UUID,
  action_type TEXT NOT NULL DEFAULT 'create-sandbox',
  template_id TEXT NOT NULL,
  template_name TEXT NOT NULL,
  trigger_source TEXT NOT NULL CHECK (trigger_source IN ('webhook', 'manual-test')),
  auth_mode TEXT NOT NULL CHECK (auth_mode IN ('bearer', 'hmac-sha256', 'github-sha256', 'gitlab-token', 'standard-webhooks')),
  event_id TEXT NOT NULL DEFAULT '',
  event_type TEXT NOT NULL DEFAULT '',
  event_source TEXT NOT NULL DEFAULT 'generic',
  event_time TIMESTAMPTZ,
  idempotency_hash BYTEA,
  idempotency_fingerprint TEXT NOT NULL DEFAULT '',
  payload_sha256 BYTEA NOT NULL,
  payload_bytes INTEGER NOT NULL,
  input_sha256 BYTEA,
  status TEXT NOT NULL CHECK (status IN ('evaluating', 'queued', 'provisioning', 'succeeded', 'failed')),
  sandbox_id TEXT,
  worker_job_id UUID REFERENCES worker_jobs(id) ON DELETE SET NULL,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  received_at TIMESTAMPTZ NOT NULL,
  queued_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);

ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS endpoint_id UUID;
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS action_type TEXT NOT NULL DEFAULT 'create-sandbox';
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS event_id TEXT NOT NULL DEFAULT '';
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS event_type TEXT NOT NULL DEFAULT '';
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT 'generic';
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS event_time TIMESTAMPTZ;
ALTER TABLE automation_runs DROP CONSTRAINT IF EXISTS automation_runs_action_type_check;
ALTER TABLE automation_runs ADD CONSTRAINT automation_runs_action_type_check
	CHECK (action_type = 'create-sandbox') NOT VALID;
ALTER TABLE automation_runs DROP CONSTRAINT IF EXISTS automation_runs_status_check;
ALTER TABLE automation_runs ADD CONSTRAINT automation_runs_status_check
	CHECK (status IN ('evaluating', 'queued', 'provisioning', 'succeeded', 'failed')) NOT VALID;
ALTER TABLE automation_runs DROP CONSTRAINT IF EXISTS automation_runs_auth_mode_check;
ALTER TABLE automation_runs ADD CONSTRAINT automation_runs_auth_mode_check
  CHECK (auth_mode IN ('bearer', 'hmac-sha256', 'github-sha256', 'gitlab-token', 'standard-webhooks'));
ALTER TABLE worker_jobs DROP CONSTRAINT IF EXISTS worker_jobs_automation_run_id_fkey;
ALTER TABLE worker_jobs ADD CONSTRAINT worker_jobs_automation_run_id_fkey
  FOREIGN KEY (automation_run_id) REFERENCES automation_runs(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_automation_runs_idempotency
  ON automation_runs(automation_id, idempotency_hash)
  WHERE idempotency_hash IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_automation_runs_project_received
  ON automation_runs(project_id, received_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_automation_runs_automation_received
  ON automation_runs(automation_id, received_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS system_logs (
  id BIGSERIAL PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  level TEXT NOT NULL,
  category TEXT NOT NULL,
  action TEXT NOT NULL,
  message TEXT NOT NULL,
  actor_id TEXT NOT NULL DEFAULT '',
  actor_name TEXT NOT NULL DEFAULT '',
  resource_kind TEXT NOT NULL DEFAULT '',
  resource_id TEXT NOT NULL DEFAULT '',
  resource_name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'success',
  duration_ms BIGINT NOT NULL DEFAULT 0,
  remote_addr TEXT NOT NULL DEFAULT '',
  detail JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_system_logs_created
  ON system_logs(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_system_logs_category_created
  ON system_logs(category, created_at DESC);
