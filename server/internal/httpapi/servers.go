package httpapi

import (
	"net/http"
	"strings"

	"agentbox/internal/platform"
	"agentbox/internal/workerscript"
)

func (s *Server) listServers(w http.ResponseWriter, request *http.Request) {
	servers, err := s.store.ListServers(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.observeServerStatuses(servers)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"servers":       servers,
		"workerVersion": s.workerVersion,
	})
}

// trackServerStatus 跟踪服务器在线状态，仅在状态变化时返回 true。
// 首次观察到某台服务器只登记状态，避免进程重启后误报一轮上线/离线。
func (s *Server) trackServerStatus(serverID, status string) bool {
	s.serverStatesMu.Lock()
	defer s.serverStatesMu.Unlock()
	if s.serverStates == nil {
		return false
	}
	previous, known := s.serverStates[serverID]
	s.serverStates[serverID] = status
	return known && previous != status
}

// observeServerStatuses 在服务器列表刷新时记录上线/离线事件（系统事件，无操作者）。
func (s *Server) observeServerStatuses(servers []platform.ManagedServer) {
	for _, server := range servers {
		if !s.trackServerStatus(server.ID, server.Status) {
			continue
		}
		entry := platform.LogEntry{
			Level: platform.LogLevelInfo, Category: platform.LogCategoryServer, Action: "online",
			Message:      "服务器上线：" + server.Name,
			ResourceKind: "server", ResourceID: server.ID, ResourceName: server.Name,
		}
		if server.Status != "online" {
			entry.Level = platform.LogLevelWarn
			entry.Action = "offline"
			entry.Message = "服务器离线：" + server.Name
		}
		s.recordLog(nil, entry)
	}
}

func (s *Server) createServerPairing(w http.ResponseWriter, request *http.Request) {
	pairing, err := s.store.CreateServerPairing(request.Context())
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"pairing": pairing})
}

func (s *Server) getServerPairing(w http.ResponseWriter, request *http.Request) {
	pairing, err := s.store.GetServerPairing(request.Context(), request.PathValue("id"))
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"pairing": pairing})
}

func (s *Server) registerServer(w http.ResponseWriter, request *http.Request) {
	var input platform.ServerRegistration
	if !s.decodeJSON(w, request, &input) {
		return
	}
	server, credential, err := s.store.RegisterServer(request.Context(), input)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryServer, Action: "register",
			Message: "注册服务器 " + input.Name + " 失败", Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.trackServerStatus(server.ID, "online")
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"credential": credential,
		"server":     server,
	})
}

func (s *Server) heartbeatServer(w http.ResponseWriter, request *http.Request) {
	credential := authBearer(request)
	var capabilities []string
	var inventory *platform.ServerInventory
	workerVersion := ""
	if request.ContentLength != 0 {
		var body struct {
			Capabilities  []string                  `json:"capabilities"`
			Inventory     *platform.ServerInventory `json:"inventory"`
			WorkerVersion string                    `json:"workerVersion"`
		}
		if !s.decodeJSON(w, request, &body) {
			return
		}
		if len(body.Capabilities) > 32 {
			s.writeError(w, http.StatusBadRequest, "服务器能力数量过多")
			return
		}
		capabilities = body.Capabilities
		inventory = body.Inventory
		workerVersion = strings.TrimSpace(body.WorkerVersion)
		if len(workerVersion) > 64 {
			s.writeError(w, http.StatusBadRequest, "Worker 版本过长")
			return
		}
		if inventory != nil {
			platform.NormalizeServerInventory(inventory)
			if err := platform.ValidateServerInventory(*inventory); err != nil {
				s.handleError(w, err)
				return
			}
		}
	}
	if err := s.store.HeartbeatServer(request.Context(), request.PathValue("id"), credential, capabilities, inventory, workerVersion); err != nil {
		s.handleError(w, err)
		return
	}
	// 心跳本身不记日志，仅在离线后恢复上线时记录一次。
	if s.trackServerStatus(request.PathValue("id"), "online") {
		s.recordLog(nil, platform.LogEntry{
			Category: platform.LogCategoryServer, Action: "online",
			Message:      "服务器恢复上线",
			ResourceKind: "server", ResourceID: request.PathValue("id"),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateWorker(w http.ResponseWriter, request *http.Request) {
	var body struct {
		Version string `json:"version"`
	}
	if request.ContentLength != 0 && !s.decodeJSON(w, request, &body) {
		return
	}
	targetVersion := strings.TrimSpace(body.Version)
	if targetVersion == "" {
		targetVersion = s.workerVersion
	}
	if !workerVersionPattern.MatchString(targetVersion) || !strings.HasPrefix(targetVersion, "v") {
		s.writeError(w, http.StatusServiceUnavailable, "Server 未配置可发布的 Worker 版本")
		return
	}
	if targetVersion != s.workerVersion {
		s.writeError(w, http.StatusBadRequest, "只能更新到当前 Server 配套的 Worker 版本")
		return
	}
	if err := s.store.EnqueueWorkerUpdate(request.Context(), request.PathValue("id"), targetVersion); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryServer, Action: "update-worker",
			Message: "下发 Worker 更新失败", Status: platform.LogStatusFailed,
			ResourceKind: "server", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error(), "version": targetVersion},
		})
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]string{"version": targetVersion})
}

func (s *Server) deleteServer(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteServer(request.Context(), request.PathValue("id")); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryServer, Action: "delete",
			Message: "删除服务器 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "server", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workerInstallScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(workerscript.Install()))
}
