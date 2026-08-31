package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const userColumns = `id, name, username, email, role, status, preferences, last_login_at, created_at, updated_at`

// 用户会话有效期（7 天滚动）：剩余不足一半时在认证时滑动顺延。
const (
	userSessionLifetime         = 7 * 24 * time.Hour
	userSessionRenewalThreshold = userSessionLifetime / 2
)

// dummyLoginPasswordHash 用于用户名不存在时的占位 bcrypt 比较，消除时序侧信道。
var dummyLoginPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("agentbox-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

func (s *Store) NeedsUserSetup(ctx context.Context) (bool, error) {
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count == 0, nil
}

func (s *Store) SetupAdmin(ctx context.Context, input platform.UserInput, sessionHash []byte, expiresAt time.Time) (platform.User, error) {
	platform.NormalizeUserInput(&input)
	input.Role = platform.UserRoleAdmin
	input.Status = platform.UserStatusActive
	if err := platform.ValidateUserInput(input, true); err != nil {
		return platform.User{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return platform.User{}, fmt.Errorf("hash password: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.User{}, fmt.Errorf("begin user setup: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x41474255534552)); err != nil {
		return platform.User{}, fmt.Errorf("lock user setup: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return platform.User{}, fmt.Errorf("count users: %w", err)
	}
	if count != 0 {
		return platform.User{}, ErrConflict
	}
	now := time.Now().UTC()
	user, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users
    (id, name, username, email, password_hash, role, status, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
    RETURNING `+userColumns, uuid.NewString(), input.Name, input.Username, input.Email, passwordHash, input.Role, input.Status, now))
	if err != nil {
		return platform.User{}, mapUserError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_sessions (token_hash, user_id, expires_at, created_at)
    VALUES ($1, $2, $3, $4)`, sessionHash, user.ID, expiresAt, now); err != nil {
		return platform.User{}, fmt.Errorf("create setup session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.User{}, fmt.Errorf("commit user setup: %w", err)
	}
	return user, nil
}

func (s *Store) AuthenticateUser(ctx context.Context, username, password string, sessionHash []byte, expiresAt time.Time) (platform.User, error) {
	username = normalizeUsername(username)
	var passwordHash []byte
	user, err := scanUserWithPassword(s.pool.QueryRow(ctx, `SELECT `+userColumns+`, password_hash
    FROM users WHERE LOWER(username) = $1`, username), &passwordHash)
	// 用户名不存在时也执行一次 dummy bcrypt 比较，避免通过响应时间探测账号是否注册。
	compareHash := passwordHash
	if err != nil {
		compareHash = dummyLoginPasswordHash
	}
	if bcrypt.CompareHashAndPassword(compareHash, []byte(password)) != nil || err != nil || user.Status != platform.UserStatusActive {
		return platform.User{}, ErrUnauthorized
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.User{}, fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback(ctx)
	var lockedPasswordHash []byte
	lockedUser, err := scanUserWithPassword(tx.QueryRow(ctx, `SELECT `+userColumns+`, password_hash
    FROM users WHERE id = $1 AND LOWER(username) = $2 FOR UPDATE`, user.ID, username), &lockedPasswordHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.User{}, ErrUnauthorized
		}
		return platform.User{}, fmt.Errorf("lock login user: %w", err)
	}
	if lockedUser.Status != platform.UserStatusActive || !bytes.Equal(lockedPasswordHash, passwordHash) {
		return platform.User{}, ErrUnauthorized
	}
	user = lockedUser
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at = $2, updated_at = updated_at WHERE id = $1`, user.ID, now); err != nil {
		return platform.User{}, fmt.Errorf("record login: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_sessions WHERE expires_at <= $1`, now); err != nil {
		return platform.User{}, fmt.Errorf("expire sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_sessions (token_hash, user_id, expires_at, created_at)
    VALUES ($1, $2, $3, $4)`, sessionHash, user.ID, expiresAt, now); err != nil {
		return platform.User{}, fmt.Errorf("create session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.User{}, fmt.Errorf("commit login: %w", err)
	}
	user.LastLoginAt = &now
	return user, nil
}

func (s *Store) UserBySession(ctx context.Context, sessionHash []byte) (platform.User, error) {
	now := time.Now().UTC()
	user, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+prefixedUserColumns("u")+`
    FROM user_sessions s
    JOIN users u ON u.id = s.user_id
    WHERE s.token_hash = $1 AND s.expires_at > $2 AND u.status = 'active'`, sessionHash, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.User{}, ErrUnauthorized
	}
	if err != nil {
		return platform.User{}, fmt.Errorf("find session user: %w", err)
	}
	// 滑动续期：剩余有效期不足一半时顺延至 7 天（滚动上限不变）。
	// 续期失败不影响本次认证结果。
	_, _ = s.pool.Exec(ctx, `UPDATE user_sessions SET expires_at = $2
    WHERE token_hash = $1 AND expires_at > $3 AND expires_at < $4`,
		sessionHash, now.Add(userSessionLifetime), now, now.Add(userSessionRenewalThreshold))
	return user, nil
}

func (s *Store) DeleteSession(ctx context.Context, sessionHash []byte) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM user_sessions WHERE token_hash = $1", sessionHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) ListUsers(ctx context.Context) ([]platform.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM users
    ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, name, username`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]platform.User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, input platform.UserInput) (platform.User, error) {
	platform.NormalizeUserInput(&input)
	if err := platform.ValidateUserInput(input, true); err != nil {
		return platform.User{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return platform.User{}, fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().UTC()
	user, err := scanUser(s.pool.QueryRow(ctx, `INSERT INTO users
    (id, name, username, email, password_hash, role, status, created_at, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
    RETURNING `+userColumns, uuid.NewString(), input.Name, input.Username, input.Email, passwordHash, input.Role, input.Status, now))
	if err != nil {
		return platform.User{}, mapUserError(err)
	}
	return user, nil
}

func (s *Store) UpdateUser(ctx context.Context, id string, input platform.UserInput) (platform.User, error) {
	return s.updateUser(ctx, id, input, nil)
}

// UpdateUserPreservingSession 与 UpdateUser 相同，但密码变更/停用导致会话失效时
// 保留 keepSessionHash 对应的会话（用户自己改密时不踢掉当前登录）。
func (s *Store) UpdateUserPreservingSession(ctx context.Context, id string, input platform.UserInput, keepSessionHash []byte) (platform.User, error) {
	return s.updateUser(ctx, id, input, keepSessionHash)
}

func (s *Store) updateUser(ctx context.Context, id string, input platform.UserInput, keepSessionHash []byte) (platform.User, error) {
	platform.NormalizeUserInput(&input)
	if err := platform.ValidateUserInput(input, false); err != nil {
		return platform.User{}, err
	}
	var passwordHash []byte
	if input.Password != "" {
		var err error
		passwordHash, err = bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			return platform.User{}, fmt.Errorf("hash password: %w", err)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return platform.User{}, fmt.Errorf("begin update user: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	var row pgx.Row
	if input.Password == "" {
		row = tx.QueryRow(ctx, `UPDATE users SET name = $2, username = $3, email = $4, role = $5, status = $6, updated_at = $7
      WHERE id = $1 RETURNING `+userColumns, id, input.Name, input.Username, input.Email, input.Role, input.Status, now)
	} else {
		row = tx.QueryRow(ctx, `UPDATE users SET name = $2, username = $3, email = $4, password_hash = $5, role = $6, status = $7, updated_at = $8
      WHERE id = $1 RETURNING `+userColumns, id, input.Name, input.Username, input.Email, passwordHash, input.Role, input.Status, now)
	}
	user, err := scanUser(row)
	if err != nil {
		return platform.User{}, mapUserError(err)
	}
	if input.Password != "" || input.Status == platform.UserStatusDisabled {
		if len(keepSessionHash) == 0 {
			if _, err := tx.Exec(ctx, "DELETE FROM user_sessions WHERE user_id = $1", id); err != nil {
				return platform.User{}, fmt.Errorf("invalidate user sessions: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, "DELETE FROM user_sessions WHERE user_id = $1 AND token_hash <> $2", id, keepSessionHash); err != nil {
				return platform.User{}, fmt.Errorf("invalidate user sessions: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return platform.User{}, fmt.Errorf("commit update user: %w", err)
	}
	return user, nil
}

func (s *Store) UpdateUserPreferences(ctx context.Context, id string, input platform.UserPreferences) (platform.User, error) {
	if err := platform.ValidateUserPreferences(input); err != nil {
		return platform.User{}, err
	}
	user, err := scanUser(s.pool.QueryRow(ctx, `UPDATE users SET preferences = $2, updated_at = $3
      WHERE id = $1 RETURNING `+userColumns, id, input, time.Now().UTC()))
	if err != nil {
		return platform.User{}, mapUserError(err)
	}
	return user, nil
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	result, err := s.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanUser(row pgx.Row) (platform.User, error) {
	var user platform.User
	err := row.Scan(&user.ID, &user.Name, &user.Username, &user.Email, &user.Role, &user.Status, &user.Preferences, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt)
	return user, err
}

func scanUserWithPassword(row pgx.Row, passwordHash *[]byte) (platform.User, error) {
	var user platform.User
	err := row.Scan(&user.ID, &user.Name, &user.Username, &user.Email, &user.Role, &user.Status, &user.Preferences, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt, passwordHash)
	return user, err
}

func prefixedUserColumns(alias string) string {
	return alias + `.id, ` + alias + `.name, ` + alias + `.username, ` + alias + `.email, ` + alias + `.role, ` + alias + `.status, ` + alias + `.preferences, ` + alias + `.last_login_at, ` + alias + `.created_at, ` + alias + `.updated_at`
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func mapUserError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return ErrConflict
	}
	return err
}
