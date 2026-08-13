package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

var (
	ErrNotFound         = errors.New("agent not found")
	ErrResourceNotFound = errors.New("resource not found")
	ErrConflict         = errors.New("agent conflict")
)

const columns = `
  id, project_id, runtime_id, name, slug, description, avatar, provider_id, model_id,
  credential_id, system_prompt, skill_ids, mcp_server_ids, variable_ids, custom_args,
  temperature, max_steps, concurrency, sandbox_policy, status, version, created_at, updated_at`

type Store struct {
	pool    *pgxpool.Pool
	catalog agent.Catalog
}

func New(ctx context.Context, databaseURL string, catalog agent.Catalog) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	config.MaxConns = 10
	config.MinIdleConns = 1
	config.MaxConnIdleTime = 30 * time.Second
	config.ConnConfig.ConnectTimeout = 5 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}

	store := &Store{pool: pool, catalog: catalog}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := store.seed(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

func (s *Store) seed(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x4147424f58)); err != nil {
		return fmt.Errorf("lock seed: %w", err)
	}
	if err := seedResources(ctx, tx, s.catalog); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM agents").Scan(&count); err != nil {
		return fmt.Errorf("count agents: %w", err)
	}
	if count > 0 {
		return tx.Commit(ctx)
	}

	openAICredential := "openai-primary"
	anthropicCredential := "anthropic-primary"
	examples := []agent.Input{
		{
			ProjectID: "default", RuntimeID: "python-venv", Concurrency: 1, SandboxPolicy: "reuse",
			Name: "Research Copilot", Slug: "research-copilot", Description: "收集可信来源，比较观点并整理成可执行的研究结论。", Avatar: "RC",
			ProviderID: "openai", ModelID: "gpt-5", CredentialID: &openAICredential,
			SystemPrompt: "你是一名严谨的研究助理。先明确问题和证据标准，再检索并交叉验证来源，清楚区分事实、推断与未知信息。",
			SkillIDs:     []string{"web-research", "document-writer", "task-planner"}, MCPServerIDs: []string{"browser", "filesystem"},
			Temperature: 0.3, MaxSteps: 16, Status: agent.StatusActive,
		},
		{
			ProjectID: "default", RuntimeID: "docker-agent", Concurrency: 1, SandboxPolicy: "new",
			Name: "Release Writer", Slug: "release-writer", Description: "把代码变更整理成面向用户的发布说明。", Avatar: "RW",
			ProviderID: "anthropic", ModelID: "claude-sonnet", CredentialID: &anthropicCredential,
			SystemPrompt: "你负责撰写清晰、具体的发布说明。基于实际变更解释用户价值，不夸大影响，不遗漏破坏性变更和迁移步骤。",
			SkillIDs:     []string{"document-writer", "code-review"}, MCPServerIDs: []string{"github", "filesystem"},
			Temperature: 0.5, MaxSteps: 10, Status: agent.StatusDraft,
		},
	}
	for _, input := range examples {
		created, err := createInTx(ctx, tx, input)
		if err != nil {
			return fmt.Errorf("seed agent: %w", err)
		}
		if err := saveRevision(ctx, tx, created); err != nil {
			return fmt.Errorf("seed revision: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed: %w", err)
	}
	return nil
}

