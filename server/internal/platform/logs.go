package platform

import "time"

// 系统日志级别。
const (
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

// 系统日志分类。
const (
	LogCategoryAuth       = "auth"
	LogCategorySandbox    = "sandbox"
	LogCategorySession    = "session"
	LogCategoryAutomation = "automation"
	LogCategoryServer     = "server"
	LogCategoryJob        = "job"
	LogCategoryLLM        = "llm"
	LogCategoryResource   = "resource"
	LogCategoryCredential = "credential"
	LogCategoryProxy      = "proxy"
	LogCategoryUser       = "user"
	LogCategoryAPI        = "api"
	LogCategorySystem     = "system"
)

// 系统日志结果状态。
const (
	LogStatusSuccess = "success"
	LogStatusFailed  = "failed"
)

// LogEntry 是一条系统日志。Detail 只存放行为元信息，
// 绝不写入密码、令牌或密钥值。
type LogEntry struct {
	ID           int64          `json:"id"`
	CreatedAt    time.Time      `json:"createdAt"`
	Level        string         `json:"level"`
	Category     string         `json:"category"`
	Action       string         `json:"action"`
	Message      string         `json:"message"`
	ActorID      string         `json:"actorId"`
	ActorName    string         `json:"actorName"`
	ResourceKind string         `json:"resourceKind"`
	ResourceID   string         `json:"resourceId"`
	ResourceName string         `json:"resourceName"`
	Status       string         `json:"status"`
	DurationMS   int64          `json:"durationMs"`
	RemoteAddr   string         `json:"remoteAddr"`
	Detail       map[string]any `json:"detail"`
}

// LogFilter 是系统日志的查询条件；From/To 为零值表示不限制。
type LogFilter struct {
	Category string
	Level    string
	Status   string
	Query    string
	From     time.Time
	To       time.Time
	Page     int
	PageSize int
}
