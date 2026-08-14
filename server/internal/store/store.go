package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
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
	ErrNotFound            = errors.New("agent not found")
	ErrResourceNotFound    = errors.New("resource not found")
	ErrConflict            = errors.New("agent conflict")
	ErrPairingInvalid      = errors.New("server pairing invalid")
	ErrWorkerUnauthorized  = errors.New("worker unauthorized")
	ErrUnauthorized        = errors.New("user unauthorized")
	ErrNoJob               = errors.New("no worker job available")
	ErrProviderUnavailable = errors.New("Provider 服务不可用")
)

const workerJobLeaseDuration = 2 * time.Hour

const columns = `
  id, project_id, runtime_id, name, slug, description, avatar, provider_id, model_id,
  credential_id, system_prompt, skill_ids, mcp_server_ids, variable_ids, custom_args,
  temperature, max_steps, concurrency, sandbox_policy, status, version, created_at, updated_at`

type Store struct {
	pool      *pgxpool.Pool
	catalog   agent.Catalog
	secretKey []byte
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

	secretKey, err := loadSecretKey()
	if err != nil {
		pool.Close()
		return nil, err
	}
	store := &Store{pool: pool, catalog: catalog, secretKey: secretKey}
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
			ProjectID: "default", RuntimeID: "docker-agent", Concurrency: 1, SandboxPolicy: "reuse",
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
		agent.Normalize(&input)
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
		{ID: "default", Kind: platform.KindProject, Name: "AgentBox Studio", Description: "默认 Agent 沙箱项目", Enabled: true, Spec: map[string]any{}},
		{ID: "image-ubuntu-2404", Kind: platform.KindImage, Name: "Ubuntu 24.04", Description: "Docker 与 VM 环境模板可复用的基础 OCI 镜像", Enabled: true, Spec: map[string]any{"reference": "ubuntu:24.04", "architecture": "all", "modes": []string{"docker", "vm"}}},
		{ID: "docker-agent", Kind: platform.KindRuntime, ProjectID: stringRef("default"), Name: "Codex 开发环境", Description: "预装 Codex、Git 与常用 Agent 能力的标准 Linux 环境", Enabled: true, Spec: map[string]any{"driver": "docker", "imageId": "image-ubuntu-2404", "agentTools": []string{"codex"}, "skillIds": []string{"code-review", "task-planner"}, "mcpServerIds": []string{"filesystem"}, "variableIds": []string{"github-token"}, "credentialIds": []string{}, "workdir": "/workspace", "cpu": "2", "memory": "4 GiB", "network": "egress"}},
		{ID: "github-token", Kind: platform.KindVariable, ProjectID: stringRef("default"), Name: "GITHUB_TOKEN", Description: "由 Runtime worker 从宿主机环境解析", Enabled: true, Spec: map[string]any{"mode": "secret-ref", "reference": "env://GITHUB_TOKEN"}},
	}
	for _, skill := range catalog.Skills {
		resources = append(resources, platform.Input{ID: skill.ID, Kind: platform.KindSkill, ProjectID: stringRef("default"), Name: skill.Name, Description: skill.Description, Enabled: true, Spec: map[string]any{"version": skill.Version, "category": skill.Category, "source": "builtin", "instructions": "由平台在创建沙箱时安装到 Agent 的 skills 目录。"}})
	}
	for _, server := range catalog.MCPServers {
		spec := map[string]any{"transport": server.Transport, "toolCount": server.ToolCount}
		switch server.ID {
		case "filesystem":
			spec["command"] = "npx"
			spec["args"] = "-y @modelcontextprotocol/server-filesystem /workspace"
		case "browser":
			spec["transport"] = "stdio"
			spec["command"] = "npx"
			spec["args"] = "-y @playwright/mcp@latest --headless"
		case "github":
			spec["url"] = "https://api.githubcopilot.com/mcp/"
			spec["headers"] = "Authorization=env://GITHUB_TOKEN"
		default:
			spec["command"] = server.ID
		}
		enabled := server.Status == "ready" && server.ID != "github"
		resources = append(resources, platform.Input{ID: server.ID, Kind: platform.KindMCP, ProjectID: stringRef("default"), Name: server.Name, Description: server.Description, Enabled: enabled, Spec: spec})
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
	if result.VariableIDs == nil {
		result.VariableIDs = []string{}
	}
	if result.CustomArgs == nil {
		result.CustomArgs = []string{}
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
	validationCatalog, err := s.validationCatalog(ctx)
	if err != nil {
		return agent.Agent{}, err
	}
	if err := agent.Validate(input, validationCatalog); err != nil {
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
	validationCatalog, err := s.validationCatalog(ctx)
	if err != nil {
		return agent.Agent{}, err
	}
	if err := agent.Validate(input, validationCatalog); err != nil {
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
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1)", id).Scan(&exists); err != nil {
		return fmt.Errorf("check delete agent: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	var referenced bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM control_resources WHERE spec->>'agentId' = $1
  )`, id).Scan(&referenced); err != nil {
		return fmt.Errorf("check delete agent references: %w", err)
	}
	if referenced {
		return fmt.Errorf("%w: agent is still referenced", ErrConflict)
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

const serverColumns = `
  id::text, name, hostname, os, arch, capabilities, inventory,
  CASE WHEN last_seen_at > NOW() - INTERVAL '45 seconds' THEN 'online' ELSE 'offline' END,
  last_seen_at, created_at, updated_at`

func scanManagedServer(row pgx.Row) (platform.ManagedServer, error) {
	var result platform.ManagedServer
	var capabilitiesJSON, inventoryJSON []byte
	if err := row.Scan(&result.ID, &result.Name, &result.Hostname, &result.OS, &result.Arch,
		&capabilitiesJSON, &inventoryJSON, &result.Status, &result.LastSeenAt, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return platform.ManagedServer{}, err
	}
	if err := json.Unmarshal(capabilitiesJSON, &result.Capabilities); err != nil {
		return platform.ManagedServer{}, fmt.Errorf("decode server capabilities: %w", err)
	}
	if err := json.Unmarshal(inventoryJSON, &result.Inventory); err != nil {
		return platform.ManagedServer{}, fmt.Errorf("decode server inventory: %w", err)
	}
	platform.NormalizeServerInventory(&result.Inventory)
	return result, nil
}

func (s *Store) ListServers(ctx context.Context) ([]platform.ManagedServer, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+serverColumns+` FROM managed_servers ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()
	servers := make([]platform.ManagedServer, 0)
	for rows.Next() {
		server, err := scanManagedServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan server: %w", err)
		}
		servers = append(servers, server)
	}
	return servers, rows.Err()
}

func (s *Store) CreateServerPairing(ctx context.Context) (platform.ServerPairing, error) {
	token, err := randomToken()
	if err != nil {
		return platform.ServerPairing{}, err
	}
	now := time.Now().UTC()
	pairing := platform.ServerPairing{
		ID:        uuid.NewString(),
		Token:     token,
		ExpiresAt: now.Add(15 * time.Minute),
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO server_pairings
    (id, token_hash, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
		pairing.ID, hashToken(token), pairing.ExpiresAt, now); err != nil {
		return platform.ServerPairing{}, fmt.Errorf("create server pairing: %w", err)
	}
	_, _ = s.pool.Exec(ctx, "DELETE FROM server_pairings WHERE expires_at < NOW() - INTERVAL '1 day'")
	return pairing, nil
}

func (s *Store) GetServerPairing(ctx context.Context, id string) (platform.ServerPairing, error) {
	if _, err := uuid.Parse(id); err != nil {
		return platform.ServerPairing{}, ErrResourceNotFound
	}
	var pairing platform.ServerPairing
	var serverID pgtype.Text
	var claimedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `SELECT id::text, expires_at, server_id::text, claimed_at
    FROM server_pairings WHERE id = $1`, id).Scan(&pairing.ID, &pairing.ExpiresAt, &serverID, &claimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.ServerPairing{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.ServerPairing{}, fmt.Errorf("get server pairing: %w", err)
	}
	if serverID.Valid {
		pairing.ServerID = &serverID.String
	}
	if claimedAt.Valid {
		pairing.ClaimedAt = &claimedAt.Time
	}
	return pairing, nil
}

func (s *Store) RegisterServer(ctx context.Context, input platform.ServerRegistration) (platform.ManagedServer, string, error) {
	platform.NormalizeServerRegistration(&input)
	if err := platform.ValidateServerRegistration(input); err != nil {
		return platform.ManagedServer{}, "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.ManagedServer{}, "", fmt.Errorf("begin server registration: %w", err)
	}
	defer tx.Rollback(ctx)
	var pairingID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM server_pairings
    WHERE token_hash = $1 AND claimed_at IS NULL AND expires_at > NOW() FOR UPDATE`,
		hashToken(input.PairingToken)).Scan(&pairingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.ManagedServer{}, "", ErrPairingInvalid
	}
	if err != nil {
		return platform.ManagedServer{}, "", fmt.Errorf("claim server pairing: %w", err)
	}
	credential, err := randomToken()
	if err != nil {
		return platform.ManagedServer{}, "", err
	}
	serverID := input.ServerID
	now := time.Now().UTC()
	server, err := scanManagedServer(tx.QueryRow(ctx, `INSERT INTO managed_servers
    (id, name, hostname, os, arch, capabilities, credential_hash, last_seen_at, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $8, $8)
    ON CONFLICT (id) DO UPDATE SET
      name = EXCLUDED.name,
      hostname = EXCLUDED.hostname,
      os = EXCLUDED.os,
      arch = EXCLUDED.arch,
      capabilities = EXCLUDED.capabilities,
      credential_hash = EXCLUDED.credential_hash,
      last_seen_at = EXCLUDED.last_seen_at,
      updated_at = EXCLUDED.updated_at
    RETURNING `+serverColumns, serverID, input.Name, input.Hostname, input.OS, input.Arch,
		mustJSON(input.Capabilities), hashToken(credential), now))
	if err != nil {
		return platform.ManagedServer{}, "", fmt.Errorf("register server: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE server_pairings
    SET server_id = $1, claimed_at = $2 WHERE id = $3`, serverID, now, pairingID); err != nil {
		return platform.ManagedServer{}, "", fmt.Errorf("complete server pairing: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.ManagedServer{}, "", fmt.Errorf("commit server registration: %w", err)
	}
	return server, credential, nil
}

func (s *Store) HeartbeatServer(
	ctx context.Context,
	id, credential string,
	capabilities []string,
	inventory *platform.ServerInventory,
) error {
	if _, err := uuid.Parse(id); err != nil || len(credential) < 32 {
		return ErrWorkerUnauthorized
	}
	now := time.Now().UTC()
	var result pgconn.CommandTag
	var err error
	if capabilities == nil && inventory == nil {
		result, err = s.pool.Exec(ctx, `UPDATE managed_servers
      SET last_seen_at = $1, updated_at = $1 WHERE id = $2 AND credential_hash = $3`,
			now, id, hashToken(credential))
	} else if inventory == nil {
		result, err = s.pool.Exec(ctx, `UPDATE managed_servers
      SET capabilities = $1::jsonb, last_seen_at = $2, updated_at = $2
      WHERE id = $3 AND credential_hash = $4`,
			mustJSON(capabilities), now, id, hashToken(credential))
	} else if capabilities == nil {
		platform.NormalizeServerInventory(inventory)
		result, err = s.pool.Exec(ctx, `UPDATE managed_servers
      SET inventory = $1::jsonb, last_seen_at = $2, updated_at = $2
      WHERE id = $3 AND credential_hash = $4`,
			mustMapJSON(map[string]any{
				"dockerImages": inventory.DockerImages, "vmImages": inventory.VMImages,
				"vmImageDirectory": inventory.VMImageDirectory,
			}), now, id, hashToken(credential))
	} else {
		platform.NormalizeServerInventory(inventory)
		result, err = s.pool.Exec(ctx, `UPDATE managed_servers
      SET capabilities = $1::jsonb, inventory = $2::jsonb, last_seen_at = $3, updated_at = $3
      WHERE id = $4 AND credential_hash = $5`,
			mustJSON(capabilities), mustMapJSON(map[string]any{
				"dockerImages": inventory.DockerImages, "vmImages": inventory.VMImages,
				"vmImageDirectory": inventory.VMImageDirectory,
			}), now, id, hashToken(credential))
	}
	if err != nil {
		return fmt.Errorf("heartbeat server: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrWorkerUnauthorized
	}
	return nil
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrResourceNotFound
	}
	var referenced bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM control_resources
    WHERE kind IN ('runtime', 'sandbox') AND spec->>'serverId' = $1
  )`, id).Scan(&referenced); err != nil {
		return fmt.Errorf("check server environment bindings: %w", err)
	}
	if referenced {
		return fmt.Errorf("%w: server still has environment bindings", ErrConflict)
	}
	result, err := s.pool.Exec(ctx, "DELETE FROM managed_servers WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete server: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrResourceNotFound
	}
	return nil
}

const credentialColumns = `id, name, provider_id, protocol, endpoint, model_id, models,
  secret_last_four, enabled, last_check_at, last_check_ok, last_check_error,
  created_at, updated_at`

func scanCredential(row pgx.Row) (platform.ManagedCredential, error) {
	var result platform.ManagedCredential
	var lastFour string
	var modelsJSON []byte
	var lastCheckAt pgtype.Timestamptz
	var lastCheckOK pgtype.Bool
	if err := row.Scan(
		&result.ID, &result.Name, &result.ProviderID, &result.Protocol,
		&result.Endpoint, &result.ModelID, &modelsJSON, &lastFour, &result.Enabled,
		&lastCheckAt, &lastCheckOK, &result.LastCheckError,
		&result.CreatedAt, &result.UpdatedAt,
	); err != nil {
		return platform.ManagedCredential{}, err
	}
	if err := json.Unmarshal(modelsJSON, &result.Models); err != nil {
		return platform.ManagedCredential{}, fmt.Errorf("decode provider models: %w", err)
	}
	if result.Models == nil {
		result.Models = []platform.CredentialModel{}
	}
	result.Endpoint = normalizeKnownProviderEndpoint(result.Protocol, result.Endpoint)
	result.MaskedSecret = "••••" + lastFour
	if lastCheckAt.Valid {
		result.LastCheckAt = &lastCheckAt.Time
	}
	if lastCheckOK.Valid {
		result.LastCheckOK = &lastCheckOK.Bool
	}
	return result, nil
}

func (s *Store) ListCredentials(ctx context.Context) ([]platform.ManagedCredential, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+credentialColumns+` FROM provider_credentials
    ORDER BY enabled DESC, updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list provider credentials: %w", err)
	}
	defer rows.Close()
	credentials := make([]platform.ManagedCredential, 0)
	for rows.Next() {
		credential, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("scan provider credential: %w", err)
		}
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}

