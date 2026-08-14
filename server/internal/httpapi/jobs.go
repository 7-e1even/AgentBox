package httpapi

import (
	"errors"
	"net/http"
	"net/url"
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
	attachWorkerRuntimeEndpoints(job.Payload, workerRequestBaseURL(request))
	s.writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func workerRequestBaseURL(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	if forwarded := firstForwardedValue(request.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	host := request.Host
	if forwarded := firstForwardedValue(request.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	parsed, err := url.Parse(scheme + "://" + host)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return strings.TrimRight(parsed.String(), "/")
}

func firstForwardedValue(value string) string {
	first, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(first)
}

func attachWorkerRuntimeEndpoints(payload map[string]any, baseURL string) {
	if baseURL == "" {
		return
	}
	credentials, ok := payload["credentials"].([]map[string]any)
	if !ok {
		return
	}
	for _, credential := range credentials {
		facadePath, _ := credential["facadePath"].(string)
		protocol, _ := credential["protocol"].(string)
		if facadePath == "" {
			continue
		}
		root := baseURL + "/" + strings.TrimLeft(facadePath, "/")
		credential["anthropicEndpoint"] = root + "/anthropic"
		credential["openaiEndpoint"] = root + "/openai/v1"
		credential["chatEndpoint"] = root + "/openai/v1"
		switch protocol {
		case "anthropic":
			credential["endpoint"] = credential["anthropicEndpoint"]
		case "gemini":
			credential["endpoint"] = root + "/gemini"
		default:
			credential["endpoint"] = credential["openaiEndpoint"]
		}
	}
}

func (s *Server) completeWorkerJob(w http.ResponseWriter, request *http.Request) {
	var result platform.WorkerJobResult
	if !s.decodeJSON(w, request, &result) {
		return
	}
	if len(result.Message) > 768<<10 || len(result.ExternalID) > 255 {
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
