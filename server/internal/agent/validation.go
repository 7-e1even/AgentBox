package agent

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func validation(message string) error { return &ValidationError{Message: message} }

func Normalize(input *Input) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	input.Description = strings.TrimSpace(input.Description)
	input.Avatar = strings.TrimSpace(input.Avatar)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.ModelID = strings.TrimSpace(input.ModelID)
	input.SystemPrompt = strings.TrimSpace(input.SystemPrompt)
	if input.SkillIDs == nil {
		input.SkillIDs = []string{}
	}
	if input.MCPServerIDs == nil {
		input.MCPServerIDs = []string{}
	}
}

func Validate(input Input, catalog Catalog) error {
	if n := utf8.RuneCountInString(input.Name); n < 2 || n > 60 {
		return validation("名称需要 2 到 60 个字符")
	}
	if n := utf8.RuneCountInString(input.Slug); n < 2 || n > 64 || !slugPattern.MatchString(input.Slug) {
		return validation("标识只能包含小写字母、数字和连字符，长度为 2 到 64")
	}
	if utf8.RuneCountInString(input.Description) > 280 {
		return validation("简介不能超过 280 个字符")
	}
	if n := utf8.RuneCountInString(input.Avatar); n < 1 || n > 4 {
		return validation("头像文字需要 1 到 4 个字符")
	}
	if n := utf8.RuneCountInString(input.SystemPrompt); n < 20 || n > 16000 {
		return validation("系统指令需要 20 到 16000 个字符")
	}
	if input.Temperature < 0 || input.Temperature > 2 {
		return validation("Temperature 需要在 0 到 2 之间")
	}
	if input.MaxSteps < 1 || input.MaxSteps > 50 {
		return validation("最大步骤需要在 1 到 50 之间")
	}
	if input.Status != StatusDraft && input.Status != StatusActive && input.Status != StatusArchived {
		return validation("Agent 状态无效")
	}
	if len(input.SkillIDs) > 20 || len(input.MCPServerIDs) > 20 {
		return validation("单个 Agent 最多绑定 20 个 Skill 和 20 个 MCP Server")
	}

	var provider *Provider
	for i := range catalog.Providers {
		if catalog.Providers[i].ID == input.ProviderID {
			provider = &catalog.Providers[i]
			break
		}
	}
	if provider == nil {
		return validation("Provider 不存在")
	}
	modelFound := false
	for _, model := range provider.Models {
		if model.ID == input.ModelID {
			modelFound = true
			break
		}
	}
	if !modelFound {
		return validation("模型不属于所选 Provider")
	}
	if input.CredentialID == nil {
		return validation("请选择凭据引用")
	}
	credentialFound := false
	for _, credential := range catalog.Credentials {
		if credential.ID == *input.CredentialID && credential.ProviderID == input.ProviderID {
			credentialFound = true
			break
		}
	}
	if !credentialFound {
		return validation("请选择与 Provider 匹配的凭据引用")
	}

	skillSet := make(map[string]struct{}, len(catalog.Skills))
	for _, skill := range catalog.Skills {
		skillSet[skill.ID] = struct{}{}
	}
	for _, id := range input.SkillIDs {
		if _, ok := skillSet[id]; !ok {
			return validation("包含不存在的 Skill")
		}
	}
	mcpSet := make(map[string]struct{}, len(catalog.MCPServers))
	for _, server := range catalog.MCPServers {
		mcpSet[server.ID] = struct{}{}
	}
	for _, id := range input.MCPServerIDs {
		if _, ok := mcpSet[id]; !ok {
			return validation("包含不存在的 MCP Server")
		}
	}
	return nil
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}
