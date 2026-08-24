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

type AutomationInput struct {
	ProjectID     string                 `json:"projectId"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Enabled       bool                   `json:"enabled"`
	Secret        string                 `json:"secret,omitempty"`
	Trigger       AutomationTriggerInput `json:"trigger"`
	TemplateID    string                 `json:"templateId"`
	ModelBindings map[string]string      `json:"modelBindings"`
}

type Automation struct {
	ID              string                 `json:"id"`
	ProjectID       string                 `json:"projectId"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description"`
	Enabled         bool                   `json:"enabled"`
	Trigger         AutomationTriggerInput `json:"trigger"`
	TemplateID      string                 `json:"templateId"`
	ModelBindings   map[string]string      `json:"modelBindings"`
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

type AutomationEvent struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Source     string    `json:"source"`
	Time       time.Time `json:"time"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type AutomationRun struct {
	ID                     string               `json:"id"`
	AutomationID           *string              `json:"automationId"`
	EndpointID             string               `json:"endpointId,omitempty"`
	ProjectID              string               `json:"projectId"`
	AutomationName         string               `json:"automationName"`
	TemplateID             string               `json:"templateId"`
	TemplateName           string               `json:"templateName"`
	TriggerSource          string               `json:"triggerSource"`
	AuthMode               AutomationAuthMode   `json:"authMode"`
	Event                  AutomationEvent      `json:"event"`
	IdempotencyFingerprint string               `json:"idempotencyFingerprint"`
	PayloadSHA256          string               `json:"payloadSha256"`
	PayloadBytes           int                  `json:"payloadBytes"`
	InputSHA256            string               `json:"inputSha256"`
	Status                 AutomationRunStatus  `json:"status"`
	SandboxID              *string              `json:"sandboxId"`
	WorkerJobID            *string              `json:"workerJobId"`
	ErrorCode              string               `json:"errorCode"`
	ErrorMessage           string               `json:"errorMessage"`
	ReceivedAt             time.Time            `json:"receivedAt"`
	QueuedAt               *time.Time           `json:"queuedAt"`
	StartedAt              *time.Time           `json:"startedAt"`
	FinishedAt             *time.Time           `json:"finishedAt"`
	Provisioning           ProvisioningProgress `json:"provisioning"`
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
	input.Trigger.Type = strings.ToLower(strings.TrimSpace(input.Trigger.Type))
	input.Trigger.AuthMode = AutomationAuthMode(strings.ToLower(strings.TrimSpace(string(input.Trigger.AuthMode))))
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	modelBindings := make(map[string]string, len(input.ModelBindings))
	for credentialID, modelID := range input.ModelBindings {
		credentialID = strings.TrimSpace(credentialID)
		modelID = strings.TrimSpace(modelID)
		if credentialID != "" || modelID != "" {
			modelBindings[credentialID] = modelID
		}
	}
	input.ModelBindings = modelBindings
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
	if input.Secret != "" && (utf8.RuneCountInString(input.Secret) < 16 || utf8.RuneCountInString(input.Secret) > 512) {
		return &ValidationError{Message: "自定义 Webhook 密钥需要 16 到 512 个字符"}
	}
	if strings.ContainsAny(input.Secret, "\r\n") {
		return &ValidationError{Message: "自定义 Webhook 密钥不能包含换行符"}
	}
	if input.TemplateID == "" {
		return &ValidationError{Message: "请选择沙箱模板"}
	}
	for credentialID, modelID := range input.ModelBindings {
		if credentialID == "" || modelID == "" {
			return &ValidationError{Message: "请为沙箱中的每个模型服务选择具体模型"}
		}
	}
	return nil
}

func IsAutomationValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}
