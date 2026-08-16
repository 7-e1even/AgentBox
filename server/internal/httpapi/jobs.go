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
	attachWorkerProxyEndpoint(job.Payload, workerRequestBaseURL(request))
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

func attachWorkerProxyEndpoint(payload map[string]any, baseURL string) {
	proxy, ok := payload["proxy"].(map[string]any)
	if !ok || baseURL == "" {
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return
	}
	proxyHost, _ := proxy["host"].(string)
	noProxy := uniqueStrings([]string{"localhost", "127.0.0.1", "::1"}, proxy["noProxy"], parsed.Hostname())
	allowNet := uniqueStrings([]string{proxyHost}, proxy["noProxy"], parsed.Hostname())
	proxy["noProxy"] = noProxy
	proxy["allowNet"] = removeLocalProxyHosts(allowNet)
}

func uniqueStrings(initial []string, extra any, final string) []string {
	result := make([]string, 0, len(initial)+4)
	seen := make(map[string]bool)
	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			return
		}
		seen[key] = true
		result = append(result, value)
	}
	for _, value := range initial {
		appendValue(value)
	}
	switch values := extra.(type) {
	case []string:
		for _, value := range values {
			appendValue(value)
		}
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				appendValue(text)
			}
		}
	}
	appendValue(final)
	return result
}

func removeLocalProxyHosts(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "localhost", "127.0.0.1", "::1":
			continue
		}
		result = append(result, value)
	}
	return result
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
