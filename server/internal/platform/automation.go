package platform

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type AutomationAuthMode string

const (
	AutomationAuthBearer          AutomationAuthMode = "bearer"
	AutomationAuthHMAC            AutomationAuthMode = "hmac-sha256"
	AutomationAuthGitHub          AutomationAuthMode = "github-sha256"
	AutomationAuthGitLab          AutomationAuthMode = "gitlab-token"
	AutomationAuthStandardWebhook AutomationAuthMode = "standard-webhooks"
)

type AutomationTriggerInput struct {
	Type     string             `json:"type"`
	AuthMode AutomationAuthMode `json:"authMode"`
}

type AutomationCleanupPolicy string

const (
	AutomationCleanupNever     AutomationCleanupPolicy = "never"
	AutomationCleanupOnSuccess AutomationCleanupPolicy = "on-success"
	AutomationCleanupAlways    AutomationCleanupPolicy = "always"
)

type AutomationActionInput struct {
	Type                string                  `json:"type"`
	TemplateID          string                  `json:"templateId"`
	ModelBindings       map[string]string       `json:"modelBindings"`
	InputTemplate       string                  `json:"inputTemplate"`
	TargetTemplate      string                  `json:"targetTemplate"`
	CommandTemplate     string                  `json:"commandTemplate"`
	TimeoutSeconds      int                     `json:"timeoutSeconds"`
	CleanupPolicy       AutomationCleanupPolicy `json:"cleanupPolicy"`
	ExpiresAfterSeconds int                     `json:"expiresAfterSeconds"`
}

type AutomationInput struct {
	ProjectID         string                 `json:"projectId"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	Enabled           bool                   `json:"enabled"`
	ConditionTemplate string                 `json:"conditionTemplate"`
	Secret            string                 `json:"secret,omitempty"`
	Trigger           AutomationTriggerInput `json:"trigger"`
	Action            AutomationActionInput  `json:"action"`
}

type Automation struct {
	ID                string                 `json:"id"`
	ProjectID         string                 `json:"projectId"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	Enabled           bool                   `json:"enabled"`
	ConditionTemplate string                 `json:"conditionTemplate"`
	Trigger           AutomationTriggerInput `json:"trigger"`
	Action            AutomationActionInput  `json:"action"`
	EndpointID        string                 `json:"endpointId"`
	SecretLastFour    string                 `json:"secretLastFour"`
	CreatedBy         *string                `json:"createdBy"`
	UpdatedBy         *string                `json:"updatedBy"`
	LastTriggeredAt   *time.Time             `json:"lastTriggeredAt"`
	SecretRotatedAt   time.Time              `json:"secretRotatedAt"`
	CreatedAt         time.Time              `json:"createdAt"`
	UpdatedAt         time.Time              `json:"updatedAt"`
}

type AutomationRunStatus string

const (
	AutomationRunEvaluating   AutomationRunStatus = "evaluating"
	AutomationRunQueued       AutomationRunStatus = "queued"
	AutomationRunProvisioning AutomationRunStatus = "provisioning"
	AutomationRunRunning      AutomationRunStatus = "running"
	AutomationRunSucceeded    AutomationRunStatus = "succeeded"
	AutomationRunFailed       AutomationRunStatus = "failed"
	AutomationRunSkipped      AutomationRunStatus = "skipped"
	AutomationRunExpired      AutomationRunStatus = "expired"
)

type AutomationEvent struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Source     string    `json:"source"`
	Time       time.Time `json:"time"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type AutomationRun struct {
	ID                     string              `json:"id"`
	AutomationID           *string             `json:"automationId"`
	EndpointID             string              `json:"endpointId,omitempty"`
	ProjectID              string              `json:"projectId"`
	AutomationName         string              `json:"automationName"`
	ActionType             string              `json:"actionType"`
	TemplateID             string              `json:"templateId"`
	TemplateName           string              `json:"templateName"`
	TriggerSource          string              `json:"triggerSource"`
	AuthMode               AutomationAuthMode  `json:"authMode"`
	Event                  AutomationEvent     `json:"event"`
	IdempotencyFingerprint string              `json:"idempotencyFingerprint"`
	PayloadSHA256          string              `json:"payloadSha256"`
	PayloadBytes           int                 `json:"payloadBytes"`
	InputSHA256            string              `json:"inputSha256"`
	Status                 AutomationRunStatus `json:"status"`
	SandboxID              *string             `json:"sandboxId"`
	WorkerJobID            *string             `json:"workerJobId"`
	ExitCode               *int                `json:"exitCode"`
	Output                 string              `json:"output"`
	OutputTruncated        bool                `json:"outputTruncated"`
	CleanupStatus          string              `json:"cleanupStatus"`
	ErrorCode              string              `json:"errorCode"`
	ErrorMessage           string              `json:"errorMessage"`
	ReceivedAt             time.Time           `json:"receivedAt"`
	QueuedAt               *time.Time          `json:"queuedAt"`
	StartedAt              *time.Time          `json:"startedAt"`
	FinishedAt             *time.Time          `json:"finishedAt"`
	ExpiresAt              *time.Time          `json:"expiresAt"`
}

type AutomationPreviewInput struct {
	Automation AutomationInput   `json:"automation"`
	Payload    any               `json:"payload"`
	Headers    map[string]string `json:"headers"`
	Query      map[string]any    `json:"query"`
}

type AutomationPreview struct {
	Matched bool                  `json:"matched"`
	Command string                `json:"command,omitempty"`
	Target  string                `json:"target,omitempty"`
	Input   *ResourceInputPreview `json:"input,omitempty"`
}

type ResourceInputPreview struct {
	ID          string         `json:"id"`
	Kind        Kind           `json:"kind"`
	ProjectID   *string        `json:"projectId"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Enabled     bool           `json:"enabled"`
	Spec        map[string]any `json:"spec"`
}

