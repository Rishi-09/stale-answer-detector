package freshness

import (
	"testing"
)

func TestBudgetController_HourlyCap(t *testing.T) {
	limits := LimitsConfig{
		MaxClaimsPerResponse:   5,
		MaxSearchesPerResponse: 2,
		TavilyCreditsPerHour:   5,
		TavilyCreditsPerDay:    100,
	}

	budget := NewBudgetController(limits)

	// Spend 5 credits (allowed)
	for i := 0; i < 5; i++ {
		if !budget.AllowSearch(1) {
			t.Fatalf("expected credit %d to be allowed", i+1)
		}
	}

	// 6th credit should be rejected due to hourly cap
	if budget.AllowSearch(1) {
		t.Fatal("expected 6th credit to be rejected by hourly budget cap")
	}

	if budget.CreditsSpent() != 5 {
		t.Errorf("expected 5 credits recorded in hour, got %d", budget.CreditsSpent())
	}
	if budget.CreditsSpentDay() != 5 {
		t.Errorf("expected 5 credits recorded in day, got %d", budget.CreditsSpentDay())
	}
}

func TestBudgetController_DailyCap(t *testing.T) {
	limits := LimitsConfig{
		MaxClaimsPerResponse:   5,
		MaxSearchesPerResponse: 2,
		TavilyCreditsPerHour:   50,
		TavilyCreditsPerDay:    8,
	}

	budget := NewBudgetController(limits)

	// Spend 8 credits (allowed)
	for i := 0; i < 8; i++ {
		if !budget.AllowSearch(1) {
			t.Fatalf("expected credit %d to be allowed under daily cap", i+1)
		}
	}

	// 9th credit rejected by daily cap even though hourly cap (50) is not reached
	if budget.AllowSearch(1) {
		t.Fatal("expected 9th credit to be rejected by daily budget cap")
	}

	if budget.CreditsSpentDay() != 8 {
		t.Errorf("expected 8 credits recorded in day, got %d", budget.CreditsSpentDay())
	}
}

func TestBudgetController_Limits(t *testing.T) {
	limits := LimitsConfig{
		MaxClaimsPerResponse:   10,
		MaxSearchesPerResponse: 3,
		MaxResponseChars:       5000,
		TavilyCreditsPerHour:   200,
		TavilyCreditsPerDay:    400,
	}
	budget := NewBudgetController(limits)

	if budget.MaxClaims() != 10 {
		t.Errorf("expected max claims 10, got %d", budget.MaxClaims())
	}
	if budget.MaxSearches() != 3 {
		t.Errorf("expected max searches 3, got %d", budget.MaxSearches())
	}
	if budget.MaxChars() != 5000 {
		t.Errorf("expected max chars 5000, got %d", budget.MaxChars())
	}
}
