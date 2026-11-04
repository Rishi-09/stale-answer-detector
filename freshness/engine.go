package freshness

import (
	"context"
	"strings"
	"time"
)

// Engine orchestrates end-to-end claim detection, extraction, search verification, and scoring.
type Engine struct {
	config    Config
	extractor Extractor
	verifier  Verifier
	cache     *ClaimCache
	budget    *BudgetController
	breaker   *CircuitBreaker
}

// NewEngine constructs a verification engine.
func NewEngine(cfg Config, extractor Extractor, verifier Verifier) *Engine {
	if extractor == nil {
		extractor = NewLocalExtractor()
	}
	if verifier == nil {
		verifier = NewLocalDeterministicVerifier()
	}

	cache := NewClaimCache(5000, cfg.CacheTTLHours)
	budget := NewBudgetController(cfg.Limits)
	breaker := NewCircuitBreaker(cfg.CircuitBreaker.Failures, cfg.CircuitBreaker.CooldownS)

	return &Engine{
		config:    cfg,
		extractor: extractor,
		verifier:  verifier,
		cache:     cache,
		budget:    budget,
		breaker:   breaker,
	}
}

// Process evaluates text for stale claims and produces a complete VerdictRecord.
func (e *Engine) Process(ctx context.Context, requestID, model, text string) (*VerdictRecord, error) {
	startTime := time.Now()

	record := &VerdictRecord{
		RequestID: requestID,
		Model:     model,
		CheckedAt: startTime,
		Mode:      e.config.Mode,
		Claims:    make([]Claim, 0),
		Stats: VerdictStats{
			ClaimsDetected: 0,
			Searches:       0,
			LatencyMS:      0,
			CreditsSpent:   0,
		},
	}

	// 1. Tier 1 Fast Pre-filter
	allowedCats := make(map[string]bool)
	for _, c := range e.config.Categories {
		allowedCats[c] = true
	}

	candidates := DetectCandidates(text, e.config.Limits.MaxResponseChars, allowedCats)
	if len(candidates) == 0 {
		record.Stats.LatencyMS = time.Since(startTime).Milliseconds()
		return record, nil
	}

	// 2. Redaction before extraction
	cleanedText := Redact(text, e.config.Privacy.Redact)

	// 3. Tier 2 Claim Extraction
	claims, err := e.extractor.Extract(ctx, cleanedText, candidates, e.config.Limits.MaxClaimsPerResponse)
	if err != nil {
		record.Stats.LatencyMS = time.Since(startTime).Milliseconds()
		return record, nil // Fail open
	}

	record.Stats.ClaimsDetected = len(claims)
	if len(claims) == 0 {
		record.Stats.LatencyMS = time.Since(startTime).Milliseconds()
		return record, nil
	}

	// Determine cutoff date for volatility calculation
	cutoffDate := e.getKnowledgeCutoff(model)

	// 4. Verification and Scoring
	searchesPerformed := 0
	creditsSpent := 0

	for i := range claims {
		claim := &claims[i]
		volatility := ComputeVolatilityRisk(claim.Category, cutoffDate, startTime)
		claim.VolatilityRisk = volatility

		// Check cache first
		if cached, hit := e.cache.Get(claim.Category, claim.Entity, claim.Attribute, claim.ValueInAnswer); hit {
			claim.Verdict = cached.Verdict
			claim.FreshnessScore = cached.FreshnessScore
			claim.CorrectedValue = cached.CorrectedValue
			claim.Evidence = cached.Evidence
			claim.FromCache = true
			continue
		}

		// Check circuit breaker
		if err := e.breaker.Allow(); err != nil {
			v, s := CalculateFreshness(AgreementNone, 0.50, volatility)
			claim.Verdict = v
			claim.FreshnessScore = s
			continue
		}

		// Check budget caps per response
		if searchesPerformed >= e.config.Limits.MaxSearchesPerResponse {
			v, s := CalculateFreshness(AgreementNone, 0.50, volatility)
			claim.Verdict = v
			claim.FreshnessScore = s
			continue
		}

		// Check credit allowance
		if !e.budget.AllowSearch(1) {
			v, s := CalculateFreshness(AgreementNone, 0.50, volatility)
			claim.Verdict = v
			claim.FreshnessScore = s
			continue
		}

		// Execute verification with single-flight dedupe
		claimKey := ComputeClaimKey(claim.Category, claim.Entity, claim.Attribute, claim.ValueInAnswer)
		verifiedClaim, err := e.cache.DoSingleFlight(claimKey, func() (Claim, error) {
			searchesPerformed++
			creditsSpent++

			res, vErr := e.verifier.Verify(ctx, *claim)
			if vErr != nil {
				e.breaker.RecordFailure()
				v, s := CalculateFreshness(AgreementNone, 0.50, volatility)
				claim.Verdict = v
				claim.FreshnessScore = s
				return *claim, nil
			}

			e.breaker.RecordSuccess()
			v, s := CalculateFreshness(res.Agreement, res.Authority, volatility)
			claim.Verdict = v
			claim.FreshnessScore = s
			claim.CorrectedValue = res.CorrectedValue
			claim.Evidence = res.Evidence
			return *claim, nil
		})

		if err == nil {
			claim.Verdict = verifiedClaim.Verdict
			claim.FreshnessScore = verifiedClaim.FreshnessScore
			claim.CorrectedValue = verifiedClaim.CorrectedValue
			claim.Evidence = verifiedClaim.Evidence
		}

		// Store verified claim in cache
		e.cache.Set(*claim)
	}

	record.Claims = claims
	record.Stats.Searches = searchesPerformed
	record.Stats.CreditsSpent = creditsSpent
	record.Stats.LatencyMS = time.Since(startTime).Milliseconds()

	return record, nil
}

func (e *Engine) getKnowledgeCutoff(model string) time.Time {
	cleanModel := strings.ToLower(model)
	if e.config.KnowledgeCutoffs != nil {
		for k, cutoffStr := range e.config.KnowledgeCutoffs {
			if strings.Contains(cleanModel, strings.ToLower(k)) {
				if t, err := time.Parse("2006-01-02", cutoffStr); err == nil {
					return t
				}
			}
		}
	}
	// Fallback cutoff: 1 year prior to today
	return time.Now().AddDate(-1, 0, 0)
}

// Cache returns the underlying claim cache.
func (e *Engine) Cache() *ClaimCache {
	return e.cache
}

// Breaker returns the underlying circuit breaker.
func (e *Engine) Breaker() *CircuitBreaker {
	return e.breaker
}

// Budget returns the budget controller.
func (e *Engine) Budget() *BudgetController {
	return e.budget
}

