package httpapi

import "net/http"

func (s *Server) operateSandbox(w http.ResponseWriter, request *http.Request) {
	resource, err := s.store.OperateSandbox(
		request.Context(), request.PathValue("id"), request.PathValue("action"),
	)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]any{"resource": resource})
}
