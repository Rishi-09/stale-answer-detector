package freshness

import (
	"math"
	"testing"
	"time"
)

func TestCalculateFreshness_Branches(t *testing.T) {
	tests := []struct {
		name            string
		agreement       string
		authority       float64
		volatility      float64
		expectedVerdict string
		expectedScore   float64
	}{
		{
			name:            "Match - Standard authority",
			agreement:       AgreementMatch,
			authority:       0.70,
			volatility:      0.50,
			expectedVerdict: VerdictCurrent,
			expectedScore:   0.910, // 0.70 + 0.30 * 0.70 = 0.910
		},
		{
			name:            "Match - Maximum authority",
			agreement:       AgreementMatch,
			authority:       1.00,
			volatility:      0.50,
			expectedVerdict: VerdictCurrent,
			expectedScore:   1.000, // 0.70 + 0.30 * 1.0 = 1.000
		},
		{
			name:            "Match - Zero authority boundary",
			agreement:       AgreementMatch,
			authority:       0.00,
			volatility:      0.50,
			expectedVerdict: VerdictCurrent,
			expectedScore:   0.700, // 0.70 + 0.30 * 0.0 = 0.700
		},
		{
			name:            "Mismatch - Standard authority",
			agreement:       AgreementMismatch,
			authority:       0.70,
			volatility:      0.50,
			expectedVerdict: VerdictStale,
			expectedScore:   0.075, // 0.25 * (1 - 0.70) = 0.075
		},
		{
			name:            "Mismatch - Maximum authority (confident outdated)",
			agreement:       AgreementMismatch,
			authority:       1.00,
			volatility:      0.50,
			expectedVerdict: VerdictStale,
			expectedScore:   0.000, // 0.25 * (1 - 1.0) = 0.000
		},
		{
			name:            "Mismatch - Zero authority",
			agreement:       AgreementMismatch,
			authority:       0.00,
			volatility:      0.50,
			expectedVerdict: VerdictStale,
			expectedScore:   0.250, // 0.25 * (1 - 0.0) = 0.250
		},
		{
			name:            "None - Low volatility (1 - v > 0.50 capped at 0.50)",
			agreement:       AgreementNone,
			authority:       0.70,
			volatility:      0.20,
			expectedVerdict: VerdictUnverifiable,
			expectedScore:   0.500, // min(0.50, 1 - 0.20) = 0.500
		},
		{
			name:            "None - High volatility (1 - v < 0.50)",
			agreement:       AgreementNone,
			authority:       0.70,
			volatility:      0.80,
			expectedVerdict: VerdictUnverifiable,
			expectedScore:   0.200, // min(0.50, 1 - 0.80) = 0.200
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, score := CalculateFreshness(tt.agreement, tt.authority, tt.volatility)
			if verdict != tt.expectedVerdict {
				t.Errorf("expected verdict %s, got %s", tt.expectedVerdict, verdict)
			}
			if math.Abs(score-tt.expectedScore) > 0.001 {
				t.Errorf("expected score %.3f, got %.3f", tt.expectedScore, score)
			}
		})
	}
}

func TestComputeVolatilityRisk(t *testing.T) {
	checkDate := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

	// Case 1: Pricing half-life is 90 days. Exactly 90 days elapsed:
	// v = 1 - exp(-ln(2)) = 1 - 0.5 = 0.50
	cutoffPricing := checkDate.AddDate(0, 0, -90)
	vPricing := ComputeVolatilityRisk(CategoryPricing, cutoffPricing, checkDate)
	if math.Abs(vPricing-0.50) > 0.01 {
		t.Errorf("expected v ~0.50 for 90 days gap on pricing, got %.3f", vPricing)
	}

	// Case 2: Zero gap (cutoff = checkDate) -> should clamp to lower bound 0.10
	vZero := ComputeVolatilityRisk(CategoryPricing, checkDate, checkDate)
	if vZero != 0.10 {
		t.Errorf("expected v clamped to 0.10 for zero gap, got %.3f", vZero)
	}

	// Case 3: Very large gap (1000 days) -> should clamp to upper bound 0.95
	cutoffOld := checkDate.AddDate(0, 0, -1000)
	vOld := ComputeVolatilityRisk(CategoryPricing, cutoffOld, checkDate)
	if vOld != 0.95 {
		t.Errorf("expected v clamped to 0.95 for large gap, got %.3f", vOld)
	}
}

func TestComputeSourceAuthority(t *testing.T) {
	checkDate := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	allowlist := []string{"openai.com", "anthropic.com"}

	// 1. Unlisted domain, no date -> baseline 0.70
	authBase := ComputeSourceAuthority("https://blog.randomtech.io/post", "", checkDate, allowlist)
	if math.Abs(authBase-0.70) > 0.001 {
		t.Errorf("expected baseline authority 0.70, got %.3f", authBase)
	}

	// 2. Allowlisted domain -> +0.20 -> 0.90
	authAllowed := ComputeSourceAuthority("https://openai.com/pricing", "", checkDate, allowlist)
	if math.Abs(authAllowed-0.90) > 0.001 {
		t.Errorf("expected allowlisted authority 0.90, got %.3f", authAllowed)
	}

	// 3. Allowlisted domain + recent publish date (10 days ago) -> 0.70 + 0.20 + 0.08 = 0.98
	recentDate := checkDate.AddDate(0, 0, -10).Format("2006-01-02")
	authRecent := ComputeSourceAuthority("https://openai.com/pricing", recentDate, checkDate, allowlist)
	if math.Abs(authRecent-0.98) > 0.001 {
		t.Errorf("expected recent allowlisted authority 0.98, got %.3f", authRecent)
	}
}

func TestShouldFlag(t *testing.T) {
	if !ShouldFlag(0.12, 0.50) {
		t.Error("expected 0.12 to be flagged below 0.50")
	}
	if ShouldFlag(0.85, 0.50) {
		t.Error("expected 0.85 not to be flagged below 0.50")
	}
}

