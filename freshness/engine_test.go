package freshness

import (
	"context"
	"strings"
	"testing"
)

func TestEngine_ProcessEndToEnd(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeAsync

	extractor := NewLocalExtractor()
	verifier := NewLocalDeterministicVerifier()

	// Register canned staleness result for GPT-4o pricing
	verifier.RegisterCanned("GPT-4o:price per unit", VerificationResult{
		Agreement:      AgreementMismatch,
		Authority:      0.95,
		CorrectedValue: "$2.50 per million",
		Evidence: []Evidence{
			{
				URL:       "https://openai.com/pricing",
				Title:     "OpenAI Pricing",
				Snippet:   "GPT-4o standard input price is $2.50 per million tokens.",
				Authority: 0.95,
			},
		},
	})

	engine := NewEngine(cfg, extractor, verifier)

	text := "As of today, GPT-4o costs $5.00 per million tokens for input."
	record, err := engine.Process(context.Background(), "req-test-123", "openai/gpt-4o", text)
	if err != nil {
		t.Fatalf("unexpected engine process error: %v", err)
	}

	if record.RequestID != "req-test-123" {
		t.Errorf("expected request ID 'req-test-123', got '%s'", record.RequestID)
	}

	if len(record.Claims) == 0 {
		t.Fatal("expected at least 1 verified claim")
	}

	claim := record.Claims[0]
	if claim.Category != CategoryPricing {
		t.Errorf("expected pricing category, got %s", claim.Category)
	}

	if claim.Verdict != VerdictStale {
		t.Errorf("expected verdict 'stale', got '%s'", claim.Verdict)
	}

	if claim.FreshnessScore >= 0.50 {
		t.Errorf("expected stale claim score to be low (<0.50), got %.3f", claim.FreshnessScore)
	}

	if !strings.Contains(claim.CorrectedValue, "$2.50") {
		t.Errorf("expected corrected value '$2.50', got '%s'", claim.CorrectedValue)
	}

	// Process second time: should hit cache!
	record2, err := engine.Process(context.Background(), "req-test-456", "openai/gpt-4o", text)
	if err != nil {
		t.Fatalf("unexpected second process error: %v", err)
	}

	if len(record2.Claims) == 0 || !record2.Claims[0].FromCache {
		t.Error("expected second run to serve claim from cache (FromCache=true)")
	}
}

func TestEngine_ZeroCandidatesEarlyExit(t *testing.T) {
	cfg := DefaultConfig()
	engine := NewEngine(cfg, nil, nil)

	plainProse := "The quick brown fox jumps over the lazy dog."
	record, err := engine.Process(context.Background(), "req-zero", "openai/gpt-4o", plainProse)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(record.Claims) != 0 {
		t.Errorf("expected 0 claims for non-factual text, got %d", len(record.Claims))
	}
	if record.Stats.Searches != 0 {
		t.Errorf("expected 0 searches, got %d", record.Stats.Searches)
	}
}
