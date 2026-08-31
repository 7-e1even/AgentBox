package httpapi

import (
	"net/http"
	"net/url"

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
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryCredential, Action: "create",
			Message: "创建模型凭据 " + input.Name + " 失败", Status: platform.LogStatusFailed,
			Detail: map[string]any{"error": err.Error()},
		})
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
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryCredential, Action: "update",
			Message: "更新模型凭据 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "credential", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"credential": credential})
}

func (s *Server) deleteCredential(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteCredential(request.Context(), request.PathValue("id")); err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryCredential, Action: "delete",
			Message: "删除模型凭据 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "credential", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) checkCredential(w http.ResponseWriter, request *http.Request) {
	credential, err := s.store.CheckCredential(request.Context(), request.PathValue("id"))
	if err != nil {
		s.recordLog(request, platform.LogEntry{
			Level: platform.LogLevelWarn, Category: platform.LogCategoryCredential, Action: "check",
			Message: "检查模型凭据 " + request.PathValue("id") + " 失败", Status: platform.LogStatusFailed,
			ResourceKind: "credential", ResourceID: request.PathValue("id"),
			Detail: map[string]any{"error": err.Error()},
		})
		s.handleError(w, err)
		return
	}
	entry := platform.LogEntry{
		Category: platform.LogCategoryCredential, Action: "check",
		Message:      "检查模型凭据 " + credential.Name,
		ResourceKind: "credential", ResourceID: credential.ID, ResourceName: credential.Name,
		Detail: map[string]any{"provider": credential.ProviderID},
	}
	if credential.LastCheckOK != nil && !*credential.LastCheckOK {
		entry.Level = platform.LogLevelWarn
		entry.Status = platform.LogStatusFailed
		entry.Message = "检查模型凭据未通过：" + credential.Name
		entry.Detail["error"] = credential.LastCheckError
	}
	s.recordLog(request, entry)
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

// deleteCredentialModel 支持两种路由：
//   - DELETE /api/credentials/{id}/models/{modelId}（推荐，路径参数）
//   - DELETE /api/credentials/{id}/models?modelId=...（deprecated，兼容旧前端，后续移除）
func (s *Server) deleteCredentialModel(w http.ResponseWriter, request *http.Request) {
	modelID := request.PathValue("modelId")
	if decoded, err := url.PathUnescape(modelID); err == nil && decoded != "" {
		modelID = decoded
	}
	if modelID == "" {
		modelID = request.URL.Query().Get("modelId")
	}
	models, err := s.store.DeleteCredentialModel(
		request.Context(), request.PathValue("id"), modelID,
	)
	if err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"models": models})
}
