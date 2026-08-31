package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"agentbox/internal/workerprotocol"
)

func (s *Server) negotiateWorkerProtocol(w http.ResponseWriter, request *http.Request) bool {
	selected, err := workerprotocol.Negotiate(
		request.Header.Get(workerprotocol.HeaderMinimum),
		request.Header.Get(workerprotocol.HeaderMaximum),
	)
	if err != nil {
		status := http.StatusBadRequest
		code := "worker_protocol_invalid"
		if errors.Is(err, workerprotocol.ErrIncompatible) {
			status = http.StatusUpgradeRequired
			code = "worker_protocol_incompatible"
		}
		s.writeAPIError(w, status, code, err.Error(), false)
		return false
	}
	w.Header().Set(workerprotocol.HeaderSelected, strconv.Itoa(selected))
	return true
}