func (s *Store) CreateCredential(ctx context.Context, input platform.CredentialInput) (platform.ManagedCredential, error) {
	platform.NormalizeCredential(&input)
	input.Endpoint = normalizeKnownProviderEndpoint(input.Protocol, input.Endpoint)
	if err := platform.ValidateCredential(input, true); err != nil {
		return platform.ManagedCredential{}, err
	}
	if !s.providerExists(input.ProviderID) {
		return platform.ManagedCredential{}, &platform.ValidationError{Message: "Agent Provider 不存在"}
	}
	ciphertext, nonce, err := encryptSecret(s.secretKey, input.Secret)
	if err != nil {
		return platform.ManagedCredential{}, err
	}
	now := time.Now().UTC()
	result, err := scanCredential(s.pool.QueryRow(ctx, `INSERT INTO provider_credentials
    (id, name, provider_id, protocol, endpoint, model_id, secret_ciphertext,
     secret_nonce, secret_last_four, enabled, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
    RETURNING `+credentialColumns,
		input.ID, input.Name, input.ProviderID, input.Protocol, input.Endpoint,
		input.ModelID, ciphertext, nonce, lastFour(input.Secret), input.Enabled, now,
	))
	if err != nil {
		return platform.ManagedCredential{}, mapResourceError(err)
	}
	return result, nil
}