func seedResources(ctx context.Context, tx pgx.Tx, catalog agent.Catalog) error {
	now := time.Now().UTC()
	resources := []platform.Input{
		{ID: "default", Kind: platform.KindProject, Name: "AgentBox Studio", Description: "默认 Agent 沙箱项目", Enabled: true, Spec: map[string]any{"source": "git", "repository": "http://192.168.31.134:3000/admin/AgentBox.git", "branch": "main"}},
		{ID: "python-venv", Kind: platform.KindRuntime, ProjectID: stringRef("default"), Name: "Python Virtualenv", Description: "在主机隔离目录中创建 Python 虚拟环境", Enabled: true, Spec: map[string]any{"driver": "process", "base": "Python 3.12", "workdir": "/workspace", "setup": "python -m venv .venv", "cpu": "2", "memory": "4 GiB", "network": "restricted"}},
		{ID: "docker-agent", Kind: platform.KindRuntime, ProjectID: stringRef("default"), Name: "Docker Agent", Description: "标准 Linux 容器沙箱模板", Enabled: true, Spec: map[string]any{"driver": "docker", "image": "ubuntu:24.04", "workdir": "/workspace", "cpu": "2", "memory": "4 GiB", "network": "egress"}},
		{ID: "github-token", Kind: platform.KindVariable, ProjectID: stringRef("default"), Name: "GITHUB_TOKEN", Description: "由 Runtime worker 从宿主机环境解析", Enabled: true, Spec: map[string]any{"mode": "secret-ref", "reference": "env://GITHUB_TOKEN"}},
	}
	for _, skill := range catalog.Skills {
		resources = append(resources, platform.Input{ID: skill.ID, Kind: platform.KindSkill, ProjectID: stringRef("default"), Name: skill.Name, Description: skill.Description, Enabled: true, Spec: map[string]any{"version": skill.Version, "category": skill.Category, "source": "builtin", "instructions": "由平台在创建沙箱时安装到 Agent 的 skills 目录。"}})
	}
	for _, server := range catalog.MCPServers {
		spec := map[string]any{"transport": server.Transport, "toolCount": server.ToolCount}
		if server.Transport == "http" {
			spec["url"] = "runtime://" + server.ID
		} else {
			spec["command"] = server.ID
		}
		resources = append(resources, platform.Input{ID: server.ID, Kind: platform.KindMCP, ProjectID: stringRef("default"), Name: server.Name, Description: server.Description, Enabled: server.Status == "ready", Spec: spec})
	}
	for _, resource := range resources {
		if _, err := tx.Exec(ctx, `INSERT INTO control_resources
      (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
      VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $8)
      ON CONFLICT (id) DO NOTHING`, resource.ID, resource.Kind, resource.ProjectID,
			resource.Name, resource.Description, resource.Enabled, mustMapJSON(resource.Spec), now); err != nil {
			return fmt.Errorf("seed resource %s: %w", resource.ID, err)
		}
	}
	return nil
}

func stringRef(value string) *string { return &value }

func scanAgent(row pgx.Row) (agent.Agent, error) {
	var result agent.Agent
	var credential pgtype.Text
	var skillJSON []byte
	var mcpJSON []byte
	var variableJSON []byte
	var argsJSON []byte
	err := row.Scan(
		&result.ID, &result.ProjectID, &result.RuntimeID, &result.Name, &result.Slug, &result.Description,
		&result.Avatar, &result.ProviderID, &result.ModelID, &credential,
		&result.SystemPrompt, &skillJSON, &mcpJSON, &variableJSON, &argsJSON, &result.Temperature,
		&result.MaxSteps, &result.Concurrency, &result.SandboxPolicy, &result.Status,
		&result.Version, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return agent.Agent{}, err
	}
	if credential.Valid {
		result.CredentialID = &credential.String
	}
	if err := json.Unmarshal(skillJSON, &result.SkillIDs); err != nil {
		return agent.Agent{}, fmt.Errorf("decode skill ids: %w", err)
	}
	if err := json.Unmarshal(mcpJSON, &result.MCPServerIDs); err != nil {
		return agent.Agent{}, fmt.Errorf("decode mcp server ids: %w", err)
	}
	if err := json.Unmarshal(variableJSON, &result.VariableIDs); err != nil {
		return agent.Agent{}, fmt.Errorf("decode variable ids: %w", err)
	}
	if err := json.Unmarshal(argsJSON, &result.CustomArgs); err != nil {
		return agent.Agent{}, fmt.Errorf("decode custom args: %w", err)
	}
	return result, nil
}

func (s *Store) List(ctx context.Context) ([]agent.Agent, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM agents
    ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'draft' THEN 1 ELSE 2 END,
             updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	result := make([]agent.Agent, 0)
	for rows.Next() {
		item, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents: %w", err)
	}
	return result, nil
}

func (s *Store) Get(ctx context.Context, id string) (agent.Agent, error) {
	result, err := scanAgent(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM agents WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.Agent{}, ErrNotFound
	}
	if err != nil {
		return agent.Agent{}, fmt.Errorf("get agent: %w", err)
	}
	return result, nil
}

