package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/platform"
)

// listLogs 处理 GET /api/logs，仅管理员可用。
func (s *Server) listLogs(w http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	filter := platform.LogFilter{
		Category: strings.TrimSpace(query.Get("category")),
		Level:    strings.TrimSpace(query.Get("level")),
		Status:   strings.TrimSpace(query.Get("status")),
		Query:    strings.TrimSpace(query.Get("q")),
		Page:     1,
		PageSize: 50,
	}
	var ok bool
	if filter.From, ok = parseLogTime(query.Get("from"), false); !ok {
		s.writeError(w, http.StatusBadRequest, "from 时间格式无效（支持 RFC3339 或 YYYY-MM-DD）")
		return
	}
	if filter.To, ok = parseLogTime(query.Get("to"), true); !ok {
		s.writeError(w, http.StatusBadRequest, "to 时间格式无效（支持 RFC3339 或 YYYY-MM-DD）")
		return
	}
	if value := strings.TrimSpace(query.Get("page")); value != "" {
		page, err := strconv.Atoi(value)
		if err != nil || page < 1 {
			s.writeError(w, http.StatusBadRequest, "page 参数无效")
			return
		}
		filter.Page = page
	}
	if value := strings.TrimSpace(query.Get("pageSize")); value != "" {
		pageSize, err := strconv.Atoi(value)
		if err != nil || pageSize < 1 {
			s.writeError(w, http.StatusBadRequest, "pageSize 参数无效")
			return
		}
		filter.PageSize = pageSize
	}
	entries, total, err := s.store.ListLogs(request.Context(), filter)
	if err != nil {
		s.handleError(w, err)
		return
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "total": total,
		"page": filter.Page, "pageSize": filter.PageSize,
	})
}

// parseLogTime 解析 RFC3339 或 YYYY-MM-DD 日期；endOfDay 时日期按当天结束（UTC）处理。
// 空值返回零值与 true。
func parseLogTime(value string, endOfDay bool) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, true
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		if endOfDay {
			parsed = parsed.Add(24*time.Hour - time.Nanosecond)
		}
		return parsed, true
	}
	return time.Time{}, false
}
