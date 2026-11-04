package freshness

import (
	"math"
	"net/url"
	"strings"
	"time"
)

// Default category half-lives in days
var defaultHalfLives = map[string]float64{
	CategoryPricing:    90.0,  // Fast moving (quarterly price adjustments)
	CategoryAPIVersion: 120.0, // Frequent updates (SDK / package releases)
	CategoryCompany:    180.0, // Executive and structural changes
	CategoryRegulation: 365.0, // Slower legislative and compliance timelines
}

// ComputeVolatilityRisk calculates v in [0, 1] based on category half-life and knowledge cutoff age.
// Formula: v = 1 - exp(- (deltaDays * ln(2)) / halfLife)
func ComputeVolatilityRisk(category string, cutoffDate, checkDate time.Time) float64 {
	halfLife, ok := defaultHalfLives[category]
	if !ok || halfLife <= 0 {
		halfLife = 180.0
	}

	deltaDays := checkDate.Sub(cutoffDate).Hours() / 24.0
	if deltaDays < 0 {
		deltaDays = 0
	}

	// Exponential decay risk of information drift
	v := 1.0 - math.Exp(-(deltaDays*math.Ln2)/halfLife)

	// Clamp to [0.10, 0.95]
	if v < 0.10 {
		v = 0.10
	}
	if v > 0.95 {
		v = 0.95
	}

	return math.Round(v*1000) / 1000
}

// ComputeSourceAuthority evaluates source trustworthiness a in [0, 1].
// Baseline: 0.70.
// +0.20 for domains in category allowlist.
// +0.08 for recent evidence (published within last 30 days).
func ComputeSourceAuthority(evidenceURL string, publishedDate string, checkDate time.Time, allowlist []string) float64 {
	authority := 0.70

	// Check domain allowlist
	if len(allowlist) > 0 && evidenceURL != "" {
		parsed, err := url.Parse(evidenceURL)
		if err == nil {
			host := strings.ToLower(parsed.Hostname())
			for _, allowed := range allowlist {
				allowed = strings.ToLower(strings.TrimSpace(allowed))
				if host == allowed || strings.HasSuffix(host, "."+allowed) {
					authority += 0.20
					break
				}
			}
		}
	}

	// Check publication recency
	if publishedDate != "" {
		pubTime, err := parseDate(publishedDate)
		if err == nil {
			daysOld := checkDate.Sub(pubTime).Hours() / 24.0
			if daysOld >= 0 && daysOld <= 30 {
				authority += 0.08
			} else if daysOld > 365 {
				authority -= 0.10
			}
		}
	}

	// Clamp to [0.20, 0.98]
	if authority < 0.20 {
		authority = 0.20
	}
	if authority > 0.98 {
		authority = 0.98
	}

	return math.Round(authority*1000) / 1000
}

// CalculateFreshness computes verdict and score per the specification formula:
// - match:    verdict = current,      score = 0.70 + 0.30 * a
// - mismatch: verdict = stale,        score = 0.25 * (1 - a)
// - none:     verdict = unverifiable, score = min(0.50, 1 - v)
func CalculateFreshness(agreement string, authority, volatility float64) (verdict string, score float64) {
	switch agreement {
	case AgreementMatch:
		verdict = VerdictCurrent
		score = 0.70 + (0.30 * authority)

	case AgreementMismatch:
		verdict = VerdictStale
		score = 0.25 * (1.0 - authority)

	case AgreementNone:
		fallthrough
	default:
		verdict = VerdictUnverifiable
		score = math.Min(0.50, 1.0-volatility)
	}

	// Clamp score strictly to [0.0, 1.0]
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	score = math.Round(score*1000) / 1000
	return verdict, score
}

// ShouldFlag determines if a claim's freshness score drops below the warning threshold.
func ShouldFlag(score float64, flagBelow float64) bool {
	if flagBelow <= 0 {
		flagBelow = 0.50
	}
	return score < flagBelow
}

func parseDate(d string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02T15:04:05Z",
		"Jan 02, 2006",
		"02 Jan 2006",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, d); err == nil {
			return t, nil
		}
	}
	return time.Time{}, time.ErrDeadlineExceeded
}