func (s *Store) UpdateCredential(ctx context.Context, id string, input platform.CredentialInput) (platform.ManagedCredential, error) {
	platform.NormalizeCredential(&input)
	input.Endpoint = normalizeKnownProviderEndpoint(input.Protocol, input.Endpoint)
	if input.ID != id {
		return platform.ManagedCredential{}, &platform.ValidationError{Message: "凭据标识不能修改"}
	}
	if err := platform.ValidateCredential(input, false); err != nil {
		return platform.ManagedCredential{}, err
	}
	if !s.providerExists(input.ProviderID) {
		return platform.ManagedCredential{}, &platform.ValidationError{Message: "Agent Provider 不存在"}
	}
	if input.Secret == "" {
		result, err := scanCredential(s.pool.QueryRow(ctx, `UPDATE provider_credentials SET
		name = $1, models = CASE WHEN provider_id = $2 THEN models ELSE '[]'::jsonb END,
		provider_id = $2, protocol = $3, endpoint = $4, model_id = $5,
		enabled = $6, last_check_at = NULL, last_check_ok = NULL,
		last_check_error = '', updated_at = $7
		WHERE id = $8 RETURNING `+credentialColumns,
			input.Name, input.ProviderID, input.Protocol, input.Endpoint, input.ModelID,
			input.Enabled, time.Now().UTC(), id,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ManagedCredential{}, ErrResourceNotFound
		}
		return result, err
	}
	ciphertext, nonce, err := encryptSecret(s.secretKey, input.Secret)
	if err != nil {
		return platform.ManagedCredential{}, err
	}
	result, err := scanCredential(s.pool.QueryRow(ctx, `UPDATE provider_credentials SET
	name = $1, models = CASE WHEN provider_id = $2 THEN models ELSE '[]'::jsonb END,
	provider_id = $2, protocol = $3, endpoint = $4, model_id = $5,
	secret_ciphertext = $6, secret_nonce = $7, secret_last_four = $8,
	enabled = $9, last_check_at = NULL, last_check_ok = NULL,
	last_check_error = '', updated_at = $10
	WHERE id = $11 RETURNING `+credentialColumns,
		input.Name, input.ProviderID, input.Protocol, input.Endpoint, input.ModelID,
		ciphertext, nonce, lastFour(input.Secret), input.Enabled, time.Now().UTC(), id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.ManagedCredential{}, ErrResourceNotFound
	}
	return result, err
}

func (s *Store) DeleteCredential(ctx context.Context, id string) error {
	var referenced bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM agents WHERE credential_id = $1
    UNION ALL
    SELECT 1 FROM control_resources WHERE kind IN ('runtime', 'sandbox') AND spec->'credentialIds' ? $1
  )`, id).Scan(&referenced); err != nil {
		return fmt.Errorf("check credential bindings: %w", err)
	}
	if referenced {
		return fmt.Errorf("%w: credential is still referenced", ErrConflict)
	}
	result, err := s.pool.Exec(ctx, "DELETE FROM provider_credentials WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete provider credential: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrResourceNotFound
	}
	return nil
}

func (s *Store) CheckCredential(ctx context.Context, id string) (platform.ManagedCredential, error) {
	var providerID, protocol, endpoint string
	var ciphertext, nonce []byte
	if err := s.pool.QueryRow(ctx, `SELECT provider_id, protocol, endpoint,
    secret_ciphertext, secret_nonce FROM provider_credentials WHERE id = $1`, id).Scan(
		&providerID, &protocol, &endpoint, &ciphertext, &nonce,
	); errors.Is(err, pgx.ErrNoRows) {
		return platform.ManagedCredential{}, ErrResourceNotFound
	} else if err != nil {
		return platform.ManagedCredential{}, fmt.Errorf("load provider credential: %w", err)
	}
	secret, err := decryptSecret(s.secretKey, ciphertext, nonce)
	if err != nil {
		return platform.ManagedCredential{}, err
	}
	checkedAt := time.Now().UTC()
	checkErr := checkProviderCredential(ctx, providerID, protocol, endpoint, secret)
	checkOK := checkErr == nil
	checkMessage := ""
	if checkErr != nil {
		checkMessage = checkErr.Error()
		if len(checkMessage) > 500 {
			checkMessage = checkMessage[:500]
		}
	}
	result, err := scanCredential(s.pool.QueryRow(ctx, `UPDATE provider_credentials SET
    last_check_at = $1, last_check_ok = $2, last_check_error = $3, updated_at = $1
    WHERE id = $4 RETURNING `+credentialColumns, checkedAt, checkOK, checkMessage, id))
	if err != nil {
		return platform.ManagedCredential{}, fmt.Errorf("save provider credential check: %w", err)
	}
	return result, nil
}

func (s *Store) PullCredentialModels(ctx context.Context, id string) ([]platform.CredentialModel, error) {
	var providerID, protocol, endpoint string
	var ciphertext, nonce []byte
	if err := s.pool.QueryRow(ctx, `SELECT provider_id, protocol, endpoint,
    secret_ciphertext, secret_nonce FROM provider_credentials WHERE id = $1`, id).Scan(
		&providerID, &protocol, &endpoint, &ciphertext, &nonce,
	); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResourceNotFound
	} else if err != nil {
		return nil, fmt.Errorf("load provider credential: %w", err)
	}
	secret, err := decryptSecret(s.secretKey, ciphertext, nonce)
	if err != nil {
		return nil, err
	}
	if models := knownCredentialModels(protocol, endpoint); len(models) > 0 {
		return s.mutateCredentialModels(ctx, id, func(existing []platform.CredentialModel, defaultModelID string) ([]platform.CredentialModel, error) {
			return mergeCredentialModels(existing, models, defaultModelID), nil
		})
	}
	request, err := providerModelsRequest(ctx, providerID, protocol, endpoint, secret)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	response, err := safeProviderHTTPClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: 连接失败: %v", ErrProviderUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: Provider 返回 HTTP %d", ErrProviderUnavailable, response.StatusCode)
	}
	models, err := parseCredentialModels(response.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return s.mutateCredentialModels(ctx, id, func(existing []platform.CredentialModel, defaultModelID string) ([]platform.CredentialModel, error) {
		return mergeCredentialModels(existing, models, defaultModelID), nil
	})
}

func (s *Store) AddCredentialModel(ctx context.Context, id string, input platform.CredentialModelInput) ([]platform.CredentialModel, error) {
	platform.NormalizeCredentialModel(&input)
	if err := platform.ValidateCredentialModel(input); err != nil {
		return nil, err
	}
	return s.mutateCredentialModels(ctx, id, func(models []platform.CredentialModel, _ string) ([]platform.CredentialModel, error) {
		for _, model := range models {
			if model.ID == input.ID {
				return nil, fmt.Errorf("%w: model already exists", ErrConflict)
			}
		}
		models = append(models, platform.CredentialModel{
			ID: input.ID, Name: input.Name, Group: credentialModelGroup(input.ID, ""), Source: "manual",
		})
		sortCredentialModels(models)
		return models, nil
	})
}

func (s *Store) DeleteCredentialModel(ctx context.Context, id, modelID string) ([]platform.CredentialModel, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, &platform.ValidationError{Message: "模型 ID 不能为空"}
	}
	return s.mutateCredentialModels(ctx, id, func(models []platform.CredentialModel, defaultModelID string) ([]platform.CredentialModel, error) {
		if modelID == defaultModelID {
			return nil, fmt.Errorf("%w: default model cannot be deleted", ErrConflict)
		}
		result := make([]platform.CredentialModel, 0, len(models))
		found := false
		for _, model := range models {
			if model.ID == modelID {
				found = true
				continue
			}
			result = append(result, model)
		}
		if !found {
			return nil, ErrResourceNotFound
		}
		return result, nil
	})
}

func (s *Store) mutateCredentialModels(
	ctx context.Context,
	id string,
	mutate func([]platform.CredentialModel, string) ([]platform.CredentialModel, error),
) ([]platform.CredentialModel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin provider model update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var modelsJSON []byte
	var defaultModelID string
	if err := tx.QueryRow(ctx, `SELECT models, model_id FROM provider_credentials WHERE id = $1 FOR UPDATE`, id).Scan(
		&modelsJSON, &defaultModelID,
	); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResourceNotFound
	} else if err != nil {
		return nil, fmt.Errorf("load provider models: %w", err)
	}
	models, err := decodeCredentialModels(modelsJSON)
	if err != nil {
		return nil, err
	}
	models, err = mutate(models, defaultModelID)
	if err != nil {
		return nil, err
	}
	modelsJSON, err = json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("encode provider models: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE provider_credentials SET models = $1, updated_at = $2 WHERE id = $3`,
		modelsJSON, time.Now().UTC(), id,
	); err != nil {
		return nil, fmt.Errorf("save provider models: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit provider model update: %w", err)
	}
	return models, nil
}

