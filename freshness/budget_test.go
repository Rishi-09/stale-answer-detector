package freshness

import (
	"testing"
)

func TestBudgetController_HourlyCap(t *testing.T) {
	limits := LimitsConfig{
		MaxClaimsPerResponse:   5,
		MaxSearchesPerResponse: 2,
		TavilyCreditsPerHour:   5,
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
		t.Errorf("expected 5 credits recorded, got %d", budget.CreditsSpent())
	}
}

func TestBudgetController_Limits(t *testing.T) {
	limits := LimitsConfig{
		MaxClaimsPerResponse:   10,
		MaxSearchesPerResponse: 3,
		MaxResponseChars:       5000,
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

