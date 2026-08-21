package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/platform"
	"agentbox/internal/store"
)

type PlatformStore interface {
	ListResources(context.Context) ([]platform.Resource, error)
	CreateResource(context.Context, platform.Input) (platform.Resource, error)
	UpdateResource(context.Context, string, platform.Input) (platform.Resource, error)
	DeleteResource(context.Context, string) error
	OperateSandbox(context.Context, string, string) (platform.Resource, error)
	ListAutomations(context.Context, string) ([]platform.Automation, error)
	GetAutomation(context.Context, string) (platform.Automation, error)
	CreateAutomation(context.Context, platform.AutomationInput, string) (platform.Automation, string, error)
	UpdateAutomation(context.Context, string, platform.AutomationInput, string) (platform.Automation, error)
	DeleteAutomation(context.Context, string) error
	RotateAutomationSecret(context.Context, string, string) (platform.Automation, string, error)
	TriggerAutomation(context.Context, platform.AutomationDelivery) (platform.AutomationTriggerResult, error)
	TestAutomation(context.Context, string, []byte) (platform.AutomationTriggerResult, error)
	ListAutomationRuns(context.Context, string, string, int) ([]platform.AutomationRun, error)
	GetAutomationRun(context.Context, string) (platform.AutomationRun, error)
	GetPublicAutomationRun(context.Context, string, string, string) (platform.AutomationRun, error)
	ListCredentials(context.Context) ([]platform.ManagedCredential, error)
	CreateCredential(context.Context, platform.CredentialInput) (platform.ManagedCredential, error)
	UpdateCredential(context.Context, string, platform.CredentialInput) (platform.ManagedCredential, error)
	CheckCredential(context.Context, string) (platform.ManagedCredential, error)
	PullCredentialModels(context.Context, string) ([]platform.CredentialModel, error)
	AddCredentialModel(context.Context, string, platform.CredentialModelInput) ([]platform.CredentialModel, error)
	DeleteCredentialModel(context.Context, string, string) ([]platform.CredentialModel, error)
	DeleteCredential(context.Context, string) error
	ListNetworkProxies(context.Context) ([]platform.ManagedNetworkProxy, error)
	CreateNetworkProxy(context.Context, platform.NetworkProxyInput) (platform.ManagedNetworkProxy, error)
	UpdateNetworkProxy(context.Context, string, platform.NetworkProxyInput) (platform.ManagedNetworkProxy, error)
	DeleteNetworkProxy(context.Context, string) error
	ResolveRuntimeLLMTarget(context.Context, string, string, string) (platform.RuntimeLLMTarget, error)
	ClaimWorkerJob(context.Context, string, string) (platform.WorkerJob, error)
	CompleteWorkerJob(context.Context, string, string, string, platform.WorkerJobResult) error
	ListServers(context.Context) ([]platform.ManagedServer, error)
	CreateServerPairing(context.Context) (platform.ServerPairing, error)
	GetServerPairing(context.Context, string) (platform.ServerPairing, error)
	RegisterServer(context.Context, platform.ServerRegistration) (platform.ManagedServer, string, error)
	HeartbeatServer(context.Context, string, string, []string, *platform.ServerInventory, string) error
	EnqueueWorkerUpdate(context.Context, string, string) error
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
	InsertLogs(context.Context, []platform.LogEntry) error
	ListLogs(context.Context, platform.LogFilter) ([]platform.LogEntry, int, error)
	Ping(context.Context) error
}

type Server struct {
	store            PlatformStore
	catalog          catalog.Catalog
	logger           *slog.Logger
	handler          http.Handler
	allowedOrigins   map[string]struct{}
	disableAuth      bool
	trustedProxy     bool
	loginLimiter     *loginRateLimiter
	sessions         *sessionHub
	logRecorder      *logRecorder
	serverStatesMu   sync.Mutex
	serverStates     map[string]string
	runtimeLLMClient *http.Client
	workerBinaryDir  string
	workerVersion    string
	workerReleaseURL string
	workerCacheDir   string
	workerHTTPClient *http.Client
	workerBinaryMu   sync.Mutex
}

