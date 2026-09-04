package httpapi

import (
	"encoding/base64"
	"net/http"
	"path"
	"strings"

	"agentbox/internal/platform"
	"agentbox/internal/skillimport"
)

func (s *Server) searchSkills(w http.ResponseWriter, request *http.Request) {
	result, err := skillimport.Search(request.Context(), request.URL.Query().Get("q"))
	if err != nil {
		if platform.IsValidationError(err) {
			s.handleError(w, err)
		} else if request.Context().Err() == nil {
			s.writeError(w, http.StatusBadGateway, "暂时无法搜索 skills.sh，请重试，或使用链接导入与本地上传")
		}
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

// The response is a draft. Persistence, conflict handling and audit still use
// the existing resource creation endpoint after the user reviews the content.
func (s *Server) previewSkillImport(w http.ResponseWriter, request *http.Request) {
	var input struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	if !s.decodeJSONWithLimit(w, request, &input, 8<<20) {
		return
	}
	var draft skillimport.Draft
	var err error
	if strings.TrimSpace(input.URL) != "" {
		if input.Content != "" || input.Filename != "" {
			s.writeError(w, http.StatusBadRequest, "请只选择链接导入或本地上传中的一种")
			return
		}
		draft, err = skillimport.Fetch(request.Context(), input.URL)
	} else {
		if len(input.Content) > base64.StdEncoding.EncodedLen(platform.MaxSkillBundleBytes) {
			s.writeError(w, http.StatusRequestEntityTooLarge, "上传文件不能超过 4 MiB")
			return
		}
		data, decodeErr := base64.StdEncoding.Strict().DecodeString(input.Content)
		if decodeErr != nil {
			s.writeError(w, http.StatusBadRequest, "上传文件编码无效")
			return
		}
		filename := path.Base(strings.ReplaceAll(input.Filename, "\\", "/"))
		draft, err = skillimport.Parse(filename, data)
	}
	if err != nil {
		s.handleError(w, err)
		return
	}
	if err := platform.CanonicalizeSkillSpec(draft.Name, &draft.Spec); err != nil {
		s.handleError(w, err)
		return
	}
	draft.Description, err = platform.SkillCatalogDescription(draft.Spec)
	if err != nil {
		s.handleError(w, err)
		return
	}
	if err := platform.ValidateSkillResource(draft.Name, draft.Description, draft.Spec); err != nil {
		s.handleError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"skill": draft})
}