func (s *Store) Create(ctx context.Context, input agent.Input) (agent.Agent, error) {
	agent.Normalize(&input)
	if err := agent.Validate(input, s.catalog); err != nil {
		return agent.Agent{}, err
	}
	if err := s.validateAgentBindings(ctx, input); err != nil {
		return agent.Agent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return agent.Agent{}, fmt.Errorf("begin create: %w", err)
	}
	defer tx.Rollback(ctx)
	created, err := createInTx(ctx, tx, input)
	if err != nil {
		return agent.Agent{}, mapDatabaseError(err)
	}
	if err := saveRevision(ctx, tx, created); err != nil {
		return agent.Agent{}, fmt.Errorf("save revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return agent.Agent{}, mapDatabaseError(err)
	}
	return created, nil
}

func createInTx(ctx context.Context, tx pgx.Tx, input agent.Input) (agent.Agent, error) {
	now := time.Now().UTC()
	return scanAgent(tx.QueryRow(ctx, `INSERT INTO agents (
    id, project_id, runtime_id, name, slug, description, avatar, provider_id, model_id,
    credential_id, system_prompt, skill_ids, mcp_server_ids, variable_ids, custom_args,
    temperature, max_steps, concurrency, sandbox_policy, status, version, created_at, updated_at
  ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb,
    $13::jsonb, $14::jsonb, $15::jsonb, $16, $17, $18, $19, $20, 1, $21, $21)
  RETURNING `+columns,
		uuid.NewString(), input.ProjectID, input.RuntimeID, input.Name, input.Slug,
		input.Description, input.Avatar, input.ProviderID, input.ModelID, input.CredentialID,
		input.SystemPrompt, mustJSON(input.SkillIDs), mustJSON(input.MCPServerIDs),
		mustJSON(input.VariableIDs), mustJSON(input.CustomArgs), input.Temperature,
		input.MaxSteps, input.Concurrency, input.SandboxPolicy, input.Status, now,
	))
}

func (s *Store) Update(ctx context.Context, id string, input agent.Input, expectedVersion int) (agent.Agent, error) {
	agent.Normalize(&input)
	if err := agent.Validate(input, s.catalog); err != nil {
		return agent.Agent{}, err
	}
	if err := s.validateAgentBindings(ctx, input); err != nil {
		return agent.Agent{}, err
	}
	if expectedVersion < 1 {
		return agent.Agent{}, &agent.ValidationError{Message: "版本号无效"}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return agent.Agent{}, fmt.Errorf("begin update: %w", err)
	}
	defer tx.Rollback(ctx)

	updated, err := scanAgent(tx.QueryRow(ctx, `UPDATE agents SET
    project_id = $1, runtime_id = $2, name = $3, slug = $4, description = $5,
    avatar = $6, provider_id = $7, model_id = $8, credential_id = $9,
    system_prompt = $10, skill_ids = $11::jsonb, mcp_server_ids = $12::jsonb,
    variable_ids = $13::jsonb, custom_args = $14::jsonb, temperature = $15,
    max_steps = $16, concurrency = $17, sandbox_policy = $18, status = $19,
    version = version + 1, updated_at = $20
  WHERE id = $21 AND version = $22 RETURNING `+columns,
		input.ProjectID, input.RuntimeID, input.Name, input.Slug, input.Description,
		input.Avatar, input.ProviderID, input.ModelID, input.CredentialID, input.SystemPrompt,
		mustJSON(input.SkillIDs), mustJSON(input.MCPServerIDs), mustJSON(input.VariableIDs),
		mustJSON(input.CustomArgs), input.Temperature, input.MaxSteps, input.Concurrency,
		input.SandboxPolicy, input.Status, time.Now().UTC(), id, expectedVersion,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if queryErr := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1)", id).Scan(&exists); queryErr != nil {
			return agent.Agent{}, fmt.Errorf("check update conflict: %w", queryErr)
		}
		if !exists {
			return agent.Agent{}, ErrNotFound
		}
		return agent.Agent{}, ErrConflict
	}
	if err != nil {
		return agent.Agent{}, mapDatabaseError(err)
	}
	if err := saveRevision(ctx, tx, updated); err != nil {
		return agent.Agent{}, fmt.Errorf("save revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return agent.Agent{}, mapDatabaseError(err)
	}
	return updated, nil
}

func (s *Store) validateAgentBindings(ctx context.Context, input agent.Input) error {
	type binding struct {
		id   string
		kind platform.Kind
		name string
	}
	bindings := []binding{
		{input.ProjectID, platform.KindProject, "Project"},
		{input.RuntimeID, platform.KindRuntime, "Runtime"},
	}
	for _, id := range input.SkillIDs {
		bindings = append(bindings, binding{id, platform.KindSkill, "Skill"})
	}
	for _, id := range input.MCPServerIDs {
		bindings = append(bindings, binding{id, platform.KindMCP, "MCP Server"})
	}
	for _, id := range input.VariableIDs {
		bindings = append(bindings, binding{id, platform.KindVariable, "Variable"})
	}
	for _, binding := range bindings {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
      SELECT 1 FROM control_resources
      WHERE id = $1 AND kind = $2 AND enabled = TRUE
        AND (kind = 'project' OR project_id = $3)
    )`, binding.id, binding.kind, input.ProjectID).Scan(&exists); err != nil {
			return fmt.Errorf("validate agent binding: %w", err)
		}
		if !exists {
			return &agent.ValidationError{Message: binding.name + " 不存在、未启用或不属于当前 Project"}
		}
	}
	return nil
}

func (s *Store) Duplicate(ctx context.Context, id string) (agent.Agent, error) {
	source, err := s.Get(ctx, id)
	if err != nil {
		return agent.Agent{}, err
	}
	rows, err := s.pool.Query(ctx, "SELECT slug FROM agents WHERE slug = $1 OR slug LIKE $2", source.Slug+"-copy", source.Slug+"-copy-%")
	if err != nil {
		return agent.Agent{}, fmt.Errorf("find duplicate slugs: %w", err)
	}
	existing := map[string]struct{}{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return agent.Agent{}, fmt.Errorf("scan duplicate slug: %w", err)
		}
		existing[slug] = struct{}{}
	}
	rows.Close()

	input := source.Input
	input.Name += " 副本"
	input.Slug = source.Slug + "-copy"
	input.Status = agent.StatusDraft
	for suffix := 2; ; suffix++ {
		if _, found := existing[input.Slug]; !found {
			break
		}
		input.Slug = fmt.Sprintf("%s-copy-%d", source.Slug, suffix)
	}
	return s.Create(ctx, input)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	var status agent.Status
	if err := s.pool.QueryRow(ctx, "SELECT status FROM agents WHERE id = $1", id).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("get delete status: %w", err)
	}
	if status != agent.StatusArchived {
		return ErrConflict
	}
	command, err := s.pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func saveRevision(ctx context.Context, tx pgx.Tx, value agent.Agent) error {
	snapshot, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_revisions (agent_id, version, snapshot, created_at)
    VALUES ($1, $2, $3::jsonb, $4)`, value.ID, value.Version, snapshot, value.UpdatedAt)
	return err
}

