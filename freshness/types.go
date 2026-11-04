package freshness

import (
	"time"
)

// Supported claim categories
const (
	CategoryPricing    = "pricing"
	CategoryAPIVersion = "api_version"
	CategoryRegulation = "regulation"
	CategoryCompany    = "company"
)

// Verdict states
const (
	VerdictCurrent      = "current"
	VerdictStale        = "stale"
	VerdictUnverifiable = "unverifiable"
)

// Agreement states from verifier comparison
const (
	AgreementMatch    = "match"
	AgreementMismatch = "mismatch"
	AgreementNone     = "none"
)

// Operational modes
const (
	ModeAsync  = "async"
	ModeStrict = "strict"
)

// Evidence represents a piece of supporting or refuting web evidence.
type Evidence struct {
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Published   string    `json:"published,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
	Authority   float64   `json:"authority"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

// Claim represents an extracted factual claim with its verification state.
type Claim struct {
	ClaimID        string     `json:"claim_id"`
	Text           string     `json:"text"`
	Category       string     `json:"category"`
	Entity         string     `json:"entity"`
	Attribute      string     `json:"attribute"`
	ValueInAnswer  string     `json:"value_in_answer"`
	Span           [2]int     `json:"span"`
	Verdict        string     `json:"verdict"`
	FreshnessScore float64    `json:"freshness_score"`
	CorrectedValue string     `json:"corrected_value,omitempty"`
	VolatilityRisk float64    `json:"volatility_risk"`
	FromCache      bool       `json:"from_cache"`
	Evidence       []Evidence `json:"evidence,omitempty"`
}

// VerdictStats carries telemetry counters for a verification run.
type VerdictStats struct {
	ClaimsDetected int   `json:"claims_detected"`
	Searches       int   `json:"searches"`
	LatencyMS      int64 `json:"latency_ms"`
	CreditsSpent   int   `json:"credits_spent"`
}

// VerdictRecord is the complete audit record stored and served via API.
type VerdictRecord struct {
	RequestID string       `json:"request_id"`
	Model     string       `json:"model"`
	CheckedAt time.Time    `json:"checked_at"`
	Mode      string       `json:"mode"`
	Claims    []Claim      `json:"claims"`
	Stats     VerdictStats `json:"stats"`
}

// CandidateSpan represents a text region matched by Tier 1 pre-filtering.
type CandidateSpan struct {
	Category string
	Start    int
	End      int
	Snippet  string
	Hint     string
}

// VerificationResult holds the output of the Verifier before score calculation.
type VerificationResult struct {
	Agreement      string
	Evidence       []Evidence
	CorrectedValue string
	Authority      float64
}

// LimitsConfig configures operational thresholds.
type LimitsConfig struct {
	MaxClaimsPerResponse   int `json:"max_claims_per_response"`
	MaxSearchesPerResponse int `json:"max_searches_per_response"`
	MaxResponseChars       int `json:"max_response_chars"`
	QueueSize              int `json:"queue_size"`
	Workers                int `json:"workers"`
	TavilyCreditsPerHour   int `json:"tavily_credits_per_hour"`
}

// TimeoutsConfig configures execution deadlines.
type TimeoutsConfig struct {
	ExtractorMS   int `json:"extractor"`
	VerifyMS      int `json:"verify"`
	StrictTotalMS int `json:"strict_total"`
}

// CircuitBreakerConfig configures resilience parameters.
type CircuitBreakerConfig struct {
	Failures  int `json:"failures"`
	CooldownS int `json:"cooldown_s"`
}

// PrivacyConfig configures redaction and telemetry scrubbing.
type PrivacyConfig struct {
	Redact                []string `json:"redact"`
	SendPromptToExtractor bool     `json:"send_prompt_to_extractor"`
}

// Config represents the complete Stale-Answer Detector configuration.
type Config struct {
	Mode             string                 `json:"mode"`
	Categories       []string               `json:"categories"`
	TavilyAPIKey     string                 `json:"tavily_api_key"`
	ExtractorModel   string                 `json:"extractor_model"`
	FlagBelow        float64                `json:"flag_below"`
	Limits           LimitsConfig           `json:"limits"`
	TimeoutsMS       TimeoutsConfig         `json:"timeouts_ms"`
	CacheTTLHours    map[string]int         `json:"cache_ttl_hours"`
	CircuitBreaker   CircuitBreakerConfig   `json:"circuit_breaker"`
	Privacy          PrivacyConfig          `json:"privacy"`
	DomainAllowlist  map[string][]string    `json:"domain_allowlist"`
	KnowledgeCutoffs map[string]string      `json:"knowledge_cutoffs,omitempty"`
}

// DefaultConfig returns safe production defaults.
func DefaultConfig() Config {
	return Config{
		Mode: ModeAsync,
		Categories: []string{
			CategoryPricing,
			CategoryAPIVersion,
			CategoryRegulation,
			CategoryCompany,
		},
		TavilyAPIKey:   "",
		ExtractorModel: "openai/gpt-4o-mini",
		FlagBelow:      0.50,
		Limits: LimitsConfig{
			MaxClaimsPerResponse:   8,
			MaxSearchesPerResponse: 4,
			MaxResponseChars:       8000,
			QueueSize:              256,
			Workers:                4,
			TavilyCreditsPerHour:   200,
		},
		TimeoutsMS: TimeoutsConfig{
			ExtractorMS:   2500,
			VerifyMS:      4000,
			StrictTotalMS: 3000,
		},
		CacheTTLHours: map[string]int{
			CategoryPricing:    6,
			CategoryAPIVersion: 24,
			CategoryCompany:    72,
			CategoryRegulation: 168,
		},
		CircuitBreaker: CircuitBreakerConfig{
			Failures:  5,
			CooldownS: 60,
		},
		Privacy: PrivacyConfig{
			Redact:                []string{"email", "phone", "secret"},
			SendPromptToExtractor: false,
		},
		DomainAllowlist: map[string][]string{
			CategoryPricing:    {"openai.com", "anthropic.com", "docs.aws.amazon.com", "cloud.google.com"},
			CategoryAPIVersion: {"github.com", "python.org", "kubernetes.io", "nodejs.org", "go.dev"},
			CategoryCompany:    {"bloomberg.com", "reuters.com", "techcrunch.com", "sec.gov"},
			CategoryRegulation: {"europa.eu", "ftc.gov", "sec.gov", "congress.gov"},
		},
		KnowledgeCutoffs: map[string]string{
			"gpt-4o":              "2023-10-01",
			"gpt-4o-mini":         "2023-10-01",
			"gpt-4-turbo":         "2023-12-01",
			"claude-3-5-sonnet":   "2024-04-01",
			"claude-3-opus":       "2023-08-01",
			"gemini-1.5-pro":      "2023-11-01",
			"gemini-1.5-flash":    "2023-11-01",
		},
	}
}
