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
		s.handleError(w, err)
		return
	}
	setSessionCookie(w, request, token, expiresAt)
	s.writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) login(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !s.decodeJSON(w, request, &input) {
		return
	}
	if strings.TrimSpace(input.Email) == "" || input.Password == "" {
		s.writeError(w, http.StatusBadRequest, "请输入邮箱和密码")
		return
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		s.handleError(w, err)
		return
	}
	expiresAt := time.Now().UTC().Add(sessionLifetime)
	user, err := s.store.AuthenticateUser(request.Context(), input.Email, input.Password, tokenHash, expiresAt)
	if err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			s.writeError(w, http.StatusUnauthorized, "邮箱或密码错误，或账号已停用")
			return
		}
		s.handleError(w, err)
		return
	}
	setSessionCookie(w, request, token, expiresAt)
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
	user, err := s.store.UpdateUser(request.Context(), current.ID, input)
	if err != nil {
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
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) logout(w http.ResponseWriter, request *http.Request) {
	if tokenHash, ok := sessionHash(request); ok {
		if err := s.store.DeleteSession(request.Context(), tokenHash); err != nil {
			s.handleError(w, err)
			return
		}
	}
	clearSessionCookie(w, request)
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
			next.ServeHTTP(w, request.WithContext(context.WithValue(request.Context(), userContextKey{}, user)))
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
				clearSessionCookie(w, request)
				s.writeError(w, http.StatusUnauthorized, "登录状态无效或已过期")
				return
			}
			s.handleError(w, err)
			return
		}
		next.ServeHTTP(w, request.WithContext(context.WithValue(request.Context(), userContextKey{}, user)))
	})
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
		Email:       "debug@agentbox.local",
		Role:        platform.UserRoleAdmin,
		Status:      platform.UserStatusActive,
		Preferences: platform.DefaultUserPreferences(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if userFromContext(request.Context()).Role != platform.UserRoleAdmin {
			s.writeError(w, http.StatusForbidden, "仅管理员可以管理用户")
			return
		}
		next.ServeHTTP(w, request)
	})
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

func setSessionCookie(w http.ResponseWriter, request *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   request.TLS != nil || request.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, request *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   request.TLS != nil || request.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
}