func mustJSON(value []string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func mustMapJSON(value map[string]any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func scanResource(row pgx.Row) (platform.Resource, error) {
	var result platform.Resource
	var project pgtype.Text
	var specJSON []byte
	if err := row.Scan(&result.ID, &result.Kind, &project, &result.Name, &result.Description,
		&result.Enabled, &specJSON, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return platform.Resource{}, err
	}
	if project.Valid {
		result.ProjectID = &project.String
	}
	if err := json.Unmarshal(specJSON, &result.Spec); err != nil {
		return platform.Resource{}, fmt.Errorf("decode resource spec: %w", err)
	}
	return result, nil
}

const resourceColumns = `id, kind, project_id, name, description, enabled, spec, created_at, updated_at`

func (s *Store) ListResources(ctx context.Context) ([]platform.Resource, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+resourceColumns+` FROM control_resources
    ORDER BY kind, enabled DESC, updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	defer rows.Close()
	resources := make([]platform.Resource, 0)
	for rows.Next() {
		resource, err := scanResource(rows)
		if err != nil {
			return nil, fmt.Errorf("scan resource: %w", err)
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func (s *Store) CreateResource(ctx context.Context, input platform.Input) (platform.Resource, error) {
	platform.Normalize(&input)
	if err := platform.Validate(input); err != nil {
		return platform.Resource{}, err
	}
	if err := s.ensureProject(ctx, input); err != nil {
		return platform.Resource{}, err
	}
	if err := s.ensureResourceReferences(ctx, input); err != nil {
		return platform.Resource{}, err
	}
	now := time.Now().UTC()
	result, err := scanResource(s.pool.QueryRow(ctx, `INSERT INTO control_resources
    (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $8)
    RETURNING `+resourceColumns, input.ID, input.Kind, input.ProjectID, input.Name,
		input.Description, input.Enabled, mustMapJSON(input.Spec), now))
	if err != nil {
		return platform.Resource{}, mapResourceError(err)
	}
	return result, nil
}

func (s *Store) UpdateResource(ctx context.Context, id string, input platform.Input) (platform.Resource, error) {
	platform.Normalize(&input)
	if input.ID != id {
		return platform.Resource{}, &platform.ValidationError{Message: "资源标识不能修改"}
	}
	if err := platform.Validate(input); err != nil {
		return platform.Resource{}, err
	}
	if err := s.ensureProject(ctx, input); err != nil {
		return platform.Resource{}, err
	}
	if err := s.ensureResourceReferences(ctx, input); err != nil {
		return platform.Resource{}, err
	}
	result, err := scanResource(s.pool.QueryRow(ctx, `UPDATE control_resources SET
    project_id = $1, name = $2, description = $3, enabled = $4, spec = $5::jsonb,
    updated_at = $6 WHERE id = $7 AND kind = $8 RETURNING `+resourceColumns,
		input.ProjectID, input.Name, input.Description, input.Enabled, mustMapJSON(input.Spec),
		time.Now().UTC(), id, input.Kind))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Resource{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.Resource{}, mapResourceError(err)
	}
	return result, nil
}

func (s *Store) DeleteResource(ctx context.Context, id string) error {
	var kind platform.Kind
	if err := s.pool.QueryRow(ctx, "SELECT kind FROM control_resources WHERE id = $1", id).Scan(&kind); errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceNotFound
	} else if err != nil {
		return fmt.Errorf("get resource: %w", err)
	}
	var referenced bool
	var referenceQuery string
	switch kind {
	case platform.KindProject:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE project_id = $1 UNION ALL SELECT 1 FROM control_resources WHERE project_id = $1)"
	case platform.KindRuntime:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE runtime_id = $1)"
	case platform.KindSkill:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE skill_ids ? $1)"
	case platform.KindMCP:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE mcp_server_ids ? $1)"
	case platform.KindVariable:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE variable_ids ? $1)"
	}
	if referenceQuery != "" {
		if err := s.pool.QueryRow(ctx, referenceQuery, id).Scan(&referenced); err != nil {
			return fmt.Errorf("check resource bindings: %w", err)
		}
	}
	if referenced {
		return fmt.Errorf("%w: resource is still referenced", ErrConflict)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM control_resources WHERE id = $1", id); err != nil {
		return fmt.Errorf("delete resource: %w", err)
	}
	return nil
}

func (s *Store) ensureProject(ctx context.Context, input platform.Input) error {
	if input.Kind == platform.KindProject {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM control_resources WHERE id = $1 AND kind = 'project'
  )`, input.ProjectID).Scan(&exists); err != nil {
		return fmt.Errorf("check project: %w", err)
	}
	if !exists {
		return &platform.ValidationError{Message: "所属项目不存在"}
	}
	return nil
}

func (s *Store) ensureResourceReferences(ctx context.Context, input platform.Input) error {
	if input.Kind != platform.KindSandbox && input.Kind != platform.KindSchedule && input.Kind != platform.KindWebhook {
		return nil
	}
	agentID, _ := input.Spec["agentId"].(string)
	if _, err := uuid.Parse(agentID); err != nil {
		return &platform.ValidationError{Message: "目标 Agent 无效"}
	}
	var agentExists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1 AND project_id = $2)", agentID, input.ProjectID).Scan(&agentExists); err != nil {
		return fmt.Errorf("check target agent: %w", err)
	}
	if !agentExists {
		return &platform.ValidationError{Message: "目标 Agent 不存在或不属于当前 Project"}
	}
	if input.Kind == platform.KindSandbox {
		runtimeID, _ := input.Spec["runtimeId"].(string)
		var runtimeExists bool
		if err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM control_resources WHERE id = $1 AND kind = 'runtime' AND project_id = $2 AND enabled = TRUE)", runtimeID, input.ProjectID).Scan(&runtimeExists); err != nil {
			return fmt.Errorf("check sandbox runtime: %w", err)
		}
		if !runtimeExists {
			return &platform.ValidationError{Message: "Runtime 不存在、未启用或不属于当前 Project"}
		}
	}
	return nil
}

func mapResourceError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%w: resource id already exists", ErrConflict)
	}
	return err
}

func mapDatabaseError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%w: agent slug already exists", ErrConflict)
	}
	return err
}