func decodeCredentialModels(value []byte) ([]platform.CredentialModel, error) {
	models := []platform.CredentialModel{}
	if len(value) == 0 {
		return models, nil
	}
	if err := json.Unmarshal(value, &models); err != nil {
		return nil, fmt.Errorf("decode provider models: %w", err)
	}
	return models, nil
}

func mergeCredentialModels(existing, remote []platform.CredentialModel, defaultModelID string) []platform.CredentialModel {
	result := make([]platform.CredentialModel, 0, len(existing)+len(remote))
	seen := make(map[string]struct{}, len(existing)+len(remote))
	for _, model := range existing {
		if model.Source != "manual" && model.ID != defaultModelID {
			continue
		}
		if _, ok := seen[model.ID]; ok {
			continue
		}
		seen[model.ID] = struct{}{}
		result = append(result, model)
	}
	for _, model := range remote {
		if _, ok := seen[model.ID]; ok {
			continue
		}
		model.Source = "remote"
		if model.Group == "" {
			model.Group = credentialModelGroup(model.ID, "")
		}
		seen[model.ID] = struct{}{}
		result = append(result, model)
	}
	sortCredentialModels(result)
	return result
}

func sortCredentialModels(models []platform.CredentialModel) {
	sort.Slice(models, func(i, j int) bool {
		if models[i].Group != models[j].Group {
			return models[i].Group < models[j].Group
		}
		return models[i].ID < models[j].ID
	})
}

func credentialModelGroup(id, owner string) string {
	owner = strings.TrimSpace(owner)
	if owner != "" {
		return owner
	}
	id = strings.TrimSpace(strings.TrimPrefix(id, "models/"))
	for _, separator := range []string{"/", ":", "-"} {
		if before, _, ok := strings.Cut(id, separator); ok && before != "" {
			return before
		}
	}
	return "models"
}

func checkProviderCredential(ctx context.Context, providerID, protocol, endpoint, secret string) error {
	request, err := providerCheckRequest(ctx, providerID, protocol, endpoint, secret)
	if err != nil {
		return err
	}
	response, err := safeProviderHTTPClient().Do(request)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Provider 返回 HTTP %d", response.StatusCode)
	}
	return nil
}

