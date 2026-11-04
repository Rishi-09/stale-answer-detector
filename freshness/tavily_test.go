package freshness

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConstructQuery_SafeQueryConstruction(t *testing.T) {
	fullAnswer := "As an AI, I can tell you that in 2023 GPT-4 cost $30.00 per million. But now GPT-4o costs $5.00 per 1M tokens."
	claim := Claim{
		Category:      CategoryPricing,
		Entity:        "GPT-4o",
		Attribute:     "input price per 1M tokens",
		ValueInAnswer: "$5.00",
		Text:          fullAnswer,
	}

	query := ConstructQuery(claim)

	// Strict requirement: Never include full answer or prompt!
	if strings.Contains(query, "As an AI") || strings.Contains(query, "I can tell you") {
		t.Fatalf("query leaked surrounding answer prose: %s", query)
	}

	if !strings.Contains(query, "GPT-4o") || !strings.Contains(query, "pricing") {
		t.Fatalf("query missing core entity and category terms: %s", query)
	}
}

func TestTavilyClient_ReplayMode(t *testing.T) {
	client := NewTavilyClient("test-key", map[string][]string{
		CategoryPricing: {"openai.com"},
	}, 2*time.Second)

	client.SetReplayMode(true)

	// Register canned fixture
	fixtureKey := computeQueryHash("GPT-4o input price per 1M tokens pricing cost official", []string{"openai.com"}, "basic")
	client.RegisterFixture(fixtureKey, TavilyResponse{
		Query: "GPT-4o pricing",
		Results: []TavilySearchResultItem{
			{
				Title:         "OpenAI API Pricing",
				URL:           "https://openai.com/pricing",
				Content:       "GPT-4o input tokens cost $2.50 per million tokens.",
				Score:         0.95,
				PublishedDate: "2024-10-01",
			},
		},
	})

	claim := Claim{
		Category:  CategoryPricing,
		Entity:    "GPT-4o",
		Attribute: "input price per 1M tokens",
	}

	evidence, credits, err := client.Search(context.Background(), claim, "basic")
	if err != nil {
		t.Fatalf("search in replay mode failed: %v", err)
	}

	if len(evidence) == 0 {
		t.Fatal("expected evidence from replay fixture")
	}

	if credits != 1 {
		t.Errorf("expected 1 credit spent for basic search, got %d", credits)
	}

	if !strings.Contains(evidence[0].Snippet, "$2.50 per million") {
		t.Errorf("unexpected evidence snippet: %s", evidence[0].Snippet)
	}
}

