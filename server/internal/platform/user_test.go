package platform

import "testing"

func TestNormalizeAndValidateUsername(t *testing.T) {
	input := UserInput{
		Name: "AgentBox Admin", Username: "  Admin.User  ", Email: "ADMIN@example.com",
		Password: "password123", Role: UserRoleAdmin, Status: UserStatusActive,
	}
	NormalizeUserInput(&input)
	if input.Username != "admin.user" {
		t.Fatalf("username = %q, want %q", input.Username, "admin.user")
	}
	if err := ValidateUserInput(input, true); err != nil {
		t.Fatal(err)
	}
}

func TestValidateUsernameRejectsEmailAddress(t *testing.T) {
	input := UserInput{
		Name: "AgentBox Admin", Username: "admin@example.com", Email: "admin@example.com",
		Password: "password123", Role: UserRoleAdmin, Status: UserStatusActive,
	}
	NormalizeUserInput(&input)
	if err := ValidateUserInput(input, true); err == nil {
		t.Fatal("email address was accepted as a username")
	}
}
