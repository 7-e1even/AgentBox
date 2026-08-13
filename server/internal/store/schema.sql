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
