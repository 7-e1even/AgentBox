package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/platform"
	"agentbox/internal/store"
)

const (
	sessionCookieName = "agentbox_session"
	sessionLifetime   = 7 * 24 * time.Hour
)

type userContextKey struct{}

func (s *Server) authStatus(w http.ResponseWriter, request *http.Request) {
	needsSetup, err := s.store.NeedsUserSetup(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"needsSetup": needsSetup})
}

func (s *Server) setupAdmin(w http.ResponseWriter, request *http.Request) {
	var input platform.UserInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		s.handleError(w, err)
		return
	}
	expiresAt := time.Now().UTC().Add(sessionLifetime)
	user, err := s.store.SetupAdmin(request.Context(), input, tokenHash, expiresAt)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAuth, Action: "setup-admin",
			Message: "初始化管理员失败", Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error(), "username": input.Username},
		})
		s.handleError(w, err)
		return
	}
	s.setSessionCookie(w, request, token, expiresAt)
	s.writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) login(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !s.decodeJSON(w, request, &input) {
		return
	}
	if strings.TrimSpace(input.Username) == "" || input.Password == "" {
		s.writeError(w, http.StatusBadRequest, "请输入用户名和密码")
		return
	}
	if !s.loginLimiter.allow(s.clientIP(request), time.Now().UTC()) {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAuth, Action: "login",
			Message: "登录尝试过于频繁", Status: platform.LogStatusFailed,
			Detail: map[string]any{"username": input.Username, "reason": "rate-limited"},
		})
		w.Header().Set("Retry-After", "60")
		s.writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		s.handleError(w, err)
		return
	}
	expiresAt := time.Now().UTC().Add(sessionLifetime)
	user, err := s.store.AuthenticateUser(request.Context(), input.Username, input.Password, tokenHash, expiresAt)
	if err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			s.recordLog(request, platform.LogEntry{
				Level: platform.LogLevelWarn, Category: platform.LogCategoryAuth, Action: "login",
				Message: "登录失败：" + input.Username, Status: platform.LogStatusFailed,
				Detail: map[string]any{"username": input.Username, "reason": "用户名或密码错误，或账号已停用"},
			})
			s.writeError(w, http.StatusUnauthorized, "用户名或密码错误，或账号已停用")
			return
		}
		s.handleError(w, err)
		return
	}
	s.setSessionCookie(w, request, token, expiresAt)
	s.writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) currentUser(w http.ResponseWriter, request *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{"user": userFromContext(request.Context())})
}

