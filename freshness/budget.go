package freshness

import (
	"sync"
	"time"
)

// BudgetController governs per-response caps and hourly credit spending limits.
type BudgetController struct {
	mu                   sync.Mutex
	maxClaimsPerResp     int
	maxSearchesPerResp   int
	maxResponseChars     int
	tavilyCreditsPerHour int
	creditWindowStart    time.Time
	creditsSpentInWindow int
}

// NewBudgetController constructs a budget and rate controller.
func NewBudgetController(cfg LimitsConfig) *BudgetController {
	maxClaims := cfg.MaxClaimsPerResponse
	if maxClaims <= 0 {
		maxClaims = 8
	}
	maxSearches := cfg.MaxSearchesPerResponse
	if maxSearches <= 0 {
		maxSearches = 4
	}
	maxChars := cfg.MaxResponseChars
	if maxChars <= 0 {
		maxChars = 8000
	}
	creditsPerHour := cfg.TavilyCreditsPerHour
	if creditsPerHour <= 0 {
		creditsPerHour = 200
	}

	return &BudgetController{
		maxClaimsPerResp:     maxClaims,
		maxSearchesPerResp:   maxSearches,
		maxResponseChars:     maxChars,
		tavilyCreditsPerHour: creditsPerHour,
		creditWindowStart:    time.Now(),
		creditsSpentInWindow: 0,
	}
}

// AllowSearch checks if spending the specified credits is permitted under the hourly budget.
// If allowed, it records the spend and returns true.
func (b *BudgetController) AllowSearch(credits int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	// Slide window after 1 hour
	if now.Sub(b.creditWindowStart) >= time.Hour {
		b.creditWindowStart = now
		b.creditsSpentInWindow = 0
	}

	if b.creditsSpentInWindow+credits > b.tavilyCreditsPerHour {
		return false
	}

	b.creditsSpentInWindow += credits
	return true
}

// MaxClaims returns the maximum claims allowed per response.
func (b *BudgetController) MaxClaims() int {
	return b.maxClaimsPerResp
}

// MaxSearches returns the maximum searches allowed per response.
func (b *BudgetController) MaxSearches() int {
	return b.maxSearchesPerResp
}

// MaxChars returns the response text truncation limit.
func (b *BudgetController) MaxChars() int {
	return b.maxResponseChars
}

// CreditsSpent returns current spend in the active window.
func (b *BudgetController) CreditsSpent() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.creditsSpentInWindow
}
