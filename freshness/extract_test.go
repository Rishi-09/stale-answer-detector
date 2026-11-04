package freshness

import (
	"context"
	"strings"
	"testing"
)

func TestExtract_SpanValidation(t *testing.T) {
	extractor := NewLocalExtractor()
	text := "OpenAI reduced GPT-4o input prices to $2.50 per million tokens in recent updates."
	candidates := DetectCandidates(text, 8000, nil)

	claims, err := extractor.Extract(context.Background(), text, candidates, 5)
	if err != nil {
		t.Fatalf("unexpected extract error: %v", err)
	}

	if len(claims) == 0 {
		t.Fatal("expected at least 1 extracted claim")
	}

	for _, clm := range claims {
		// Strict test: claim span and value MUST be contained in the source text
		if !ValidateClaimSpan(text, clm) {
			t.Fatalf("claim failed span validation: %+v", clm)
		}
		if clm.Span[0] < 0 || clm.Span[1] > len(text) {
			t.Fatalf("span out of bounds: [%d, %d] for text len %d", clm.Span[0], clm.Span[1], len(text))
		}
	}
}

func TestParseLLMExtraction_RejectsHallucinatedSpans(t *testing.T) {
	sourceText := "Anthropic released Claude 3.5 Sonnet in June 2024."

	// Model hallucinates a pricing claim that does not exist in the source text
	mockLLMOutput := `[
		{
			"category": "pricing",
			"entity": "Claude 3.5 Sonnet",
			"attribute": "price per million",
			"value": "$3.00 per million",
			"span": [100, 120]
		},
		{
			"category": "api_version",
			"entity": "Claude 3.5 Sonnet",
			"attribute": "release version",
			"value": "Claude 3.5 Sonnet",
			"span": [19, 36]
		}
	]`

	claims, err := ParseLLMExtraction(sourceText, mockLLMOutput, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The pricing claim should be rejected because "$3.00 per million" does not exist in sourceText
	if len(claims) != 1 {
		t.Fatalf("expected exactly 1 valid claim, got %d", len(claims))
	}
	if claims[0].Category != CategoryAPIVersion {
		t.Fatalf("expected api_version claim to be kept, got %s", claims[0].Category)
	}
}

func TestBuildExtractorPrompt_Delimiters(t *testing.T) {
	text := "Sample model response."
	prompt, err := BuildExtractorPrompt(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, "<untrusted_answer>") || !strings.Contains(prompt, "</untrusted_answer>") {
		t.Fatal("expected prompt to contain untrusted answer delimiters")
	}
}

