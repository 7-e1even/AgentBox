package httpapi

import (
	"net/http"
	"strings"

	"agentbox/internal/platform"
)

const networkProxyCheckTarget = "https://www.gstatic.com/generate_204"

type networkProxyCheckInput struct {
	ServerID string `json:"serverId"`
}

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
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryProxy, Action: "create",
			Message: "创建网络代理 " + input.Name + " 失败", Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
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
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryProxy, Action: "update",
			Message: "更新网络代理 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "proxy", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"proxy": proxy})
}

func (s *Server) checkNetworkProxy(w http.ResponseWriter, request *http.Request) {
	proxyID := request.PathValue("id")
	var input networkProxyCheckInput
	if !s.decodeJSONWithLimit(w, request, &input, 4<<10) {
		return
	}
	input.ServerID = strings.TrimSpace(input.ServerID)
	result, err := s.store.CreateNetworkProxyCheck(
		request.Context(), proxyID, input.ServerID, networkProxyCheckTarget,
	)
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryProxy, Action: "check",
			Message: "请求 Worker 检测网络代理失败", Status: platform.LogStatusFailed,
			ResourceKind: "proxy", ResourceID: proxyID,
			Detail: map[string]any{"serverId": input.ServerID, "error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]any{"result": result})
}

func (s *Server) getNetworkProxyCheck(w http.ResponseWriter, request *http.Request) {
	result, err := s.store.GetNetworkProxyCheck(
		request.Context(), request.PathValue("id"), request.PathValue("checkId"),
	)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) deleteNetworkProxy(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteNetworkProxy(request.Context(), request.PathValue("id")); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryProxy, Action: "delete",
			Message: "删除网络代理 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "proxy", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
