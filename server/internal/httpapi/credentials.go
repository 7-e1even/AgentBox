package httpapi

import (
	"net/http"

	"agentbox/internal/platform"
)

func (s *Server) listCredentials(w http.ResponseWriter, request *http.Request) {
	credentials, err := s.store.ListCredentials(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"credentials": credentials})
}

func (s *Server) createCredential(w http.ResponseWriter, request *http.Request) {
	var input platform.CredentialInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	credential, err := s.store.CreateCredential(request.Context(), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"credential": credential})
}

func (s *Server) updateCredential(w http.ResponseWriter, request *http.Request) {
	var input platform.CredentialInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	credential, err := s.store.UpdateCredential(
		request.Context(), request.PathValue("id"), input,
	)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"credential": credential})
}

func (s *Server) deleteCredential(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteCredential(request.Context(), request.PathValue("id")); err != nil {
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) checkCredential(w http.ResponseWriter, request *http.Request) {
	credential, err := s.store.CheckCredential(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"credential": credential})
}

func (s *Server) pullCredentialModels(w http.ResponseWriter, request *http.Request) {
	models, err := s.store.PullCredentialModels(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) addCredentialModel(w http.ResponseWriter, request *http.Request) {
	var input platform.CredentialModelInput
	if !s.decodeJSON(w, request, &input) {
		return
	}
	models, err := s.store.AddCredentialModel(request.Context(), request.PathValue("id"), input)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"models": models})
}

func (s *Server) deleteCredentialModel(w http.ResponseWriter, request *http.Request) {
	models, err := s.store.DeleteCredentialModel(
		request.Context(), request.PathValue("id"), request.URL.Query().Get("modelId"),
	)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"models": models})
}
