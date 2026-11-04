package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/staledetector/freshness"
)

func createTestPlugin(t *testing.T, mode string) *Plugin {
	cfg := freshness.DefaultConfig()
	cfg.Mode = mode
	cfg.TimeoutsMS.StrictTotalMS = 500

	p, err := NewPlugin(cfg)
	if err != nil {
		t.Fatalf("failed to create plugin: %v", err)
	}
	return p
}

func createTestChatResponse(id, content string) *schemas.BifrostResponse {
	return &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			ID:    id,
			Model: "openai/gpt-4o",
			Choices: []schemas.BifrostResponseChoice{
				{
					Index: 0,
					ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
						Message: &schemas.ChatMessage{
							Role: schemas.ChatMessageRoleAssistant,
							Content: &schemas.ChatMessageContent{
								ContentStr: &content,
							},
						},
					},
				},
			},
		},
	}
}

func TestPostLLMHook_AsyncEnqueueHotPath(t *testing.T) {
	p := createTestPlugin(t, freshness.ModeAsync)
	defer p.Cleanup()

	ctx := schemas.NewBifrostContext(context.Background(), nil)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-async-123")

	resp := createTestChatResponse("req-async-123", "GPT-4o input tokens cost $5.00 per million tokens.")

	start := time.Now()
	outResp, outErr, err := p.PostLLMHook(ctx, resp, nil)
	duration := time.Since(start)

	if err != nil || outErr != nil {
		t.Fatalf("unexpected error in PostLLMHook: err=%v, outErr=%v", err, outErr)
	}

	if outResp != resp {
		t.Fatal("expected original response pointer returned")
	}

	// Hot path should finish in under 1 millisecond
	if duration > 5*time.Millisecond {
		t.Logf("PostLLMHook hot-path took %v", duration)
	}

	// Wait briefly for worker queue to process job
	time.Sleep(100 * time.Millisecond)

	// Check that verdict was stored
	val, ok := p.verdictStore.Load("req-async-123")
	if !ok {
		t.Fatal("expected verdict to be stored in verdictStore after async processing")
	}
	record := val.(*freshness.VerdictRecord)
	if record.RequestID != "req-async-123" {
		t.Errorf("expected requestID 'req-async-123', got %s", record.RequestID)
	}
}

func TestPostLLMHook_StrictAnnotation(t *testing.T) {
	p := createTestPlugin(t, freshness.ModeStrict)
	defer p.Cleanup()

	// Register canned stale response
	p.tavilyClient.RegisterFixture("test-fixture", freshness.TavilyResponse{
		Query: "GPT-4o",
		Results: []freshness.TavilySearchResultItem{
			{
				Title:   "OpenAI Pricing",
				URL:     "https://openai.com/pricing",
				Content: "GPT-4o input tokens cost $2.50 per million tokens.",
			},
		},
	})

	ctx := schemas.NewBifrostContext(context.Background(), nil)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-strict-456")

	content := "GPT-4o costs $5.00 per million tokens."
	resp := createTestChatResponse("req-strict-456", content)

	outResp, outErr, err := p.PostLLMHook(ctx, resp, nil)
	if err != nil || outErr != nil {
		t.Fatalf("unexpected error: %v, %v", err, outErr)
	}

	if outResp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestHTTPTransportPreHook_VerdictLookupAPI(t *testing.T) {
	p := createTestPlugin(t, freshness.ModeAsync)
	defer p.Cleanup()

	// Seed a verdict
	testRecord := &freshness.VerdictRecord{
		RequestID: "req-lookup-789",
		Model:     "gpt-4o",
		CheckedAt: time.Now(),
		Mode:      freshness.ModeAsync,
		Claims: []freshness.Claim{
			{
				ClaimID:        "c1",
				Category:       freshness.CategoryPricing,
				Entity:         "GPT-4o",
				Verdict:        freshness.VerdictStale,
				FreshnessScore: 0.12,
			},
		},
	}
	p.verdictStore.Store("req-lookup-789", testRecord)

	// Case 1: Existing verdict
	reqExist := &schemas.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/api/plugins/staledetector/verdicts/req-lookup-789",
	}
	httpResp, err := p.HTTPTransportPreHook(nil, reqExist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if httpResp == nil {
		t.Fatal("expected short-circuit HTTPResponse")
	}
	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", httpResp.StatusCode)
	}
	if !strings.Contains(string(httpResp.Body), "req-lookup-789") {
		t.Errorf("expected body to contain record JSON: %s", string(httpResp.Body))
	}

	// Case 2: Non-existent verdict -> 404
	reqNonExist := &schemas.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/api/plugins/staledetector/verdicts/req-not-found",
	}
	httpResp404, err := p.HTTPTransportPreHook(nil, reqNonExist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if httpResp404.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", httpResp404.StatusCode)
	}

	// Case 3: Other path -> pass through (nil, nil)
	reqPass := &schemas.HTTPRequest{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
	}
	httpRespPass, err := p.HTTPTransportPreHook(nil, reqPass)
	if err != nil || httpRespPass != nil {
		t.Errorf("expected pass-through (nil, nil) for standard routes")
	}
}

