package freshness

import (
	"sync"
	"time"
)

// BudgetController governs per-response caps, hourly credit spending limits, and daily credit caps.
type BudgetController struct {
	mu                   sync.Mutex
	maxClaimsPerResp     int
	maxSearchesPerResp   int
	maxResponseChars     int
	tavilyCreditsPerHour int
	tavilyCreditsPerDay  int
	creditHourStart      time.Time
	creditsSpentInHour   int
	creditDayStart       time.Time
	creditsSpentInDay    int
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
	creditsPerDay := cfg.TavilyCreditsPerDay
	if creditsPerDay <= 0 {
		creditsPerDay = 400
	}

	now := time.Now()
	return &BudgetController{
		maxClaimsPerResp:     maxClaims,
		maxSearchesPerResp:   maxSearches,
		maxResponseChars:     maxChars,
		tavilyCreditsPerHour: creditsPerHour,
		tavilyCreditsPerDay:  creditsPerDay,
		creditHourStart:      now,
		creditsSpentInHour:   0,
		creditDayStart:       now,
		creditsSpentInDay:    0,
	}
}

// AllowSearch checks if spending the specified credits is permitted under both hourly and daily budget caps.
// If allowed, it records the spend and returns true.
func (b *BudgetController) AllowSearch(credits int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	// Slide hourly window
	if now.Sub(b.creditHourStart) >= time.Hour {
		b.creditHourStart = now
		b.creditsSpentInHour = 0
	}
	// Slide daily window
	if now.Sub(b.creditDayStart) >= 24*time.Hour {
		b.creditDayStart = now
		b.creditsSpentInDay = 0
	}

	if b.tavilyCreditsPerHour > 0 && b.creditsSpentInHour+credits > b.tavilyCreditsPerHour {
		return false
	}
	if b.tavilyCreditsPerDay > 0 && b.creditsSpentInDay+credits > b.tavilyCreditsPerDay {
		return false
	}

	b.creditsSpentInHour += credits
	b.creditsSpentInDay += credits
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

// CreditsSpent returns current spend in the active hourly window.
func (b *BudgetController) CreditsSpent() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.creditsSpentInHour
}

// CreditsSpentDay returns current spend in the active daily window.
func (b *BudgetController) CreditsSpentDay() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.creditsSpentInDay
}
