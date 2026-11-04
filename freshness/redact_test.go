package freshness

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	input := "Contact developer at alice.smith@corp.example.com or +1 (555) 234-5678. Authorization: Bearer sk-ant-api03-abcdef1234567890."

	rules := []string{"email", "phone", "secret"}
	redacted := Redact(input, rules)

	// Check email redaction
	if strings.Contains(redacted, "alice.smith@corp.example.com") {
		t.Error("expected email address to be redacted")
	}
	if !strings.Contains(redacted, "[REDACTED_EMAIL]") {
		t.Error("expected [REDACTED_EMAIL] placeholder")
	}

	// Check phone redaction
	if strings.Contains(redacted, "555") {
		t.Error("expected phone number to be redacted")
	}
	if !strings.Contains(redacted, "[REDACTED_PHONE]") {
		t.Error("expected [REDACTED_PHONE] placeholder")
	}

	// Check secret token redaction
	if strings.Contains(redacted, "sk-ant-api03") {
		t.Error("expected secret token to be redacted")
	}
	if !strings.Contains(redacted, "[REDACTED_SECRET]") {
		t.Error("expected [REDACTED_SECRET] placeholder")
	}
}

