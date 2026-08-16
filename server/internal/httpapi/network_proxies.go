package httpapi

import (
	"net/http"

	"agentbox/internal/platform"
)

func (s *Server) listNetworkProxies(w http.ResponseWriter, request *http.Request) {
	proxies, err := s.store.ListNetworkProxies(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"proxies": proxies})
}

func (s *Server) createNetworkProxy(w http.ResponseWriter, request *http.Request) {
	var input platform.NetworkProxyInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	proxy, err := s.store.CreateNetworkProxy(request.Context(), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"proxy": proxy})
}

func (s *Server) updateNetworkProxy(w http.ResponseWriter, request *http.Request) {
	var input platform.NetworkProxyInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	proxy, err := s.store.UpdateNetworkProxy(request.Context(), request.PathValue("id"), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"proxy": proxy})
}

func (s *Server) deleteNetworkProxy(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteNetworkProxy(request.Context(), request.PathValue("id")); err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
