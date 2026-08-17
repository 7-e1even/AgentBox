package platform

import (
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type UserRole string

const (
	UserRoleAdmin    UserRole = "admin"
	UserRoleOperator UserRole = "operator"
	UserRoleViewer   UserRole = "viewer"
)

type UserStatus string

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
)

type UserInput struct {
	Name     string     `json:"name"`
	Username string     `json:"username"`
	Email    string     `json:"email"`
	Password string     `json:"password"`
	Role     UserRole   `json:"role"`
	Status   UserStatus `json:"status"`
}

type UserPreferences struct {
	SuccessNotifications bool   `json:"successNotifications"`
	Density              string `json:"density"`
	ShowCapabilities     bool   `json:"showCapabilities"`
	ShowInfrastructure   bool   `json:"showInfrastructure"`
	ShowGovernance       bool   `json:"showGovernance"`
}

type User struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Username    string          `json:"username"`
	Email       string          `json:"email"`
	Role        UserRole        `json:"role"`
	Status      UserStatus      `json:"status"`
	Preferences UserPreferences `json:"preferences"`
	LastLoginAt *time.Time      `json:"lastLoginAt"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

func DefaultUserPreferences() UserPreferences {
	return UserPreferences{
		SuccessNotifications: true,
		Density:              "comfortable",
		ShowCapabilities:     true,
		ShowInfrastructure:   true,
		ShowGovernance:       true,
	}
}

func ValidateUserPreferences(input UserPreferences) error {
	if input.Density != "comfortable" && input.Density != "compact" {
		return &ValidationError{Message: "列表密度无效"}
	}
	return nil
}

func NormalizeUserInput(input *UserInput) {
	input.Name = strings.TrimSpace(input.Name)
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Password = strings.TrimSpace(input.Password)
	if input.Role == "" {
		input.Role = UserRoleViewer
	}
	if input.Status == "" {
		input.Status = UserStatusActive
	}
}

func ValidateUserInput(input UserInput, passwordRequired bool) error {
	if n := utf8.RuneCountInString(input.Name); n < 2 || n > 80 {
		return &ValidationError{Message: "用户名称需要 2 到 80 个字符"}
	}
	if n := utf8.RuneCountInString(input.Username); n < 3 || n > 64 || !usernamePattern.MatchString(input.Username) {
		return &ValidationError{Message: "用户名需要 3 到 64 个字符，只能包含字母、数字、点、下划线和短横线"}
	}
	address, err := mail.ParseAddress(input.Email)
	if err != nil || !strings.EqualFold(address.Address, input.Email) || len(input.Email) > 254 {
		return &ValidationError{Message: "邮箱地址格式无效"}
	}
	if passwordRequired && input.Password == "" {
		return &ValidationError{Message: "请输入密码"}
	}
	if input.Password != "" && (utf8.RuneCountInString(input.Password) < 8 || utf8.RuneCountInString(input.Password) > 128) {
		return &ValidationError{Message: "密码需要 8 到 128 个字符"}
	}
	if input.Role != UserRoleAdmin && input.Role != UserRoleOperator && input.Role != UserRoleViewer {
		return &ValidationError{Message: "用户角色无效"}
	}
	if input.Status != UserStatusActive && input.Status != UserStatusDisabled {
		return &ValidationError{Message: "用户状态无效"}
	}
	return nil
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func IsUserValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}
