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
	Ping(context.Context) error
}

type Server struct {
	store          AgentStore
	catalog        agent.Catalog
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
}

func New(repository AgentStore, catalog agent.Catalog, logger *slog.Logger, origins []string) http.Handler {
	server := &Server{
		store:          repository,
		catalog:        catalog,
		logger:         logger,
		allowedOrigins: make(map[string]struct{}, len(origins)),
	}
	for _, origin := range origins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			server.allowedOrigins[trimmed] = struct{}{}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /api/catalog", server.getCatalog)
	mux.HandleFunc("GET /api/agents", server.listAgents)
	mux.HandleFunc("POST /api/agents", server.createAgent)
	mux.HandleFunc("GET /api/agents/{id}", server.getAgent)
	mux.HandleFunc("PATCH /api/agents/{id}", server.updateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", server.deleteAgent)
	mux.HandleFunc("POST /api/agents/{id}/duplicate", server.duplicateAgent)

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
	case agent.IsValidationError(err):
		s.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "Agent 不存在")
	case errors.Is(err, store.ErrConflict):
		message := "Agent 已被更新，或标识已被使用，请刷新后重试"
		if err == store.ErrConflict {
			message = "请先归档 Agent，再执行永久删除"
		}
		s.writeError(w, http.StatusConflict, message)
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
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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
