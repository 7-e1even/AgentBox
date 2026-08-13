package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"agentbox/internal/platform"
	"agentbox/internal/store"
)

func (s *Server) claimWorkerJob(w http.ResponseWriter, request *http.Request) {
	credential := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	job, err := s.store.ClaimWorkerJob(
		request.Context(), request.PathValue("id"), credential,
	)
	if errors.Is(err, store.ErrNoJob) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) completeWorkerJob(w http.ResponseWriter, request *http.Request) {
	var result platform.WorkerJobResult
	if !s.decodeJSON(w, request, &result) {
		return
	}
	if len(result.Message) > 4000 || len(result.ExternalID) > 255 {
		s.writeError(w, http.StatusBadRequest, "Worker 结果过长")
		return
	}
	credential := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if err := s.store.CompleteWorkerJob(
		request.Context(), request.PathValue("id"), credential,
		request.PathValue("jobId"), result,
	); err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
