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
    'project', 'runtime', 'skill', 'mcp', 'sandbox', 'schedule', 'webhook', 'variable'
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

ALTER TABLE agents ADD COLUMN IF NOT EXISTS runtime_id TEXT NOT NULL DEFAULT 'python-venv';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS variable_ids JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS custom_args JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS concurrency INTEGER NOT NULL DEFAULT 1;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS sandbox_policy TEXT NOT NULL DEFAULT 'new';

UPDATE control_resources
SET spec = jsonb_set(spec, '{url}', to_jsonb('runtime://' || id), TRUE)
WHERE kind = 'mcp' AND spec->>'transport' = 'http' AND COALESCE(spec->>'url', '') = '';
