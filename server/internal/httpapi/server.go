package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/platform"
	"agentbox/internal/store"
	"github.com/google/uuid"
)

type AgentStore interface {
	List(context.Context) ([]agent.Agent, error)
	Get(context.Context, string) (agent.Agent, error)
	Create(context.Context, agent.Input) (agent.Agent, error)
	Update(context.Context, string, agent.Input, int) (agent.Agent, error)
	Duplicate(context.Context, string) (agent.Agent, error)
	Delete(context.Context, string) error
	ListResources(context.Context) ([]platform.Resource, error)
	CreateResource(context.Context, platform.Input) (platform.Resource, error)
	UpdateResource(context.Context, string, platform.Input) (platform.Resource, error)
	DeleteResource(context.Context, string) error
	OperateSandbox(context.Context, string, string) (platform.Resource, error)
	ListCredentials(context.Context) ([]platform.ManagedCredential, error)
	CreateCredential(context.Context, platform.CredentialInput) (platform.ManagedCredential, error)
	UpdateCredential(context.Context, string, platform.CredentialInput) (platform.ManagedCredential, error)
	CheckCredential(context.Context, string) (platform.ManagedCredential, error)
	PullCredentialModels(context.Context, string) ([]platform.CredentialModel, error)
	AddCredentialModel(context.Context, string, platform.CredentialModelInput) ([]platform.CredentialModel, error)
	DeleteCredentialModel(context.Context, string, string) ([]platform.CredentialModel, error)
	DeleteCredential(context.Context, string) error
	ClaimWorkerJob(context.Context, string, string) (platform.WorkerJob, error)
	CompleteWorkerJob(context.Context, string, string, string, platform.WorkerJobResult) error
	ListServers(context.Context) ([]platform.ManagedServer, error)
	CreateServerPairing(context.Context) (platform.ServerPairing, error)
	GetServerPairing(context.Context, string) (platform.ServerPairing, error)
	RegisterServer(context.Context, platform.ServerRegistration) (platform.ManagedServer, string, error)
	HeartbeatServer(context.Context, string, string, []string, *platform.ServerInventory) error
	DeleteServer(context.Context, string) error
	NeedsUserSetup(context.Context) (bool, error)
	SetupAdmin(context.Context, platform.UserInput, []byte, time.Time) (platform.User, error)
	AuthenticateUser(context.Context, string, string, []byte, time.Time) (platform.User, error)
	UserBySession(context.Context, []byte) (platform.User, error)
	DeleteSession(context.Context, []byte) error
	ListUsers(context.Context) ([]platform.User, error)
	CreateUser(context.Context, platform.UserInput) (platform.User, error)
	UpdateUser(context.Context, string, platform.UserInput) (platform.User, error)
	UpdateUserPreferences(context.Context, string, platform.UserPreferences) (platform.User, error)
	DeleteUser(context.Context, string) error
	Ping(context.Context) error
}

type Server struct {
	store          AgentStore
	catalog        agent.Catalog
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	disableAuth    bool
	sessions       *sessionHub
}

type Config struct {
	DisableAuth bool
}

func New(repository AgentStore, catalog agent.Catalog, logger *slog.Logger, origins []string, config Config) http.Handler {
	server := &Server{
		store:          repository,
		catalog:        catalog,
		logger:         logger,
		allowedOrigins: make(map[string]struct{}, len(origins)),
		disableAuth:    config.DisableAuth,
		sessions:       newSessionHub(origins),
	}
	for _, origin := range origins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			server.allowedOrigins[trimmed] = struct{}{}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /api/auth/status", server.authStatus)
	mux.HandleFunc("POST /api/auth/setup", server.setupAdmin)
	mux.HandleFunc("POST /api/auth/login", server.login)
	authenticated := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, server.requireUser(handler))
	}
	admin := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, server.requireUser(server.requireAdmin(handler)))
	}
	authenticated("GET /api/auth/me", server.currentUser)
	authenticated("PATCH /api/auth/me", server.updateCurrentUser)
	authenticated("PATCH /api/auth/preferences", server.updateCurrentUserPreferences)
	authenticated("POST /api/auth/logout", server.logout)
	admin("GET /api/users", server.listUsers)
	admin("POST /api/users", server.createUser)
	admin("PATCH /api/users/{id}", server.updateUser)
	admin("DELETE /api/users/{id}", server.deleteUser)
	authenticated("GET /api/catalog", server.getCatalog)
	authenticated("GET /api/agents", server.listAgents)
	authenticated("POST /api/agents", server.createAgent)
	authenticated("GET /api/agents/{id}", server.getAgent)
	authenticated("PATCH /api/agents/{id}", server.updateAgent)
	authenticated("DELETE /api/agents/{id}", server.deleteAgent)
	authenticated("POST /api/agents/{id}/duplicate", server.duplicateAgent)
	authenticated("GET /api/resources", server.listResources)
	authenticated("POST /api/resources", server.createResource)
	authenticated("PATCH /api/resources/{id}", server.updateResource)
	authenticated("DELETE /api/resources/{id}", server.deleteResource)
	authenticated("POST /api/sandboxes/{id}/actions/{action}", server.operateSandbox)
	authenticated("POST /api/sandboxes/{id}/session-ticket", server.createSandboxSessionTicket)
	authenticated("GET /api/credentials", server.listCredentials)
	authenticated("POST /api/credentials", server.createCredential)
	authenticated("PATCH /api/credentials/{id}", server.updateCredential)
	authenticated("POST /api/credentials/{id}/check", server.checkCredential)
	authenticated("POST /api/credentials/{id}/models/pull", server.pullCredentialModels)
	authenticated("POST /api/credentials/{id}/models", server.addCredentialModel)
	authenticated("DELETE /api/credentials/{id}/models", server.deleteCredentialModel)
	authenticated("DELETE /api/credentials/{id}", server.deleteCredential)
	authenticated("GET /api/servers", server.listServers)
	authenticated("DELETE /api/servers/{id}", server.deleteServer)
	authenticated("POST /api/server-pairings", server.createServerPairing)
	authenticated("GET /api/server-pairings/{id}", server.getServerPairing)
	mux.HandleFunc("POST /api/servers/register", server.registerServer)
	mux.HandleFunc("POST /api/servers/{id}/heartbeat", server.heartbeatServer)
	mux.HandleFunc("POST /api/servers/{id}/jobs/claim", server.claimWorkerJob)
	mux.HandleFunc("POST /api/servers/{id}/jobs/{jobId}/complete", server.completeWorkerJob)
	mux.HandleFunc("GET /api/servers/{id}/sessions/connect", server.connectWorkerSessions)
	mux.HandleFunc("GET /api/sandboxes/{id}/session", server.connectSandboxSession)
	mux.HandleFunc("GET /api/worker/install.sh", server.workerInstallScript)
	mux.HandleFunc("GET /api/worker/agentbox-worker", server.workerScript)
	mux.HandleFunc("GET /api/worker/agentbox-session-worker", server.workerSessionScript)

	return server.recoverPanic(server.cors(server.logRequests(mux)))
}