type Config struct {
	DisableAuth      bool
	WorkerBinaryDir  string
	WorkerVersion    string
	WorkerReleaseURL string
	WorkerCacheDir   string
}

func New(repository PlatformStore, catalog catalog.Catalog, logger *slog.Logger, origins []string, config Config) *Server {
	trustedProxy := trustedProxyFromEnv()
	server := &Server{
		store:            repository,
		catalog:          catalog,
		logger:           logger,
		allowedOrigins:   make(map[string]struct{}, len(origins)),
		disableAuth:      config.DisableAuth,
		trustedProxy:     trustedProxy,
		loginLimiter:     newLoginRateLimiter(),
		sessions:         newSessionHub(origins, trustedProxy),
		logRecorder:      newLogRecorder(repository, logger),
		serverStates:     make(map[string]string),
		runtimeLLMClient: newRuntimeLLMHTTPClient(),
		workerBinaryDir:  config.WorkerBinaryDir,
		workerVersion:    config.WorkerVersion,
		workerReleaseURL: config.WorkerReleaseURL,
		workerCacheDir:   config.WorkerCacheDir,
		workerHTTPClient: &http.Client{Timeout: 2 * time.Minute},
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
	// operator 及以上（含 admin）：沙箱/模板/自动化/镜像/项目的读写与操作。
	operator := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, server.requireUser(server.requireRole(platform.UserRoleOperator, handler)))
	}
	// 仅 admin：用户、模型凭据、网络代理、服务器（增删/配对/操作）。
	admin := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, server.requireUser(server.requireRole(platform.UserRoleAdmin, handler)))
	}
	authenticated("GET /api/auth/me", server.currentUser)
	authenticated("PATCH /api/auth/me", server.updateCurrentUser)
	authenticated("PATCH /api/auth/preferences", server.updateCurrentUserPreferences)
	authenticated("POST /api/auth/logout", server.logout)
	admin("GET /api/users", server.listUsers)
	admin("POST /api/users", server.createUser)
	admin("PATCH /api/users/{id}", server.updateUser)
	admin("DELETE /api/users/{id}", server.deleteUser)
	admin("GET /api/logs", server.listLogs)
	authenticated("GET /api/catalog", server.getCatalog)
	authenticated("GET /api/resources", server.listResources)
	operator("POST /api/resources", server.createResource)
	operator("PATCH /api/resources/{id}", server.updateResource)
	operator("DELETE /api/resources/{id}", server.deleteResource)
	operator("POST /api/sandboxes/{id}/actions/{action}", server.operateSandbox)
	operator("POST /api/sandboxes/{id}/session-ticket", server.createSandboxSessionTicket)
	authenticated("GET /api/automations", server.listAutomations)
	operator("POST /api/automations", server.createAutomation)
	authenticated("GET /api/automations/{id}", server.getAutomation)
	operator("PATCH /api/automations/{id}", server.updateAutomation)
	operator("DELETE /api/automations/{id}", server.deleteAutomation)
	operator("POST /api/automations/{id}/rotate-secret", server.rotateAutomationSecret)
	operator("POST /api/automations/{id}/test", server.testAutomation)
	authenticated("GET /api/automation-runs", server.listAutomationRuns)
	authenticated("GET /api/automation-runs/{id}", server.getAutomationRun)
	authenticated("GET /api/credentials", server.listCredentials)
	admin("POST /api/credentials", server.createCredential)
	admin("PATCH /api/credentials/{id}", server.updateCredential)
	admin("POST /api/credentials/{id}/check", server.checkCredential)
	admin("POST /api/credentials/{id}/models/pull", server.pullCredentialModels)
	admin("POST /api/credentials/{id}/models", server.addCredentialModel)
	admin("DELETE /api/credentials/{id}/models", server.deleteCredentialModel)
	admin("DELETE /api/credentials/{id}/models/{modelId}", server.deleteCredentialModel)
	admin("DELETE /api/credentials/{id}", server.deleteCredential)
	authenticated("GET /api/network-proxies", server.listNetworkProxies)
	admin("POST /api/network-proxies", server.createNetworkProxy)
	admin("PATCH /api/network-proxies/{id}", server.updateNetworkProxy)
	admin("DELETE /api/network-proxies/{id}", server.deleteNetworkProxy)
	authenticated("GET /api/servers", server.listServers)
	admin("DELETE /api/servers/{id}", server.deleteServer)
	admin("POST /api/servers/{id}/actions/update-worker", server.updateWorker)
	admin("POST /api/server-pairings", server.createServerPairing)
	admin("GET /api/server-pairings/{id}", server.getServerPairing)
	mux.HandleFunc("POST /api/servers/register", server.registerServer)
	mux.HandleFunc("POST /api/servers/{id}/heartbeat", server.heartbeatServer)
	mux.HandleFunc("POST /api/servers/{id}/jobs/claim", server.claimWorkerJob)
	mux.HandleFunc("POST /api/servers/{id}/jobs/{jobId}/complete", server.completeWorkerJob)
	mux.HandleFunc("POST /api/webhooks/{endpointId}", server.receiveAutomationWebhook)
	mux.HandleFunc("GET /api/webhooks/{endpointId}/runs/{runId}", server.getPublicAutomationRun)
	mux.HandleFunc("GET /api/servers/{id}/sessions/connect", server.connectWorkerSessions)
	mux.HandleFunc("GET /api/sandboxes/{id}/session", server.connectSandboxSession)
	mux.HandleFunc("GET /api/worker/install.sh", server.workerInstallScript)
	mux.HandleFunc("GET /api/worker/agentbox-worker", server.workerBinary)
	mux.HandleFunc("GET /api/worker/agentbox-microsandbox-driver.go", server.workerMicrosandboxDriverSourceScript)
	mux.HandleFunc("POST /api/runtime/sandboxes/{id}/llm/{credentialId}/anthropic/v1/messages", server.runtimeLLMAnthropic)
	mux.HandleFunc("POST /api/runtime/sandboxes/{id}/llm/{credentialId}/anthropic/v1/messages/count_tokens", server.runtimeLLMAnthropicCountTokens)
	mux.HandleFunc("POST /api/runtime/sandboxes/{id}/llm/{credentialId}/openai/v1/responses", server.runtimeLLMResponses)
	mux.HandleFunc("POST /api/runtime/sandboxes/{id}/llm/{credentialId}/openai/v1/chat/completions", server.runtimeLLMChat)
	mux.HandleFunc("GET /api/runtime/sandboxes/{id}/llm/{credentialId}/gemini/{path...}", server.runtimeLLMGemini)
	mux.HandleFunc("POST /api/runtime/sandboxes/{id}/llm/{credentialId}/gemini/{path...}", server.runtimeLLMGemini)

	server.handler = server.recoverPanic(server.cors(server.logRequests(mux)))
	return server
}