func (s *Server) updateCurrentUser(w http.ResponseWriter, request *http.Request) {
	var input platform.UserInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	current := userFromContext(request.Context())
	input.Role = current.Role
	input.Status = current.Status
	entry := platform.LogEntry{
		Category: platform.LogCategoryAuth, Action: "update-profile",
		Message:      "更新个人资料：" + current.Username,
		ResourceKind: "user", ResourceID: current.ID, ResourceName: current.Name,
		Detail: map[string]any{"passwordChanged": input.Password != ""},
	}
	// 修改自己的密码时保留当前会话（不踢掉本次登录），其余会话仍失效。
	if input.Password != "" {
		if preserver, ok := s.store.(sessionPreservingUserStore); ok {
			keepHash, _ := sessionHash(request)
			user, err := preserver.UpdateUserPreservingSession(request.Context(), current.ID, input, keepHash)
			if err != nil {
				entry.Level = platform.LogLevelWarn
				entry.Status = platform.LogStatusFailed
				entry.Detail["error"] = err.Error()
				s.recordLog(request, entry)
				s.handleError(w, err)
				return
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"user": user})
			return
		}
	}
	user, err := s.store.UpdateUser(request.Context(), current.ID, input)
	if err != nil {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Detail["error"] = err.Error()
		s.recordLog(request, entry)
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) updateCurrentUserPreferences(w http.ResponseWriter, request *http.Request) {
	var input platform.UserPreferences
	if !s.decodeJSON(w, request, &input) {
		return
	}
	current := userFromContext(request.Context())
	user, err := s.store.UpdateUserPreferences(request.Context(), current.ID, input)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryAuth, Action: "update-preferences",
			Message: "更新偏好设置失败", Status: platform.LogStatusFailed,
			ResourceKind: "user", ResourceID: current.ID, ResourceName: current.Name,
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryAuth, Action: "update-preferences",
		Message:      "更新偏好设置",
		ResourceKind: "user", ResourceID: current.ID, ResourceName: current.Name,
	})
	s.writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) logout(w http.ResponseWriter, request *http.Request) {
	if tokenHash, ok := sessionHash(request); ok {
		if err := s.store.DeleteSession(request.Context(), tokenHash); err != nil {
			s.handleError(w, err)
			return
		}
	}
	s.clearSessionCookie(w, request)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, request *http.Request) {
	users, err := s.store.ListUsers(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) createUser(w http.ResponseWriter, request *http.Request) {
	var input platform.UserInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	user, err := s.store.CreateUser(request.Context(), input)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryUser, Action: "create",
			Message: "创建用户 " + input.Username + " 失败", Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) updateUser(w http.ResponseWriter, request *http.Request) {
	var input platform.UserInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	current := userFromContext(request.Context())
	if request.PathValue("id") == current.ID && (input.Role != platform.UserRoleAdmin || input.Status != platform.UserStatusActive) {
		s.writeError(w, http.StatusBadRequest, "不能停用当前账号或移除自己的管理员角色")
		return
	}
	user, err := s.store.UpdateUser(request.Context(), request.PathValue("id"), input)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryUser, Action: "update",
			Message: "更新用户 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "user", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) deleteUser(w http.ResponseWriter, request *http.Request) {
	current := userFromContext(request.Context())
	if request.PathValue("id") == current.ID {
		s.writeError(w, http.StatusBadRequest, "不能删除当前登录账号")
		return
	}
	if err := s.store.DeleteUser(request.Context(), request.PathValue("id")); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryUser, Action: "delete",
			Message: "删除用户 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "user", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if s.disableAuth {
			user, err := s.debugUser(request.Context())
			if err != nil {
				s.handleError(w, err)
				return
			}
			next.ServeHTTP(w, request.WithContext(withUserAuditContext(request.Context(), user)))
			return
		}
		tokenHash, ok := sessionHash(request)
		if !ok {
			s.writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		user, err := s.store.UserBySession(request.Context(), tokenHash)
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				s.clearSessionCookie(w, request)
				s.writeError(w, http.StatusUnauthorized, "登录状态无效或已过期")
				return
			}
			s.handleError(w, err)
			return
		}
		next.ServeHTTP(w, request.WithContext(withUserAuditContext(request.Context(), user)))
	})
}

func withUserAuditContext(ctx context.Context, user platform.User) context.Context {
	ctx = context.WithValue(ctx, userContextKey{}, user)
	return platform.WithAuditActor(ctx, platform.AuditActor{Type: "user", ID: user.ID, Name: user.Name})
}

func (s *Server) debugUser(ctx context.Context) (platform.User, error) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return platform.User{}, err
	}
	for _, user := range users {
		if user.Role == platform.UserRoleAdmin && user.Status == platform.UserStatusActive {
			return user, nil
		}
	}
	now := time.Now().UTC()
	return platform.User{
		ID:          "00000000-0000-0000-0000-000000000001",
		Name:        "Debug Admin",
		Username:    "debug",
		Email:       "debug@agentbox.local",
		Role:        platform.UserRoleAdmin,
		Status:      platform.UserStatusActive,
		Preferences: platform.DefaultUserPreferences(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// sessionPreservingUserStore 由 *store.Store 实现；PlatformStore 接口保持不变，
// 以免破坏依赖该接口的既有测试 fake。
type sessionPreservingUserStore interface {
	UpdateUserPreservingSession(context.Context, string, platform.UserInput, []byte) (platform.User, error)
}

func userFromContext(ctx context.Context) platform.User {
	user, _ := ctx.Value(userContextKey{}).(platform.User)
	return user
}

func newSessionToken() (string, []byte, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func sessionHash(request *http.Request) ([]byte, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	return hash[:], true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, request *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure(request),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, request *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookieSecure(request),
		SameSite: http.SameSiteLaxMode,
	})
}