func (s *Server) health(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "数据库暂时不可用")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) getCatalog(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, s.catalog)
}

func (s *Server) listAgents(w http.ResponseWriter, request *http.Request) {
	agents, err := s.store.List(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

func (s *Server) getAgent(w http.ResponseWriter, request *http.Request) {
	id, ok := s.agentID(w, request)
	if !ok {
		return
	}
	value, err := s.store.Get(request.Context(), id)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"agent": value})
}

func (s *Server) createAgent(w http.ResponseWriter, request *http.Request) {
	var input agent.Input
	if !s.decodeJSON(w, request, &input) {
		return
	}
	value, err := s.store.Create(request.Context(), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"agent": value})
}

func (s *Server) updateAgent(w http.ResponseWriter, request *http.Request) {
	id, ok := s.agentID(w, request)
	if !ok {
		return
	}
	var input agent.UpdateInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	value, err := s.store.Update(request.Context(), id, input.Input, input.Version)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"agent": value})
}

func (s *Server) duplicateAgent(w http.ResponseWriter, request *http.Request) {
	id, ok := s.agentID(w, request)
	if !ok {
		return
	}
	value, err := s.store.Duplicate(request.Context(), id)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"agent": value})
}

func (s *Server) deleteAgent(w http.ResponseWriter, request *http.Request) {
	id, ok := s.agentID(w, request)
	if !ok {
		return
	}
	if err := s.store.Delete(request.Context(), id); err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listResources(w http.ResponseWriter, request *http.Request) {
	resources, err := s.store.ListResources(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, platform.Snapshot{Resources: resources})
}

func (s *Server) createResource(w http.ResponseWriter, request *http.Request) {
	var input platform.Input
	if !s.decodeJSON(w, request, &input) {
		return
	}
	resource, err := s.store.CreateResource(request.Context(), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"resource": resource})
}

func (s *Server) updateResource(w http.ResponseWriter, request *http.Request) {
	var input platform.Input
	if !s.decodeJSON(w, request, &input) {
		return
	}
	resource, err := s.store.UpdateResource(request.Context(), request.PathValue("id"), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"resource": resource})
}

func (s *Server) deleteResource(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteResource(request.Context(), request.PathValue("id")); err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentID(w http.ResponseWriter, request *http.Request) (string, bool) {
	id := request.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.writeError(w, http.StatusNotFound, "Agent 不存在")
		return "", false
	}
	return id, true
}

func (s *Server) decodeJSON(w http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(w, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytesError):
			s.writeError(w, http.StatusRequestEntityTooLarge, "请求内容过大")
		case errors.Is(err, io.EOF):
			s.writeError(w, http.StatusBadRequest, "请求内容不能为空")
		default:
			s.writeError(w, http.StatusBadRequest, "请求内容格式无效")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeError(w, http.StatusBadRequest, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (s *Server) handleError(w http.ResponseWriter, err error) {
	switch {
	case agent.IsValidationError(err), platform.IsValidationError(err), platform.IsUserValidationError(err):
		s.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrResourceNotFound):
		s.writeError(w, http.StatusNotFound, "记录不存在")
	case errors.Is(err, store.ErrPairingInvalid), errors.Is(err, store.ErrWorkerUnauthorized):
		s.writeError(w, http.StatusUnauthorized, "服务器认证无效或已过期")
	case errors.Is(err, store.ErrUnauthorized):
		s.writeError(w, http.StatusUnauthorized, "登录状态无效或已过期")
	case errors.Is(err, store.ErrConflict):
		s.writeError(w, http.StatusConflict, "记录已存在、已被更新，或仍被其他配置引用")
	case errors.Is(err, store.ErrProviderUnavailable):
		s.writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.logger.Error("api request failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, "服务暂时不可用，请稍后重试")
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.logger.Error("encode response", "error", err)
	}
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if _, allowed := s.allowedOrigins[origin]; allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if request.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, request)
		s.logger.Info("request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started).String())
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered", "error", fmt.Sprint(recovered))
				s.writeError(w, http.StatusInternalServerError, "服务暂时不可用，请稍后重试")
			}
		}()
		next.ServeHTTP(w, request)
	})
}