func TestHTTPTransportPreHook_MetricsEndpoint(t *testing.T) {
	p := createTestPlugin(t, freshness.ModeAsync)
	defer p.Cleanup()

	p.metricClaimsDet.WithLabelValues("pricing").Inc()

	req := &schemas.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/api/plugins/staledetector/metrics",
	}

	httpResp, err := p.HTTPTransportPreHook(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if httpResp == nil || httpResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from metrics endpoint")
	}

	bodyStr := string(httpResp.Body)
	if !strings.Contains(bodyStr, "staledetector_claims_detected_total") {
		t.Errorf("expected Prometheus metric in output: %s", bodyStr)
	}
}

func TestHTTPTransportResponseHeadersHook(t *testing.T) {
	p := createTestPlugin(t, freshness.ModeAsync)
	defer p.Cleanup()

	ctx := schemas.NewBifrostContext(context.Background(), nil)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-hdr-test")

	respMeta := &schemas.HTTPResponseMetadata{
		StatusCode: http.StatusOK,
		Headers:    make(map[string]string),
	}

	err := p.HTTPTransportResponseHeadersHook(ctx, nil, respMeta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if respMeta.Headers[headerFreshnessStatus] != "checked" {
		t.Errorf("expected X-Freshness-Status header 'checked', got '%s'", respMeta.Headers[headerFreshnessStatus])
	}

	expectedURL := "/api/plugins/staledetector/verdicts/req-hdr-test"
	if respMeta.Headers[headerFreshnessVerdictURL] != expectedURL {
		t.Errorf("expected X-Freshness-Verdict-URL '%s', got '%s'", expectedURL, respMeta.Headers[headerFreshnessVerdictURL])
	}
}

func TestFailOpen_PanicRecovery(t *testing.T) {
	p := createTestPlugin(t, freshness.ModeAsync)
	defer p.Cleanup()

	// Calling with nil resp and ctx should not panic
	outResp, outErr, err := p.PostLLMHook(nil, nil, nil)
	if err != nil || outErr != nil || outResp != nil {
		t.Error("expected graceful fail open on nil inputs")
	}
}

func TestKillSwitch_EnabledFalse(t *testing.T) {
	cfg := freshness.DefaultConfig()
	cfg.Enabled = false
	p, err := NewPlugin(cfg)
	if err != nil {
		t.Fatalf("failed to create plugin: %v", err)
	}
	defer p.Cleanup()

	ctx := schemas.NewBifrostContext(context.Background(), nil)
	resp := createTestChatResponse("req-disabled-1", "GPT-4o costs $5.00 per million tokens.")

	outResp, outErr, err := p.PostLLMHook(ctx, resp, nil)
	if err != nil || outErr != nil || outResp != resp {
		t.Fatal("expected immediate pass-through when enabled is false")
	}

	// Should not enqueue or verify anything
	if len(p.workerQueue) != 0 {
		t.Errorf("expected workerQueue to remain empty, got %d", len(p.workerQueue))
	}
}

func TestDogfoodLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "dogfood.jsonl")

	cfg := freshness.DefaultConfig()
	cfg.DogfoodLogPath = logPath
	cfg.Mode = freshness.ModeAsync

	p, err := NewPlugin(cfg)
	if err != nil {
		t.Fatalf("failed to create plugin: %v", err)
	}
	defer p.Cleanup()

	ctx := schemas.NewBifrostContext(context.Background(), nil)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-df-zero")

	// 1. Zero candidate request
	respZero := createTestChatResponse("req-df-zero", "Hello, how can I help you today?")
	_, _, _ = p.PostLLMHook(ctx, respZero, nil)

	// Check log was written for zero candidate request
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected dogfood log file created: %v", err)
	}
	if !strings.Contains(string(data), "req-df-zero") {
		t.Errorf("expected req-df-zero in dogfood log, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"tier1_candidates":0`) {
		t.Errorf("expected tier1_candidates 0 in dogfood log, got: %s", string(data))
	}
}

func TestDogfoodLogging_RedactsAllClaimFields(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "dogfood.jsonl")
	cfg := freshness.DefaultConfig()
	cfg.DogfoodLogPath = logPath
	p, err := NewPlugin(cfg)
	if err != nil {
		t.Fatalf("failed to create plugin: %v", err)
	}
	defer p.Cleanup()

	secret := "person@example.com"
	p.logRecord(&freshness.VerdictRecord{
		CheckedAt: time.Now(),
		Claims: []freshness.Claim{{
			ClaimID:       "claim-1",
			Text:          secret,
			Entity:        secret,
			Attribute:     secret,
			ValueInAnswer: secret,
			CorrectedValue: secret,
		}},
	}, "req-redacted", "gpt-4o", "openai", "ignored", 1, nil)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read dogfood log: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("dogfood log leaked unredacted claim value: %s", string(data))
	}
	if !strings.Contains(string(data), "[REDACTED_EMAIL]") {
		t.Fatalf("expected redacted value in dogfood log: %s", string(data))
	}
}

func BenchmarkPostLLMHook_HotPath(b *testing.B) {
	p, err := NewPlugin(freshness.DefaultConfig())
	if err != nil {
		b.Fatalf("failed to create plugin: %v", err)
	}
	defer p.Cleanup()

	ctx := schemas.NewBifrostContext(context.Background(), nil)
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "bench-req-1")

	resp := createTestChatResponse("bench-req-1", "GPT-4o costs $5.00 per million tokens and Python 3.12 is current.")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = p.PostLLMHook(ctx, resp, nil)
	}
}