// ServeHTTP 使 *Server 直接作为 http.Handler 使用，
// 同时保留 Close/RecordSystem 供优雅关停与系统事件打点。
func (s *Server) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	s.handler.ServeHTTP(w, request)
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
	started := time.Now()
	resource, err := s.store.CreateResource(request.Context(), input)
	entry := platform.LogEntry{
		Category: platform.LogCategoryResource, Action: "create",
		ResourceKind: string(input.Kind), ResourceID: input.ID, ResourceName: input.Name,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Message = fmt.Sprintf("创建资源 %s 失败", input.Name)
		entry.Detail = map[string]any{"error": err.Error()}
		s.recordLog(request, entry)
		s.handleError(w, err)
		return
	}
	entry.Message = fmt.Sprintf("创建资源 %s（%s）", resource.Name, resource.Kind)
	s.recordLog(request, entry)
	s.writeJSON(w, http.StatusCreated, map[string]any{"resource": resource})
}

func (s *Server) updateResource(w http.ResponseWriter, request *http.Request) {
	var input platform.Input
	if !s.decodeJSON(w, request, &input) {
		return
	}
	id := request.PathValue("id")
	started := time.Now()
	resource, err := s.store.UpdateResource(request.Context(), id, input)
	entry := platform.LogEntry{
		Category: platform.LogCategoryResource, Action: "update",
		ResourceKind: string(input.Kind), ResourceID: id, ResourceName: input.Name,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Message = fmt.Sprintf("更新资源 %s 失败", id)
		entry.Detail = map[string]any{"error": err.Error()}
		s.recordLog(request, entry)
		s.handleError(w, err)
		return
	}
	entry.ResourceName = resource.Name
	entry.Message = fmt.Sprintf("更新资源 %s（%s）", resource.Name, resource.Kind)
	s.recordLog(request, entry)
	s.writeJSON(w, http.StatusOK, map[string]any{"resource": resource})
}

