package platform

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type AutomationAuthMode string

const (
	AutomationAuthBearer AutomationAuthMode = "bearer"
	AutomationAuthHMAC   AutomationAuthMode = "hmac-sha256"
)

type AutomationTriggerInput struct {
	Type     string             `json:"type"`
	AuthMode AutomationAuthMode `json:"authMode"`
}

type AutomationActionInput struct {
	Type          string            `json:"type"`
	TemplateID    string            `json:"templateId"`
	ModelBindings map[string]string `json:"modelBindings"`
	InputTemplate string            `json:"inputTemplate"`
}

type AutomationInput struct {
	ProjectID   string                 `json:"projectId"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Enabled     bool                   `json:"enabled"`
	Trigger     AutomationTriggerInput `json:"trigger"`
	Action      AutomationActionInput  `json:"action"`
}

type Automation struct {
	ID              string                 `json:"id"`
	ProjectID       string                 `json:"projectId"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description"`
	Enabled         bool                   `json:"enabled"`
	Trigger         AutomationTriggerInput `json:"trigger"`
	Action          AutomationActionInput  `json:"action"`
	EndpointID      string                 `json:"endpointId"`
	SecretLastFour  string                 `json:"secretLastFour"`
	CreatedBy       *string                `json:"createdBy"`
	UpdatedBy       *string                `json:"updatedBy"`
	LastTriggeredAt *time.Time             `json:"lastTriggeredAt"`
	SecretRotatedAt time.Time              `json:"secretRotatedAt"`
	CreatedAt       time.Time              `json:"createdAt"`
	UpdatedAt       time.Time              `json:"updatedAt"`
}

type AutomationRunStatus string

const (
	AutomationRunEvaluating   AutomationRunStatus = "evaluating"
	AutomationRunQueued       AutomationRunStatus = "queued"
	AutomationRunProvisioning AutomationRunStatus = "provisioning"
	AutomationRunSucceeded    AutomationRunStatus = "succeeded"
	AutomationRunFailed       AutomationRunStatus = "failed"
)

type AutomationRun struct {
	ID                     string              `json:"id"`
	AutomationID           *string             `json:"automationId"`
	ProjectID              string              `json:"projectId"`
	AutomationName         string              `json:"automationName"`
	TemplateID             string              `json:"templateId"`
	TemplateName           string              `json:"templateName"`
	TriggerSource          string              `json:"triggerSource"`
	AuthMode               AutomationAuthMode  `json:"authMode"`
	IdempotencyFingerprint string              `json:"idempotencyFingerprint"`
	PayloadSHA256          string              `json:"payloadSha256"`
	PayloadBytes           int                 `json:"payloadBytes"`
	InputSHA256            string              `json:"inputSha256"`
	Status                 AutomationRunStatus `json:"status"`
	SandboxID              *string             `json:"sandboxId"`
	WorkerJobID            *string             `json:"workerJobId"`
	ErrorCode              string              `json:"errorCode"`
	ErrorMessage           string              `json:"errorMessage"`
	ReceivedAt             time.Time           `json:"receivedAt"`
	QueuedAt               *time.Time          `json:"queuedAt"`
	StartedAt              *time.Time          `json:"startedAt"`
	FinishedAt             *time.Time          `json:"finishedAt"`
}

type AutomationPreviewInput struct {
	Automation AutomationInput   `json:"automation"`
	Payload    any               `json:"payload"`
	Headers    map[string]string `json:"headers"`
	Query      map[string]any    `json:"query"`
}

type AutomationPreview struct {
	Input ResourceInputPreview `json:"input"`
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
	Run       AutomationRun `json:"run"`
	Duplicate bool          `json:"duplicate"`
}

func NormalizeAutomationInput(input *AutomationInput) {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Trigger.Type = strings.ToLower(strings.TrimSpace(input.Trigger.Type))
	input.Trigger.AuthMode = AutomationAuthMode(strings.ToLower(strings.TrimSpace(string(input.Trigger.AuthMode))))
	input.Action.Type = strings.ToLower(strings.TrimSpace(input.Action.Type))
	input.Action.TemplateID = strings.TrimSpace(input.Action.TemplateID)
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
	if input.Trigger.AuthMode != AutomationAuthBearer && input.Trigger.AuthMode != AutomationAuthHMAC {
		return &ValidationError{Message: "Webhook 鉴权方式无效"}
	}
	if input.Action.Type != "create-sandbox" {
		return &ValidationError{Message: "当前仅支持创建沙箱动作"}
	}
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
	return nil
}

func IsAutomationValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}
