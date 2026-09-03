package platform

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const ExtensionDefaultTimeoutSeconds = 600

type ExtensionSpec struct {
	Version         string `json:"version"`
	Source          string `json:"source,omitempty"`
	InstallScript   string `json:"installScript"`
	VerifyScript    string `json:"verifyScript"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	RequiresNetwork bool   `json:"requiresNetwork"`
}

type ExtensionDefinition struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Generation  int64         `json:"generation"`
	Spec        ExtensionSpec `json:"spec"`
}

type ProvisioningExtension struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Message    string     `json:"message,omitempty"`
	Output     string     `json:"output,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	DurationMS int64      `json:"durationMs,omitzero"`
}

func normalizeExtensionSpec(input *Input) {
	if input.Kind == KindExtension {
		if input.Spec["requiresNetwork"] == nil {
			input.Spec["requiresNetwork"] = true
		}
		if input.Spec["timeoutSeconds"] == nil {
			input.Spec["timeoutSeconds"] = ExtensionDefaultTimeoutSeconds
		}
		if input.Spec["source"] == nil || input.Spec["source"] == "" {
			input.Spec["source"] = "custom"
		}
		if version, ok := input.Spec["version"].(string); ok {
			input.Spec["version"] = strings.TrimSpace(version)
		}
	}
	if input.Kind != KindRuntime && input.Kind != KindSandbox {
		return
	}
	var ids []string
	switch values := input.Spec["extensionIds"].(type) {
	case []string:
		ids = values
	case []any:
		ids = make([]string, 0, len(values))
		for _, value := range values {
			id, ok := value.(string)
			if !ok {
				return // Preserve malformed input for the strict decoder.
			}
			ids = append(ids, id)
		}
	default:
		return
	}
	if len(ids) > 64 {
		return // Validation rejects an oversized list before deduplication work.
	}
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(unique, id) {
			unique = append(unique, id)
		}
	}
	input.Spec["extensionIds"] = unique
}

func ValidateExtensionSpec(spec ExtensionSpec, enabled bool) error {
	if spec.Source != "" && spec.Source != "custom" && spec.Source != "preset" {
		return &ValidationError{Message: "扩展来源无效"}
	}
	if utf8.RuneCountInString(spec.Version) > 64 || strings.ContainsAny(spec.Version, "\r\n\x00") {
		return &ValidationError{Message: "扩展版本不能超过 64 个字符或包含换行"}
	}
	if len(spec.InstallScript) > 64<<10 || len(spec.VerifyScript) > 64<<10 ||
		strings.ContainsRune(spec.InstallScript, 0) || strings.ContainsRune(spec.VerifyScript, 0) {
		return &ValidationError{Message: "扩展脚本不能超过 64 KiB 或包含空字节"}
	}
	if spec.TimeoutSeconds < 30 || spec.TimeoutSeconds > 1800 {
		return &ValidationError{Message: "扩展超时需要在 30 到 1800 秒之间"}
	}
	if enabled && (strings.TrimSpace(spec.Version) == "" || strings.TrimSpace(spec.InstallScript) == "" || strings.TrimSpace(spec.VerifyScript) == "") {
		return &ValidationError{Message: "启用扩展前请填写版本、安装脚本和验证脚本"}
	}
	return nil
}

func validateExtensionIDs(spec map[string]any) error {
	ids := stringList(spec["extensionIds"])
	if len(ids) > 64 {
		return &ValidationError{Message: "单个环境最多选择 64 个扩展"}
	}
	for _, id := range ids {
		if len(id) < 2 || len(id) > 64 || !idPattern.MatchString(id) {
			return &ValidationError{Message: "扩展引用标识无效"}
		}
	}
	return nil
}

func ValidateExtensionProgress(input WorkerJobProgressInput) error {
	if (input.ExtensionID == "") != (input.ExtensionStatus == "") ||
		len(input.ExtensionID) > 64 || len(input.ExtensionOutput) > 4096 ||
		(input.ExtensionID == "" && input.ExtensionOutput != "") {
		return &ValidationError{Message: "扩展安装进度无效"}
	}
	if input.ExtensionID != "" && !idPattern.MatchString(input.ExtensionID) {
		return &ValidationError{Message: "扩展安装进度标识无效"}
	}
	switch input.ExtensionStatus {
	case "", "installing", "verifying", "succeeded", "failed":
		return nil
	default:
		return &ValidationError{Message: "扩展安装进度状态无效"}
	}
}
