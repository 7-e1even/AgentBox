package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/agent"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

var (
	ErrNotFound = errors.New("agent not found")
	ErrConflict = errors.New("agent conflict")
)

const columns = `
  id, project_id, name, slug, description, avatar, provider_id, model_id,
  credential_id, system_prompt, skill_ids, mcp_server_ids, temperature,
  max_steps, status, version, created_at, updated_at`

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
			Name: "Research Copilot", Slug: "research-copilot", Description: "收集可信来源，比较观点并整理成可执行的研究结论。", Avatar: "RC",
			ProviderID: "openai", ModelID: "gpt-5", CredentialID: &openAICredential,
			SystemPrompt: "你是一名严谨的研究助理。先明确问题和证据标准，再检索并交叉验证来源，清楚区分事实、推断与未知信息。",
			SkillIDs:     []string{"web-research", "document-writer", "task-planner"}, MCPServerIDs: []string{"browser", "filesystem"},
			Temperature: 0.3, MaxSteps: 16, Status: agent.StatusActive,
		},
		{
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

func scanAgent(row pgx.Row) (agent.Agent, error) {
	var result agent.Agent
	var credential pgtype.Text
	var skillJSON []byte
	var mcpJSON []byte
	err := row.Scan(
		&result.ID, &result.ProjectID, &result.Name, &result.Slug, &result.Description,
		&result.Avatar, &result.ProviderID, &result.ModelID, &credential,
		&result.SystemPrompt, &skillJSON, &mcpJSON, &result.Temperature,
		&result.MaxSteps, &result.Status, &result.Version, &result.CreatedAt, &result.UpdatedAt,
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
    id, project_id, name, slug, description, avatar, provider_id, model_id,
    credential_id, system_prompt, skill_ids, mcp_server_ids, temperature,
    max_steps, status, version, created_at, updated_at
  ) VALUES ($1, 'default', $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb,
    $11::jsonb, $12, $13, $14, 1, $15, $15)
  RETURNING `+columns,
		uuid.NewString(), input.Name, input.Slug, input.Description, input.Avatar,
		input.ProviderID, input.ModelID, input.CredentialID, input.SystemPrompt,
		mustJSON(input.SkillIDs), mustJSON(input.MCPServerIDs), input.Temperature,
		input.MaxSteps, input.Status, now,
	))
}

func (s *Store) Update(ctx context.Context, id string, input agent.Input, expectedVersion int) (agent.Agent, error) {
	agent.Normalize(&input)
	if err := agent.Validate(input, s.catalog); err != nil {
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
    name = $1, slug = $2, description = $3, avatar = $4, provider_id = $5,
    model_id = $6, credential_id = $7, system_prompt = $8, skill_ids = $9::jsonb,
    mcp_server_ids = $10::jsonb, temperature = $11, max_steps = $12,
    status = $13, version = version + 1, updated_at = $14
  WHERE id = $15 AND version = $16 RETURNING `+columns,
		input.Name, input.Slug, input.Description, input.Avatar, input.ProviderID,
		input.ModelID, input.CredentialID, input.SystemPrompt, mustJSON(input.SkillIDs),
		mustJSON(input.MCPServerIDs), input.Temperature, input.MaxSteps, input.Status,
		time.Now().UTC(), id, expectedVersion,
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

func mapDatabaseError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%w: agent slug already exists", ErrConflict)
	}
	return err
}