func providerCheckRequest(ctx context.Context, providerID, protocol, endpoint, secret string) (*http.Request, error) {
	endpoint = normalizeKnownProviderEndpoint(protocol, endpoint)
	if protocol == "anthropic" && isKimiCodingEndpoint(endpoint) {
		body := strings.NewReader(`{"model":"kimi-for-coding","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			strings.TrimRight(endpoint, "/")+"/v1/messages",
			body,
		)
		if err != nil {
			return nil, errors.New("无法创建连接检测请求")
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("x-api-key", secret)
		request.Header.Set("anthropic-version", "2023-06-01")
		return request, nil
	}
	return providerModelsRequest(ctx, providerID, protocol, endpoint, secret)
}

func knownCredentialModels(protocol, endpoint string) []platform.CredentialModel {
	if protocol != "anthropic" || !isKimiCodingEndpoint(endpoint) {
		return nil
	}
	return []platform.CredentialModel{
		{ID: "k3", Name: "Kimi K3", Group: "Kimi Code", Source: "remote"},
		{ID: "k3-256k", Name: "Kimi K3 256K", Group: "Kimi Code", Source: "remote"},
		{ID: "kimi-for-coding", Name: "Kimi K2.7 Code", Group: "Kimi Code", Source: "remote"},
		{ID: "kimi-for-coding-highspeed", Name: "Kimi K2.7 Code HighSpeed", Group: "Kimi Code", Source: "remote"},
	}
}

func normalizeKnownProviderEndpoint(protocol, endpoint string) string {
	if kimiCodingEndpointPath(endpoint) == "" {
		return endpoint
	}
	switch protocol {
	case "anthropic":
		return "https://api.kimi.com/coding/"
	case "openai-chat", "openai-responses":
		return "https://api.kimi.com/coding/v1"
	default:
		return endpoint
	}
}

func isKimiCodingEndpoint(endpoint string) bool {
	return kimiCodingEndpointPath(endpoint) == "/coding"
}

func kimiCodingEndpointPath(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Hostname(), "api.kimi.com") || parsed.Port() != "" {
		return ""
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if (path != "/coding" && path != "/coding/v1") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	return path
}

func providerModelsRequest(ctx context.Context, providerID, protocol, endpoint, secret string) (*http.Request, error) {
	baseURL := strings.TrimRight(endpoint, "/")
	if baseURL == "" {
		switch providerID {
		case "openai":
			baseURL = "https://api.openai.com/v1"
		case "anthropic":
			baseURL = "https://api.anthropic.com/v1"
		case "google":
			baseURL = "https://generativelanguage.googleapis.com/v1beta"
		case "deepseek":
			baseURL = "https://api.deepseek.com"
		default:
			return nil, errors.New("该 Provider 未配置默认接口地址")
		}
	}
	modelsURL := baseURL + "/models"
	if protocol == "gemini" {
		parsed, err := url.Parse(modelsURL)
		if err != nil {
			return nil, errors.New("接口地址无效")
		}
		query := parsed.Query()
		query.Set("key", secret)
		parsed.RawQuery = query.Encode()
		modelsURL = parsed.String()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, errors.New("无法创建模型列表请求")
	}
	switch protocol {
	case "anthropic":
		request.Header.Set("x-api-key", secret)
		request.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
	default:
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	return request, nil
}

func parseCredentialModels(reader io.Reader) ([]platform.CredentialModel, error) {
	const maxResponseBytes = 4 << 20
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, errors.New("无法读取模型列表")
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("模型列表响应过大")
	}
	var payload struct {
		Data []struct {
			ID               string `json:"id"`
			OwnedBy          string `json:"owned_by"`
			DisplayName      string `json:"display_name"`
			DisplayNameCamel string `json:"displayName"`
		} `json:"data"`
		Models []struct {
			Name             string `json:"name"`
			OwnedBy          string `json:"owned_by"`
			DisplayName      string `json:"display_name"`
			DisplayNameCamel string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("Provider 返回的模型列表格式无效")
	}
	models := make([]platform.CredentialModel, 0, len(payload.Data)+len(payload.Models))
	seen := make(map[string]struct{})
	appendModel := func(id, owner, displayName, displayNameCamel string) {
		id = strings.TrimSpace(strings.TrimPrefix(id, "models/"))
		if id == "" || len(id) > 256 || len(models) >= 2000 {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(displayName)
		if name == "" {
			name = strings.TrimSpace(displayNameCamel)
		}
		if name == "" {
			name = id
		}
		models = append(models, platform.CredentialModel{
			ID: id, Name: name, Group: credentialModelGroup(id, owner), Source: "remote",
		})
	}
	for _, model := range payload.Data {
		appendModel(model.ID, model.OwnedBy, model.DisplayName, model.DisplayNameCamel)
	}
	for _, model := range payload.Models {
		appendModel(model.Name, model.OwnedBy, model.DisplayName, model.DisplayNameCamel)
	}
	sortCredentialModels(models)
	return models, nil
}

func safeProviderHTTPClient() *http.Client {
	allowPrivate := strings.EqualFold(os.Getenv("AGENTBOX_ALLOW_PRIVATE_PROVIDER_ENDPOINTS"), "true")
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, candidate := range addresses {
				ip := candidate.IP
				if !allowPrivate && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
					continue
				}
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				err = dialErr
			}
			if err != nil {
				return nil, err
			}
			return nil, errors.New("接口地址解析到不允许访问的内网地址")
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("Provider 验证不允许重定向")
		},
	}
}

func (s *Store) providerExists(providerID string) bool {
	for _, provider := range s.catalog.Providers {
		if provider.ID == providerID {
			return true
		}
	}
	return false
}

func (s *Store) validationCatalog(ctx context.Context) (agent.Catalog, error) {
	managed, err := s.ListCredentials(ctx)
	if err != nil {
		return agent.Catalog{}, err
	}
	managedIDs := make(map[string]bool, len(managed))
	credentials := make([]agent.Credential, 0, len(s.catalog.Credentials)+len(managed))
	for _, credential := range managed {
		managedIDs[credential.ID] = true
		status := "attention"
		if credential.Enabled {
			status = "configured"
		}
		credentials = append(credentials, agent.Credential{
			ID: credential.ID, Name: credential.Name, ProviderID: credential.ProviderID,
			Environment: credential.Endpoint, Status: status,
		})
	}
	for _, credential := range s.catalog.Credentials {
		if !managedIDs[credential.ID] {
			credentials = append(credentials, credential)
		}
	}
	catalog := s.catalog
	catalog.Credentials = credentials
	return catalog, nil
}

func lastFour(value string) string {
	if len(value) <= 4 {
		return value
	}
	return value[len(value)-4:]
}

func encryptSecret(key []byte, value string) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize credential encryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize credential cipher: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate credential nonce: %w", err)
	}
	return gcm.Seal(nil, nonce, []byte(value), nil), nonce, nil
}

func decryptSecret(key, ciphertext, nonce []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("initialize credential decryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("initialize credential cipher: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("decrypt provider credential")
	}
	return string(plaintext), nil
}

func loadSecretKey() ([]byte, error) {
	if encoded := strings.TrimSpace(os.Getenv("AGENTBOX_SECRET_KEY")); encoded != "" {
		return decodeSecretKey(encoded)
	}
	path := strings.TrimSpace(os.Getenv("AGENTBOX_SECRET_KEY_FILE"))
	if path == "" {
		path = ".agentbox-secret-key"
	}
	if data, err := os.ReadFile(path); err == nil {
		return decodeSecretKey(strings.TrimSpace(string(data)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read credential encryption key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate credential encryption key: %w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("persist credential encryption key: %w", err)
	}
	return key, nil
}

func decodeSecretKey(encoded string) ([]byte, error) {
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(encoded)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("AGENTBOX_SECRET_KEY must be a base64-encoded 32-byte key")
	}
	return key, nil
}

func (s *Store) ClaimWorkerJob(ctx context.Context, serverID, credential string) (platform.WorkerJob, error) {
	if _, err := uuid.Parse(serverID); err != nil || len(credential) < 32 {
		return platform.WorkerJob{}, ErrWorkerUnauthorized
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.WorkerJob{}, fmt.Errorf("begin claim worker job: %w", err)
	}
	defer tx.Rollback(ctx)
	var authenticated bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM managed_servers WHERE id = $1 AND credential_hash = $2
  )`, serverID, hashToken(credential)).Scan(&authenticated); err != nil {
		return platform.WorkerJob{}, fmt.Errorf("authenticate worker job claim: %w", err)
	}
	if !authenticated {
		return platform.WorkerJob{}, ErrWorkerUnauthorized
	}
	var job platform.WorkerJob
	var payloadJSON []byte
	err = tx.QueryRow(ctx, `SELECT id, resource_id, action, payload
    FROM worker_jobs
    WHERE server_id = $1
      AND (status = 'pending' OR (status = 'leased' AND lease_until < $2))
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1`, serverID, time.Now().UTC()).Scan(
		&job.ID, &job.ResourceID, &job.Action, &payloadJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.WorkerJob{}, ErrNoJob
	}
	if err != nil {
		return platform.WorkerJob{}, fmt.Errorf("select worker job: %w", err)
	}
	if err := json.Unmarshal(payloadJSON, &job.Payload); err != nil {
		return platform.WorkerJob{}, fmt.Errorf("decode worker job payload: %w", err)
	}
	if err := s.attachWorkerCredentials(ctx, tx, job.Payload); err != nil {
		return platform.WorkerJob{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE worker_jobs SET
    status = 'leased', lease_until = $1, attempts = attempts + 1, updated_at = $2
    WHERE id = $3`, now.Add(workerJobLeaseDuration), now, job.ID); err != nil {
		return platform.WorkerJob{}, fmt.Errorf("lease worker job: %w", err)
	}
	if job.Action == "create-sandbox" {
		if _, err := tx.Exec(ctx, `UPDATE control_resources SET
      spec = spec || jsonb_build_object(
        'status', 'starting'::text,
        'message', 'Worker 正在预配沙箱'::text
      ), updated_at = $1
      WHERE id = $2 AND kind = 'sandbox'`, now, job.ResourceID); err != nil {
			return platform.WorkerJob{}, fmt.Errorf("update claimed sandbox status: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.WorkerJob{}, fmt.Errorf("commit worker job claim: %w", err)
	}
	return job, nil
}

func (s *Store) attachWorkerCredentials(ctx context.Context, tx pgx.Tx, payload map[string]any) error {
	credentialIDs := specStringList(payload, "credentialIds")
	credentials := make([]map[string]any, 0, len(credentialIDs))
	for _, id := range credentialIDs {
		var providerID, protocol, endpoint, modelID string
		var ciphertext, nonce []byte
		err := tx.QueryRow(ctx, `SELECT provider_id, protocol, endpoint, model_id,
      secret_ciphertext, secret_nonce FROM provider_credentials
      WHERE id = $1 AND enabled = TRUE`, id).Scan(
			&providerID, &protocol, &endpoint, &modelID, &ciphertext, &nonce,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return &platform.ValidationError{Message: "环境引用了不可用的 API Key"}
		}
		if err != nil {
			return fmt.Errorf("load worker credential: %w", err)
		}
		secret, err := decryptSecret(s.secretKey, ciphertext, nonce)
		if err != nil {
			return err
		}
		endpoint = normalizeKnownProviderEndpoint(protocol, endpoint)
		credentials = append(credentials, map[string]any{
			"id": id, "providerId": providerID, "protocol": protocol,
			"endpoint": endpoint, "modelId": modelID, "secret": secret,
		})
	}
	payload["credentials"] = credentials
	return nil
}

func (s *Store) CompleteWorkerJob(ctx context.Context, serverID, credential, jobID string, result platform.WorkerJobResult) error {
	if _, err := uuid.Parse(serverID); err != nil || len(credential) < 32 {
		return ErrWorkerUnauthorized
	}
	if _, err := uuid.Parse(jobID); err != nil {
		return ErrResourceNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete worker job: %w", err)
	}
	defer tx.Rollback(ctx)
	var authenticated bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM managed_servers WHERE id = $1 AND credential_hash = $2
  )`, serverID, hashToken(credential)).Scan(&authenticated); err != nil {
		return fmt.Errorf("authenticate worker job completion: %w", err)
	}
	if !authenticated {
		return ErrWorkerUnauthorized
	}
	status := "failed"
	if result.Success {
		status = "succeeded"
	}
	var resourceID, action string
	err = tx.QueryRow(ctx, `UPDATE worker_jobs SET
    status = $1, lease_until = NULL, result_message = $2, external_id = $3, updated_at = $4
    WHERE id = $5 AND server_id = $6 AND status = 'leased'
    RETURNING resource_id, action`, status, result.Message, result.ExternalID,
		time.Now().UTC(), jobID, serverID,
	).Scan(&resourceID, &action)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceNotFound
	}
	if err != nil {
		return fmt.Errorf("complete worker job: %w", err)
	}
	if result.Success && action == "delete-sandbox" {
		if _, err := tx.Exec(ctx, `DELETE FROM control_resources
      WHERE id = $1 AND kind = 'sandbox'`, resourceID); err != nil {
			return fmt.Errorf("delete completed sandbox: %w", err)
		}
		return tx.Commit(ctx)
	}
	if action == "login-agent" {
		loginStatus := "error"
		if result.Success {
			loginStatus = "waiting"
		}
		if _, err := tx.Exec(ctx, `UPDATE control_resources SET
      spec = spec || jsonb_build_object(
        'loginStatus', $1::text,
        'loginMessage', $2::text
      ), updated_at = $3
      WHERE id = $4 AND kind = 'sandbox'`, loginStatus, result.Message,
			time.Now().UTC(), resourceID,
		); err != nil {
			return fmt.Errorf("update sandbox login status: %w", err)
		}
		return tx.Commit(ctx)
	}
	if strings.HasPrefix(action, "workspace-") {
		return tx.Commit(ctx)
	}
	sandboxStatus := "error"
	if result.Success {
		switch action {
		case "create-sandbox", "start-sandbox":
			sandboxStatus = "running"
		case "stop-sandbox":
			sandboxStatus = "stopped"
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE control_resources SET
    spec = spec || jsonb_build_object(
      'status', $1::text,
      'externalId', $2::text,
      'message', $3::text
    ), updated_at = $4
    WHERE id = $5 AND kind = 'sandbox'`, sandboxStatus, result.ExternalID,
		result.Message, time.Now().UTC(), resourceID,
	); err != nil {
		return fmt.Errorf("update sandbox status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit worker job completion: %w", err)
	}
	return nil
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.Resource{}, fmt.Errorf("begin create resource: %w", err)
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	result, err := scanResource(tx.QueryRow(ctx, `INSERT INTO control_resources
    (id, kind, project_id, name, description, enabled, spec, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $8)
    RETURNING `+resourceColumns, input.ID, input.Kind, input.ProjectID, input.Name,
		input.Description, input.Enabled, mustMapJSON(input.Spec), now))
	if err != nil {
		return platform.Resource{}, mapResourceError(err)
	}
	if input.Kind == platform.KindSandbox {
		if err := enqueueSandboxJob(ctx, tx, result); err != nil {
			return platform.Resource{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.Resource{}, mapResourceError(err)
	}
	return result, nil
}

func enqueueSandboxJob(ctx context.Context, tx pgx.Tx, sandbox platform.Resource) error {
	runtimeID, _ := sandbox.Spec["runtimeId"].(string)
	serverID, _ := sandbox.Spec["serverId"].(string)
	var runtimeSpecJSON []byte
	if err := tx.QueryRow(ctx, `SELECT spec FROM control_resources
    WHERE id = $1 AND kind = 'runtime'`, runtimeID).Scan(&runtimeSpecJSON); err != nil {
		return fmt.Errorf("load sandbox environment template: %w", err)
	}
	var runtimeSpec map[string]any
	if err := json.Unmarshal(runtimeSpecJSON, &runtimeSpec); err != nil {
		return fmt.Errorf("decode sandbox environment template: %w", err)
	}
	imageReference, _ := runtimeSpec["imageReference"].(string)
	if imageReference == "" {
		imageID, _ := runtimeSpec["imageId"].(string)
		if err := tx.QueryRow(ctx, `SELECT spec->>'reference' FROM control_resources
      WHERE id = $1 AND kind = 'image' AND enabled = TRUE`, imageID).Scan(&imageReference); err != nil {
			return fmt.Errorf("load sandbox image: %w", err)
		}
	}
	agentTools := runtimeSpec["agentTools"]
	if sandboxTools, ok := sandbox.Spec["agentTools"]; ok {
		agentTools = sandboxTools
	}
	credentialIDs := runtimeSpec["credentialIds"]
	if sandboxCredentials, ok := sandbox.Spec["credentialIds"]; ok {
		credentialIDs = sandboxCredentials
	}
	payload := map[string]any{
		"sandboxId":     sandbox.ID,
		"name":          sandbox.Name,
		"driver":        runtimeSpec["driver"],
		"image":         imageReference,
		"workdir":       runtimeSpec["workdir"],
		"setup":         runtimeSpec["setup"],
		"cpu":           runtimeSpec["cpu"],
		"memory":        runtimeSpec["memory"],
		"network":       runtimeSpec["network"],
		"agentTools":    agentTools,
		"skillIds":      runtimeSpec["skillIds"],
		"mcpServerIds":  runtimeSpec["mcpServerIds"],
		"variableIds":   runtimeSpec["variableIds"],
		"credentialIds": credentialIDs,
		"workspace":     sandbox.Spec["workspace"],
	}
	for _, definitions := range []struct {
		payloadKey string
		kind       platform.Kind
		ids        []string
	}{
		{payloadKey: "skills", kind: platform.KindSkill, ids: specStringList(runtimeSpec, "skillIds")},
		{payloadKey: "mcpServers", kind: platform.KindMCP, ids: specStringList(runtimeSpec, "mcpServerIds")},
		{payloadKey: "variables", kind: platform.KindVariable, ids: specStringList(runtimeSpec, "variableIds")},
	} {
		resources, err := loadWorkerResourceDefinitions(ctx, tx, definitions.kind, definitions.ids)
		if err != nil {
			return err
		}
		payload[definitions.payloadKey] = resources
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `INSERT INTO worker_jobs
    (id, server_id, resource_id, action, status, payload, created_at, updated_at)
    VALUES ($1, $2, $3, 'create-sandbox', 'pending', $4::jsonb, $5, $5)`,
		uuid.NewString(), serverID, sandbox.ID, mustMapJSON(payload), now,
	); err != nil {
		return fmt.Errorf("enqueue sandbox creation: %w", err)
	}
	return nil
}

func loadWorkerResourceDefinitions(
	ctx context.Context,
	tx pgx.Tx,
	kind platform.Kind,
	ids []string,
) ([]map[string]any, error) {
	definitions := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		var name, description string
		var specJSON []byte
		err := tx.QueryRow(ctx, `SELECT name, description, spec
      FROM control_resources WHERE id = $1 AND kind = $2 AND enabled = TRUE`, id, kind).Scan(
			&name, &description, &specJSON,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &platform.ValidationError{Message: fmt.Sprintf("环境引用了不可用的 %s: %s", kind, id)}
		}
		if err != nil {
			return nil, fmt.Errorf("load worker %s definition: %w", kind, err)
		}
		var spec map[string]any
		if err := json.Unmarshal(specJSON, &spec); err != nil {
			return nil, fmt.Errorf("decode worker %s definition: %w", kind, err)
		}
		definitions = append(definitions, map[string]any{
			"id": id, "name": name, "description": description, "spec": spec,
		})
	}
	return definitions, nil
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

func (s *Store) OperateSandbox(ctx context.Context, id, action string) (platform.Resource, error) {
	workerAction := ""
	pendingStatus := ""
	message := ""
	loginTool := ""
	switch action {
	case "start":
		workerAction, pendingStatus, message = "start-sandbox", "starting", "正在启动沙箱"
	case "stop":
		workerAction, pendingStatus, message = "stop-sandbox", "stopping", "正在停止沙箱"
	case "delete":
		workerAction, pendingStatus, message = "delete-sandbox", "deleting", "正在删除沙箱"
	case "login-codex":
		workerAction, message, loginTool = "login-agent", "正在发起 Codex 设备登录", "codex"
	default:
		return platform.Resource{}, &platform.ValidationError{Message: "不支持的沙箱操作"}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.Resource{}, fmt.Errorf("begin sandbox operation: %w", err)
	}
	defer tx.Rollback(ctx)
	var resource platform.Resource
	var project pgtype.Text
	var specJSON []byte
	err = tx.QueryRow(ctx, `SELECT `+resourceColumns+` FROM control_resources
    WHERE id = $1 AND kind = 'sandbox' FOR UPDATE`, id).Scan(
		&resource.ID, &resource.Kind, &project, &resource.Name, &resource.Description,
		&resource.Enabled, &specJSON, &resource.CreatedAt, &resource.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Resource{}, ErrResourceNotFound
	}
	if err != nil {
		return platform.Resource{}, fmt.Errorf("load sandbox for operation: %w", err)
	}
	if project.Valid {
		resource.ProjectID = &project.String
	}
	if err := json.Unmarshal(specJSON, &resource.Spec); err != nil {
		return platform.Resource{}, fmt.Errorf("decode sandbox operation: %w", err)
	}
	status, _ := resource.Spec["status"].(string)
	if status == "requested" || status == "starting" || status == "stopping" || status == "deleting" {
		return platform.Resource{}, fmt.Errorf("%w: sandbox already has an operation in progress", ErrConflict)
	}
	if action == "start" && status == "running" {
		return platform.Resource{}, fmt.Errorf("%w: sandbox is already running", ErrConflict)
	}
	if action == "stop" && status != "running" {
		return platform.Resource{}, fmt.Errorf("%w: sandbox is not running", ErrConflict)
	}
	if action == "login-codex" && status != "running" {
		return platform.Resource{}, fmt.Errorf("%w: sandbox must be running before login", ErrConflict)
	}
	serverID, _ := resource.Spec["serverId"].(string)
	payload := map[string]any{
		"sandboxId":  id,
		"externalId": resource.Spec["externalId"],
		"tool":       loginTool,
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `INSERT INTO worker_jobs
    (id, server_id, resource_id, action, status, payload, created_at, updated_at)
    VALUES ($1, $2, $3, $4, 'pending', $5::jsonb, $6, $6)`,
		uuid.NewString(), serverID, id, workerAction, mustMapJSON(payload), now,
	); err != nil {
		return platform.Resource{}, fmt.Errorf("enqueue sandbox operation: %w", err)
	}
	if action == "login-codex" {
		resource, err = scanResource(tx.QueryRow(ctx, `UPDATE control_resources SET
      spec = spec || jsonb_build_object(
        'loginStatus', 'starting'::text,
        'loginTool', $1::text,
        'loginMessage', $2::text
      ), updated_at = $3 WHERE id = $4 RETURNING `+resourceColumns,
			loginTool, message, now, id,
		))
	} else {
		resource, err = scanResource(tx.QueryRow(ctx, `UPDATE control_resources SET
      spec = spec || jsonb_build_object('status', $1::text, 'message', $2::text),
      updated_at = $3 WHERE id = $4 RETURNING `+resourceColumns,
			pendingStatus, message, now, id,
		))
	}
	if err != nil {
		return platform.Resource{}, fmt.Errorf("update sandbox operation status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.Resource{}, fmt.Errorf("commit sandbox operation: %w", err)
	}
	return resource, nil
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
	case platform.KindImage:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM control_resources WHERE kind = 'runtime' AND spec->>'imageId' = $1)"
	case platform.KindRuntime:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE runtime_id = $1 UNION ALL SELECT 1 FROM control_resources WHERE kind = 'sandbox' AND spec->>'runtimeId' = $1)"
	case platform.KindSkill:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE skill_ids ? $1 UNION ALL SELECT 1 FROM control_resources WHERE kind = 'runtime' AND spec->'skillIds' ? $1)"
	case platform.KindMCP:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE mcp_server_ids ? $1 UNION ALL SELECT 1 FROM control_resources WHERE kind = 'runtime' AND spec->'mcpServerIds' ? $1)"
	case platform.KindVariable:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM agents WHERE variable_ids ? $1 UNION ALL SELECT 1 FROM control_resources WHERE kind = 'runtime' AND spec->'variableIds' ? $1)"
	case platform.KindSandbox:
		referenceQuery = "SELECT EXISTS(SELECT 1 FROM control_resources WHERE id = $1 AND kind = 'sandbox' AND spec->>'status' = 'running')"
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
	if input.Kind == platform.KindProject || input.Kind == platform.KindImage {
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
	if input.Kind == platform.KindImage {
		rows, err := s.pool.Query(ctx, `SELECT spec->>'driver' FROM control_resources
      WHERE kind = 'runtime' AND spec->>'imageId' = $1`, input.ID)
		if err != nil {
			return fmt.Errorf("list image runtimes: %w", err)
		}
		defer rows.Close()
		modes := map[string]bool{}
		for _, mode := range specStringList(input.Spec, "modes") {
			modes[mode] = true
		}
		for rows.Next() {
			var driver string
			if err := rows.Scan(&driver); err != nil {
				return fmt.Errorf("scan image runtime: %w", err)
			}
			if !input.Enabled {
				return &platform.ValidationError{Message: "镜像仍被 Runtime 使用，不能停用"}
			}
			if !modes[driver] {
				return &platform.ValidationError{Message: "镜像仍被不兼容的 Runtime 使用"}
			}
		}
		return rows.Err()
	}
	if input.Kind == platform.KindRuntime {
		serverID, _ := input.Spec["serverId"].(string)
		driver, _ := input.Spec["driver"].(string)
		requiredCapability := driver
		if driver == "vm" {
			requiredCapability = "kvm"
		}
		var serverArch string
		var serverSupports bool
		var inventoryJSON []byte
		err := s.pool.QueryRow(ctx, `SELECT arch, capabilities ? $2, inventory
      FROM managed_servers WHERE id = $1`, serverID, requiredCapability).Scan(&serverArch, &serverSupports, &inventoryJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return &platform.ValidationError{Message: "运行服务器不存在"}
		}
		if err != nil {
			return fmt.Errorf("check environment server: %w", err)
		}
		if !serverSupports {
			if driver == "vm" {
				return &platform.ValidationError{Message: "运行服务器尚未配置可用的 KVM/QEMU 后端"}
			}
			return &platform.ValidationError{Message: "运行服务器不支持 Docker"}
		}
		var inventory platform.ServerInventory
		if err := json.Unmarshal(inventoryJSON, &inventory); err != nil {
			return fmt.Errorf("decode environment server inventory: %w", err)
		}
		imageReference, _ := input.Spec["imageReference"].(string)
		if !runtimeImageIsAvailable(driver, inventory, imageReference, serverArch) {
			return &platform.ValidationError{Message: "所选镜像已不在运行服务器上，请刷新后重新选择"}
		}
		for kind, key := range map[platform.Kind]string{
			platform.KindSkill:    "skillIds",
			platform.KindMCP:      "mcpServerIds",
			platform.KindVariable: "variableIds",
		} {
			for _, resourceID := range specStringList(input.Spec, key) {
				var exists bool
				if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
            SELECT 1 FROM control_resources
            WHERE id = $1 AND kind = $2 AND enabled = TRUE
          )`, resourceID, kind).Scan(&exists); err != nil {
					return fmt.Errorf("check environment binding: %w", err)
				}
				if !exists {
					return &platform.ValidationError{Message: "环境模板包含不存在或已停用的能力配置"}
				}
			}
		}
		for _, credentialID := range specStringList(input.Spec, "credentialIds") {
			var exists bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
          SELECT 1 FROM provider_credentials WHERE id = $1 AND enabled = TRUE
        )`, credentialID).Scan(&exists); err != nil {
				return fmt.Errorf("check environment credential: %w", err)
			}
			if !exists {
				return &platform.ValidationError{Message: "环境模板包含不存在或已停用的 API Key"}
			}
		}
		return nil
	}
	if input.Kind != platform.KindSandbox && input.Kind != platform.KindSchedule && input.Kind != platform.KindWebhook {
		return nil
	}
	if input.Kind != platform.KindSandbox {
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
	}
	if input.Kind == platform.KindSandbox {
		runtimeID, _ := input.Spec["runtimeId"].(string)
		var runtimeSpecJSON []byte
		if err := s.pool.QueryRow(ctx, `SELECT spec FROM control_resources
      WHERE id = $1 AND kind = 'runtime' AND project_id = $2 AND enabled = TRUE`, runtimeID, input.ProjectID).Scan(&runtimeSpecJSON); errors.Is(err, pgx.ErrNoRows) {
			return &platform.ValidationError{Message: "环境模板不存在、未启用或不属于当前 Project"}
		} else if err != nil {
			return fmt.Errorf("check sandbox runtime: %w", err)
		}
		var runtimeSpec map[string]any
		if err := json.Unmarshal(runtimeSpecJSON, &runtimeSpec); err != nil {
			return fmt.Errorf("decode sandbox environment: %w", err)
		}
		serverID, _ := input.Spec["serverId"].(string)
		runtimeServerID, _ := runtimeSpec["serverId"].(string)
		if runtimeServerID != "" && runtimeServerID != serverID {
			return &platform.ValidationError{Message: "环境模板绑定了另一台运行服务器"}
		}
		driver, _ := runtimeSpec["driver"].(string)
		requiredCapability := driver
		if driver == "vm" {
			requiredCapability = "kvm"
		}
		var supports bool
		var serverArch string
		var inventoryJSON []byte
		if err := s.pool.QueryRow(ctx, `SELECT capabilities ? $2, arch, inventory
      FROM managed_servers WHERE id = $1`, serverID, requiredCapability).Scan(&supports, &serverArch, &inventoryJSON); errors.Is(err, pgx.ErrNoRows) {
			return &platform.ValidationError{Message: "目标服务器不存在"}
		} else if err != nil {
			return fmt.Errorf("check sandbox server: %w", err)
		}
		if !supports {
			return &platform.ValidationError{Message: "目标服务器不支持环境模板的隔离类型"}
		}
		imageReference, _ := runtimeSpec["imageReference"].(string)
		if imageReference == "" {
			imageID, _ := runtimeSpec["imageId"].(string)
			if err := s.pool.QueryRow(ctx, `SELECT spec->>'reference' FROM control_resources
          WHERE id = $1 AND kind = 'image' AND enabled = TRUE`, imageID).Scan(&imageReference); err != nil {
				return &platform.ValidationError{Message: "环境模板引用的旧镜像不存在"}
			}
		}
		var inventory platform.ServerInventory
		if err := json.Unmarshal(inventoryJSON, &inventory); err != nil {
			return fmt.Errorf("decode sandbox server inventory: %w", err)
		}
		if !runtimeImageIsAvailable(driver, inventory, imageReference, serverArch) {
			return &platform.ValidationError{Message: "环境模板使用的镜像已不在目标服务器上"}
		}
		for _, credentialID := range specStringList(input.Spec, "credentialIds") {
			var exists bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
          SELECT 1 FROM provider_credentials WHERE id = $1 AND enabled = TRUE
        )`, credentialID).Scan(&exists); err != nil {
				return fmt.Errorf("check sandbox credential: %w", err)
			}
			if !exists {
				return &platform.ValidationError{Message: "沙箱包含不存在或已停用的模型凭据"}
			}
		}
	}
	return nil
}

func runtimeImageIsAvailable(driver string, inventory platform.ServerInventory, reference, serverArch string) bool {
	if driver == "docker" {
		return strings.TrimSpace(reference) != ""
	}
	return serverInventoryHasImage(inventory.VMImages, reference, serverArch)
}

func serverInventoryHasImage(images []platform.ServerImage, reference, serverArch string) bool {
	for _, image := range images {
		matches := reference == image.Reference || reference == image.Path || reference == image.ID
		architectureMatches := image.Architecture == "" || image.Architecture == "all" || image.Architecture == serverArch
		if matches && architectureMatches {
			return true
		}
	}
	return false
}

func specStringList(spec map[string]any, key string) []string {
	values, ok := spec[key].([]any)
	if !ok {
		if values, ok := spec[key].([]string); ok {
			return values
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value, ok := value.(string); ok {
			result = append(result, value)
		}
	}
	return result
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
