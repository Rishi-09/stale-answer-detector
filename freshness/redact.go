package freshness

import (
	"regexp"
	"strings"
	"sync"
)

var (
	redactOnce    sync.Once
	reEmail       *regexp.Regexp
	rePhone       *regexp.Regexp
	reSecretToken *regexp.Regexp
)

func initRedactor() {
	redactOnce.Do(func() {
		reEmail = regexp.MustCompile(`(?i)\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
		rePhone = regexp.MustCompile(`\b(?:\+?\d{1,3}[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`)
		reSecretToken = regexp.MustCompile(`(?i)\b(?:bearer\s+[a-zA-Z0-9_\-\.]{20,}|(?:sk|ghp|xoxb|tavl|bfk)-[a-zA-Z0-9_\-]{16,})\b`)
	})
}

// Redact cleans sensitive information from text according to configured rules.
// Rules can include: "email", "phone", "secret".
func Redact(text string, rules []string) string {
	if len(text) == 0 || len(rules) == 0 {
		return text
	}

	initRedactor()
	result := text

	for _, rule := range rules {
		switch strings.ToLower(strings.TrimSpace(rule)) {
		case "email":
			result = reEmail.ReplaceAllString(result, "[REDACTED_EMAIL]")
		case "phone":
			result = rePhone.ReplaceAllString(result, "[REDACTED_PHONE]")
		case "secret":
			result = reSecretToken.ReplaceAllString(result, "[REDACTED_SECRET]")
		}
	}

	return result
}