func (s *Server) deleteResource(w http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if err := s.store.DeleteResource(request.Context(), id); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryResource, Action: "delete",
			Message: fmt.Sprintf("删除资源 %s 失败", id), Status: platform.LogStatusFailed,
			ResourceID: id, Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.recordLog(request, platform.LogEntry{
		Category: platform.LogCategoryResource, Action: "delete",
		Message: fmt.Sprintf("删除资源 %s", id), ResourceID: id,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) decodeJSON(w http.ResponseWriter, request *http.Request, target any) bool {
	return s.decodeJSONWithLimit(w, request, target, 1<<20)
}

func (s *Server) decodeJSONWithLimit(w http.ResponseWriter, request *http.Request, target any, limit int64) bool {
	request.Body = http.MaxBytesReader(w, request.Body, limit)
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
	case platform.IsValidationError(err), platform.IsUserValidationError(err):
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
		if request.URL.Path == "/healthz" {
			next.ServeHTTP(w, request)
			return
		}
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		duration := time.Since(started)
		s.logger.Info("request",
			"method", request.Method, "path", request.URL.Path,
			"status", recorder.status, "remote", request.RemoteAddr,
			"duration", duration.String())
		s.recordAPIRequest(request, recorder.status, duration)
	})
}

// recordAPIRequest 把 HTTP 请求写入系统日志（api 分类）。
// 跳过高频轮询路径，以及已由 sessions.go 打点覆盖的 WebSocket 连接路径。
func (s *Server) recordAPIRequest(request *http.Request, status int, duration time.Duration) {
	path := request.URL.Path
	if path == "/api/logs" || strings.HasSuffix(path, "/heartbeat") ||
		(strings.HasPrefix(path, "/api/servers/") && strings.HasSuffix(path, "/sessions/connect")) ||
		(strings.HasPrefix(path, "/api/sandboxes/") && strings.HasSuffix(path, "/session")) {
		return
	}
	level := platform.LogLevelInfo
	entryStatus := platform.LogStatusSuccess
	switch {
	case status >= http.StatusInternalServerError:
		level = platform.LogLevelError
		entryStatus = platform.LogStatusFailed
	case status >= http.StatusBadRequest:
		level = platform.LogLevelWarn
		entryStatus = platform.LogStatusFailed
	}
	s.recordLog(request, platform.LogEntry{
		Level: level, Category: platform.LogCategoryAPI,
		Action:  request.Method + " " + path,
		Message: fmt.Sprintf("%s %s → %d", request.Method, path, status),
		Status:  entryStatus, DurationMS: duration.Milliseconds(),
		Detail: map[string]any{"method": request.Method, "path": path, "status": status},
	})
}

// statusRecorder 记录响应状态码；透传 Hijacker/Flusher 以兼容 WebSocket 升级与 SSE。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
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
