package platform

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type Kind string

const (
	KindProject  Kind = "project"
	KindRuntime  Kind = "runtime"
	KindSkill    Kind = "skill"
	KindMCP      Kind = "mcp"
	KindSandbox  Kind = "sandbox"
	KindSchedule Kind = "schedule"
	KindWebhook  Kind = "webhook"
	KindVariable Kind = "variable"
)

var idPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Input struct {
	ID          string         `json:"id"`
	Kind        Kind           `json:"kind"`
	ProjectID   *string        `json:"projectId"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Enabled     bool           `json:"enabled"`
	Spec        map[string]any `json:"spec"`
}

type Resource struct {
	Input
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Snapshot struct {
	Resources []Resource `json:"resources"`
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func Normalize(input *Input) {
	input.ID = strings.ToLower(strings.TrimSpace(input.ID))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.ProjectID != nil {
		value := strings.TrimSpace(*input.ProjectID)
		if value == "" {
			input.ProjectID = nil
		} else {
			input.ProjectID = &value
		}
	}
	if input.Spec == nil {
		input.Spec = map[string]any{}
	}
}

func Validate(input Input) error {
	if !isKind(input.Kind) {
		return &ValidationError{Message: "资源类型无效"}
	}
	if n := utf8.RuneCountInString(input.ID); n < 2 || n > 64 || !idPattern.MatchString(input.ID) {
		return &ValidationError{Message: "标识只能包含小写字母、数字和连字符，长度为 2 到 64"}
	}
	if n := utf8.RuneCountInString(input.Name); n < 2 || n > 80 {
		return &ValidationError{Message: "名称需要 2 到 80 个字符"}
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return &ValidationError{Message: "简介不能超过 500 个字符"}
	}
	if input.Kind != KindProject && input.ProjectID == nil {
		return &ValidationError{Message: "请选择所属项目"}
	}
	if input.Kind == KindSchedule {
		if err := require(input.Spec, "agentId", "请选择目标 Agent"); err != nil {
			return err
		}
		cron, _ := input.Spec["cron"].(string)
		if len(strings.Fields(cron)) != 5 {
			return &ValidationError{Message: "Cron 表达式需要 5 段"}
		}
	}
	if input.Kind == KindMCP {
		transport, _ := input.Spec["transport"].(string)
		if transport != "stdio" && transport != "http" {
			return &ValidationError{Message: "MCP transport 只能是 stdio 或 http"}
		}
		if transport == "stdio" {
			return require(input.Spec, "command", "stdio MCP 需要启动命令")
		}
		return require(input.Spec, "url", "HTTP MCP 需要 URL")
	}
	if input.Kind == KindRuntime {
		driver, _ := input.Spec["driver"].(string)
		switch driver {
		case "process", "docker", "boxlite", "microsandbox":
		default:
			return &ValidationError{Message: "Runtime 驱动无效"}
		}
	}
	if input.Kind == KindSandbox {
		if err := require(input.Spec, "agentId", "请选择 Agent"); err != nil {
			return err
		}
		return require(input.Spec, "runtimeId", "请选择 Runtime")
	}
	if input.Kind == KindWebhook {
		if err := require(input.Spec, "agentId", "请选择目标 Agent"); err != nil {
			return err
		}
		if err := require(input.Spec, "path", "请填写 Webhook 路径"); err != nil {
			return err
		}
		return require(input.Spec, "secretRef", "请配置签名密钥引用")
	}
	if input.Kind == KindVariable {
		if err := require(input.Spec, "key", "请填写环境变量名"); err != nil {
			return err
		}
		return require(input.Spec, "reference", "请填写变量或密钥引用")
	}
	return nil
}

func require(spec map[string]any, key, message string) error {
	value, _ := spec[key].(string)
	if strings.TrimSpace(value) == "" {
		return &ValidationError{Message: message}
	}
	return nil
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

func isKind(kind Kind) bool {
	switch kind {
	case KindProject, KindRuntime, KindSkill, KindMCP, KindSandbox, KindSchedule, KindWebhook, KindVariable:
		return true
	default:
		return false
	}
}