type AutomationDelivery struct {
	EndpointID     string
	Authorization  string
	Timestamp      string
	Signature      string
	IdempotencyKey string
	Body           []byte
	Headers        map[string]string
	Query          map[string]any
}

type AutomationTriggerResult struct {
	Run         AutomationRun `json:"run"`
	Duplicate   bool          `json:"duplicate"`
	StatusToken string        `json:"-"`
}

func NormalizeAutomationInput(input *AutomationInput) {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.ConditionTemplate = strings.TrimSpace(input.ConditionTemplate)
	if input.ConditionTemplate == "" {
		input.ConditionTemplate = "true"
	}
	input.Trigger.Type = strings.ToLower(strings.TrimSpace(input.Trigger.Type))
	input.Trigger.AuthMode = AutomationAuthMode(strings.ToLower(strings.TrimSpace(string(input.Trigger.AuthMode))))
	input.Action.Type = strings.ToLower(strings.TrimSpace(input.Action.Type))
	input.Action.TemplateID = strings.TrimSpace(input.Action.TemplateID)
	input.Action.TargetTemplate = strings.TrimSpace(input.Action.TargetTemplate)
	input.Action.CommandTemplate = strings.TrimSpace(input.Action.CommandTemplate)
	if input.Action.Type == "run-task" && input.Action.TimeoutSeconds == 0 {
		input.Action.TimeoutSeconds = 900
	}
	input.Action.CleanupPolicy = AutomationCleanupPolicy(strings.ToLower(strings.TrimSpace(string(input.Action.CleanupPolicy))))
	if input.Action.CleanupPolicy == "" {
		input.Action.CleanupPolicy = AutomationCleanupNever
	}
	modelBindings := make(map[string]string, len(input.Action.ModelBindings))
	for credentialID, modelID := range input.Action.ModelBindings {
		credentialID = strings.TrimSpace(credentialID)
		modelID = strings.TrimSpace(modelID)
		if credentialID != "" || modelID != "" {
			modelBindings[credentialID] = modelID
		}
	}
	input.Action.ModelBindings = modelBindings
	input.Action.InputTemplate = strings.TrimSpace(input.Action.InputTemplate)
}

func ValidateAutomationInput(input AutomationInput) error {
	if input.ProjectID == "" {
		return &ValidationError{Message: "请选择所属项目"}
	}
	if n := utf8.RuneCountInString(input.Name); n < 2 || n > 80 {
		return &ValidationError{Message: "自动化名称需要 2 到 80 个字符"}
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return &ValidationError{Message: "自动化简介不能超过 500 个字符"}
	}
	if input.Trigger.Type != "webhook" {
		return &ValidationError{Message: "当前仅支持 Webhook 触发器"}
	}
	switch input.Trigger.AuthMode {
	case AutomationAuthBearer, AutomationAuthHMAC, AutomationAuthGitHub, AutomationAuthGitLab, AutomationAuthStandardWebhook:
	default:
		return &ValidationError{Message: "Webhook 鉴权方式无效"}
	}
	if len(input.ConditionTemplate) == 0 || len(input.ConditionTemplate) > 8<<10 {
		return &ValidationError{Message: "执行条件不能为空且不能超过 8 KiB"}
	}
	if input.Secret != "" && (utf8.RuneCountInString(input.Secret) < 16 || utf8.RuneCountInString(input.Secret) > 512) {
		return &ValidationError{Message: "自定义 Webhook 密钥需要 16 到 512 个字符"}
	}
	if input.Action.ExpiresAfterSeconds != 0 && (input.Action.ExpiresAfterSeconds < 60 || input.Action.ExpiresAfterSeconds > 30*24*60*60) {
		return &ValidationError{Message: "沙箱自动回收时间需要介于 60 秒和 30 天之间"}
	}
	switch input.Action.Type {
	case "create-sandbox", "run-task":
		if input.Action.TemplateID == "" {
			return &ValidationError{Message: "请选择沙箱模板"}
		}
		for credentialID, modelID := range input.Action.ModelBindings {
			if credentialID == "" || modelID == "" {
				return &ValidationError{Message: "请为沙箱中的每个模型服务选择具体模型"}
			}
		}
		if len(input.Action.InputTemplate) == 0 || len(input.Action.InputTemplate) > 64<<10 {
			return &ValidationError{Message: "沙箱输入模板不能为空且不能超过 64 KiB"}
		}
	case "destroy-sandbox":
		if len(input.Action.TargetTemplate) == 0 || len(input.Action.TargetTemplate) > 8<<10 {
			return &ValidationError{Message: "销毁沙箱动作需要有效的目标模板"}
		}
	default:
		return &ValidationError{Message: "自动化动作无效"}
	}
	if input.Action.Type == "run-task" {
		if len(input.Action.CommandTemplate) == 0 || len(input.Action.CommandTemplate) > 64<<10 {
			return &ValidationError{Message: "任务命令不能为空且不能超过 64 KiB"}
		}
		if input.Action.TimeoutSeconds < 10 || input.Action.TimeoutSeconds > 3600 {
			return &ValidationError{Message: "任务超时需要介于 10 秒和 1 小时之间"}
		}
	}
	switch input.Action.CleanupPolicy {
	case AutomationCleanupNever, AutomationCleanupOnSuccess, AutomationCleanupAlways:
	default:
		return &ValidationError{Message: "沙箱清理策略无效"}
	}
	return nil
}

func IsAutomationValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}
