package httpapi

import (
	_ "embed"
	"net/http"
)

//go:embed assets/agentbox-microsandbox-driver/main.go
var workerMicrosandboxDriverSource string

func (s *Server) workerMicrosandboxDriverSourceScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-go; charset=utf-8")
	_, _ = w.Write([]byte(workerMicrosandboxDriverSource))
}
