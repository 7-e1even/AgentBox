package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAutomatedSandboxNameStaysWithinResourceLimit(t *testing.T) {
	name := automatedSandboxName(strings.Repeat("沙", 80), "12345678")
	if got := utf8.RuneCountInString(name); got != 80 {
		t.Fatalf("generated name length = %d, want 80", got)
	}
	if !strings.HasSuffix(name, "-12345678") {
		t.Fatalf("generated name = %q, missing run suffix", name)
	}
}
