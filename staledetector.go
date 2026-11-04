package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/staledetector/freshness"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

const (
	PluginName = "stale-answer-detector"

	headerFreshnessStatus     = "X-Freshness-Status"
	headerFreshnessScore      = "X-Freshness-Score"
	headerFreshnessVerdictURL = "X-Freshness-Verdict-URL"
	headerFreshnessFlagged    = "X-Freshness-Flagged"
)

type recursionKeyType struct{}

var recursionKey = recursionKeyType{}

// Default plugin singleton for dynamic .so loading
var defaultPlugin *Plugin

// verificationJob holds the workload passed from the hot path to async workers.
type verificationJob struct {
	requestID string
	model     string
	text      string
	cacheID   string
}

// Plugin implements Bifrost LLMPlugin and HTTPTransportPlugin.
type Plugin struct {
	config       freshness.Config
	engine       *freshness.Engine
	tavilyClient *freshness.TavilyClient
	workerQueue  chan verificationJob
	workerWg     sync.WaitGroup
	stopSignal   chan struct{}
	stopped      atomic.Bool

	verdictStore sync.Map // map[string]*freshness.VerdictRecord
	seenRequests sync.Map // map[string]struct{} (deduplication across fallbacks)

	// Prometheus Metrics
	registry            *prometheus.Registry
	metricClaimsDet     *prometheus.CounterVec
	metricClaimsVer     *prometheus.CounterVec
	metricSearches      *prometheus.CounterVec
	metricCredits       prometheus.Counter
	metricCacheHits     prometheus.Counter
	metricDroppedJobs   prometheus.Counter
	metricLatency       prometheus.Histogram
	metricQueueDepth    prometheus.Gauge
	metricCircuitBreaker prometheus.Gauge
}

// NewPlugin constructs an in-repo or testable instance of Plugin.
func NewPlugin(cfg freshness.Config) (*Plugin, error) {
	reg := prometheus.NewRegistry()

	claimsDet := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staledetector_claims_detected_total",
		Help: "Total number of candidate claims identified in responses",
	}, []string{"category"})

	claimsVer := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staledetector_claims_verified_total",
		Help: "Total number of claims verified by verdict and category",
	}, []string{"category", "verdict"})

	searches := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staledetector_searches_total",
		Help: "Total number of web verification queries dispatched",
	}, []string{"category"})

	credits := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staledetector_credits_spent_total",
		Help: "Total Tavily search credits consumed",
	})

	cacheHits := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staledetector_cache_hits_total",
		Help: "Total verified claims resolved from local cache",
	})

	droppedJobs := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staledetector_dropped_jobs_total",
		Help: "Total verification jobs dropped due to queue saturation",
	})

	latency := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "staledetector_verification_duration_seconds",
		Help:    "End-to-end verification latency in seconds",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.0, 4.0, 8.0},
	})

	queueDepth := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "staledetector_queue_depth",
		Help: "Current pending verification jobs in worker channel",
	})

	cbState := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "staledetector_circuit_state",
		Help: "Current state of circuit breaker (0=closed, 1=half-open, 2=open)",
	})

	reg.MustRegister(claimsDet, claimsVer, searches, credits, cacheHits, droppedJobs, latency, queueDepth, cbState)

	tavilyClient := freshness.NewTavilyClient(
		cfg.TavilyAPIKey,
		cfg.DomainAllowlist,
		time.Duration(cfg.TimeoutsMS.VerifyMS)*time.Millisecond,
	)

	engine := freshness.NewEngine(cfg, freshness.NewLocalExtractor(), tavilyClient)

	queueSize := cfg.Limits.QueueSize
	if queueSize <= 0 {
		queueSize = 256
	}
	numWorkers := cfg.Limits.Workers
	if numWorkers <= 0 {
		numWorkers = 4
	}

	p := &Plugin{
		config:               cfg,
		engine:               engine,
		tavilyClient:         tavilyClient,
		workerQueue:          make(chan verificationJob, queueSize),
		stopSignal:           make(chan struct{}),
		registry:             reg,
		metricClaimsDet:      claimsDet,
		metricClaimsVer:      claimsVer,
		metricSearches:       searches,
		metricCredits:        credits,
		metricCacheHits:      cacheHits,
		metricDroppedJobs:    droppedJobs,
		metricLatency:        latency,
		metricQueueDepth:     queueDepth,
		metricCircuitBreaker: cbState,
	}

	// Start worker pool
	for i := 0; i < numWorkers; i++ {
		p.workerWg.Add(1)
		go p.workerLoop(i)
	}

	return p, nil
}

// Init initializes the plugin from configuration JSON/map.
func Init(config any) error {
	cfg := freshness.DefaultConfig()

	if config != nil {
		raw, err := json.Marshal(config)
		if err == nil {
			_ = json.Unmarshal(raw, &cfg)
		}
	}

	p, err := NewPlugin(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize staledetector plugin: %w", err)
	}

	defaultPlugin = p
	return nil
}

// GetName returns the plugin identifier.
func GetName() string {
	return PluginName
}

func (p *Plugin) GetName() string {
	return PluginName
}

// Cleanup gracefully shuts down workers and releases resources.
func Cleanup() error {
	if defaultPlugin != nil {
		return defaultPlugin.Cleanup()
	}
	return nil
}

func (p *Plugin) Cleanup() error {
	if p.stopped.Swap(true) {
		return nil
	}

	close(p.stopSignal)
	close(p.workerQueue)
	p.workerWg.Wait()
	return nil
}

func (p *Plugin) workerLoop(_ int) {
	defer p.workerWg.Done()

	for {
		select {
		case <-p.stopSignal:
			return
		case job, ok := <-p.workerQueue:
			if !ok {
				return
			}
			p.processAsyncJob(job)
			p.metricQueueDepth.Set(float64(len(p.workerQueue)))
		}
	}
}

func (p *Plugin) processAsyncJob(job verificationJob) {
	defer func() {
		_ = recover()
	}()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.config.TimeoutsMS.VerifyMS*2)*time.Millisecond)
	defer cancel()

	ctx = context.WithValue(ctx, recursionKey, true)

	record, err := p.engine.Process(ctx, job.requestID, job.model, job.text)
	if err != nil || record == nil {
		return
	}

	// Update telemetry
	dur := time.Since(start).Seconds()
	p.metricLatency.Observe(dur)

	for _, clm := range record.Claims {
		p.metricClaimsDet.WithLabelValues(clm.Category).Inc()
		p.metricClaimsVer.WithLabelValues(clm.Category, clm.Verdict).Inc()
		if clm.FromCache {
			p.metricCacheHits.Inc()
		}
	}
	p.metricCredits.Add(float64(record.Stats.CreditsSpent))

	// Store record for API lookup
	p.verdictStore.Store(job.requestID, record)

	// Check if any stale claim was found in a cached answer (Semantic Cache Awareness)
	if job.cacheID != "" {
		hasStale := false
		for _, clm := range record.Claims {
			if clm.Verdict == freshness.VerdictStale {
				hasStale = true
				break
			}
		}
		if hasStale {
			p.evictSemanticCache(job.cacheID)
		}
	}
}

func (p *Plugin) evictSemanticCache(cacheID string) {
	// Calls supported eviction route DELETE /api/cache/clear/{cacheId}
	go func() {
		defer func() { _ = recover() }()
		req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://localhost:8080/api/cache/clear/%s", cacheID), nil)
		if err == nil {
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}
	}()
}

// LLMPlugin Methods

func (p *Plugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}

func (p *Plugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}

// PostLLMHook is the primary ingestion hook.
// It executes Tier 1 regex detection and non-blocking job enqueue in microseconds.
func (p *Plugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	defer func() {
		_ = recover() // Fail-open: never propagate hook panics
	}()

	// Recursion guard: skip internal extractor/comparator calls
	if ctx != nil && ctx.Value(recursionKey) != nil {
		return resp, bifrostErr, nil
	}

	// Skip error responses
	if resp == nil || bifrostErr != nil {
		return resp, bifrostErr, nil
	}

	// Extract response text and metadata
	responseText, model, reqID, cacheID := extractResponseInfo(ctx, resp)
	if len(responseText) == 0 {
		return resp, bifrostErr, nil
	}

	// Fallback deduplication: do not verify identical request twice
	dedupeKey := reqID + ":" + model
	if _, alreadySeen := p.seenRequests.LoadOrStore(dedupeKey, struct{}{}); alreadySeen {
		return resp, bifrostErr, nil
	}

	// Tier 1 Fast Match Check: microsecond rejection if zero candidates
	if !freshness.HasCandidates(responseText, p.config.Limits.MaxResponseChars) {
		return resp, bifrostErr, nil
	}

	// Operational branch: Strict mode vs Async mode
	if p.config.Mode == freshness.ModeStrict {
		// Strict mode: evaluate synchronously within deadline
		strictTimeout := time.Duration(p.config.TimeoutsMS.StrictTotalMS) * time.Millisecond
		if strictTimeout <= 0 {
			strictTimeout = 3 * time.Second
		}

		strictCtx, cancel := context.WithTimeout(context.Background(), strictTimeout)
		defer cancel()
		strictCtx = context.WithValue(strictCtx, recursionKey, true)

		record, err := p.engine.Process(strictCtx, reqID, model, responseText)
		if err == nil && record != nil {
			p.verdictStore.Store(reqID, record)
			p.annotateStrictResponse(resp, record)
			return resp, bifrostErr, nil
		}
		// On timeout or error in strict mode: fail open and fall through to async enqueue!
	}

	// Async mode (Default): Non-blocking enqueue to bounded queue
	job := verificationJob{
		requestID: reqID,
		model:     model,
		text:      responseText,
		cacheID:   cacheID,
	}

	select {
	case p.workerQueue <- job:
		p.metricQueueDepth.Set(float64(len(p.workerQueue)))
	default:
		// Queue full: drop job and record metric (fail open)
		p.metricDroppedJobs.Inc()
	}

	return resp, bifrostErr, nil
}

func (p *Plugin) annotateStrictResponse(resp *schemas.BifrostResponse, record *freshness.VerdictRecord) {
	if resp.ChatResponse == nil || len(resp.ChatResponse.Choices) == 0 {
		return
	}

	hasStale := false
	minScore := 1.0

	for _, c := range record.Claims {
		if c.FreshnessScore < minScore {
			minScore = c.FreshnessScore
		}
		if c.Verdict == freshness.VerdictStale {
			hasStale = true
		}
	}

	// Attach claim findings into choice annotations if flagged below threshold
	if hasStale && minScore < p.config.FlagBelow {
		choice := &resp.ChatResponse.Choices[0]
		if choice.ChatNonStreamResponseChoice != nil && choice.Message != nil && choice.Message.Content != nil && choice.Message.Content.ContentStr != nil {
			annotation := fmt.Sprintf("\n\n> [!WARNING] Freshness Notice: One or more claims in this answer may be outdated (Freshness score: %.2f).", minScore)
			*choice.Message.Content.ContentStr += annotation
		}
	}
}

func extractResponseInfo(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse) (text, model, reqID, cacheID string) {
	if resp.ChatResponse != nil {
		cr := resp.ChatResponse
		model = cr.Model
		reqID = cr.ID

		if cr.ExtraFields.CacheDebug != nil {
			cacheID = cr.ExtraFields.CacheDebug.CacheID
		}

		if len(cr.Choices) > 0 {
			choice := cr.Choices[0]
			if choice.ChatNonStreamResponseChoice != nil && choice.Message != nil && choice.Message.Content != nil {
				if choice.Message.Content.ContentStr != nil {
					text = *choice.Message.Content.ContentStr
				} else if len(choice.Message.Content.ContentBlocks) > 0 {
					var sb strings.Builder
					for _, b := range choice.Message.Content.ContentBlocks {
						if b.Text != nil {
							sb.WriteString(*b.Text)
						}
					}
					text = sb.String()
				}
			}
		}
	} else if resp.TextCompletionResponse != nil {
		tr := resp.TextCompletionResponse
		model = tr.Model
		reqID = tr.ID
		if len(tr.Choices) > 0 && tr.Choices[0].Text != nil {
			text = *tr.Choices[0].Text
		}
	}

	if reqID == "" && ctx != nil {
		if v := ctx.Value(schemas.BifrostContextKeyRequestID); v != nil {
			if s, ok := v.(string); ok {
				reqID = s
			}
		}
	}
	if reqID == "" {
		reqID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	return text, model, reqID, cacheID
}

// HTTPTransportPlugin Methods

func (p *Plugin) HTTPTransportPreAuthHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	return nil, nil
}

// HTTPTransportPreHook intercepts custom plugin routes for verdicts and metrics.
func (p *Plugin) HTTPTransportPreHook(_ *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	defer func() { _ = recover() }()

	if req == nil {
		return nil, nil
	}

	// 1. Verdict lookup API: GET /api/plugins/staledetector/verdicts/{requestId}
	if req.Method == http.MethodGet && strings.HasPrefix(req.Path, "/api/plugins/staledetector/verdicts/") {
		reqID := strings.TrimPrefix(req.Path, "/api/plugins/staledetector/verdicts/")
		if val, ok := p.verdictStore.Load(reqID); ok {
			record, _ := val.(*freshness.VerdictRecord)
			body, _ := json.MarshalIndent(record, "", "  ")
			return &schemas.HTTPResponse{
				StatusCode: http.StatusOK,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: body,
			}, nil
		}
		return &schemas.HTTPResponse{
			StatusCode: http.StatusNotFound,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: []byte(`{"error": "verdict not found or still processing"}`),
		}, nil
	}

	// 2. Metrics endpoint: GET /api/plugins/staledetector/metrics
	if req.Method == http.MethodGet && req.Path == "/api/plugins/staledetector/metrics" {
		mfs, err := p.registry.Gather()
		if err != nil {
			return &schemas.HTTPResponse{
				StatusCode: http.StatusInternalServerError,
				Body:       []byte(err.Error()),
			}, nil
		}
		var buf bytes.Buffer
		for _, mf := range mfs {
			_ = expfmt.MetricFamilyToText(&buf, mf)
		}
		return &schemas.HTTPResponse{
			StatusCode: http.StatusOK,
			Headers: map[string]string{
				"Content-Type": "text/plain; version=0.0.4",
			},
			Body: buf.Bytes(),
		}, nil
	}

	return nil, nil
}

func (p *Plugin) HTTPTransportPostHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, _ *schemas.HTTPResponse) error {
	return nil
}

// HTTPTransportResponseHeadersHook injects freshness verdict headers before committing HTTP response headers.
func (p *Plugin) HTTPTransportResponseHeadersHook(ctx *schemas.BifrostContext, _ *schemas.HTTPRequest, resp *schemas.HTTPResponseMetadata) error {
	defer func() { _ = recover() }()

	if resp == nil {
		return nil
	}

	reqID := ""
	if ctx != nil {
		if v := ctx.Value(schemas.BifrostContextKeyRequestID); v != nil {
			if s, ok := v.(string); ok {
				reqID = s
			}
		}
	}

	if reqID != "" {
		resp.SetHeader(headerFreshnessStatus, "checked")
		resp.SetHeader(headerFreshnessVerdictURL, "/api/plugins/staledetector/verdicts/"+reqID)

		if val, ok := p.verdictStore.Load(reqID); ok {
			if record, ok := val.(*freshness.VerdictRecord); ok && len(record.Claims) > 0 {
				minScore := 1.0
				hasStale := false
				for _, c := range record.Claims {
					if c.FreshnessScore < minScore {
						minScore = c.FreshnessScore
					}
					if c.Verdict == freshness.VerdictStale {
						hasStale = true
					}
				}
				resp.SetHeader(headerFreshnessScore, fmt.Sprintf("%.2f", minScore))
				if hasStale {
					resp.SetHeader(headerFreshnessFlagged, "true")
				}
			}
		}
	}

	return nil
}

func (p *Plugin) HTTPTransportStreamChunkHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	// Rule: verify accumulated stream at end in PostLLMHook, do not block per chunk
	return chunk, nil
}

// Package-level exported symbols for dynamic .so plugin loader

func PreRequestHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) error {
	if defaultPlugin != nil {
		return defaultPlugin.PreRequestHook(ctx, req)
	}
	return nil
}

func PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if defaultPlugin != nil {
		return defaultPlugin.PreLLMHook(ctx, req)
	}
	return req, nil, nil
}

func PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if defaultPlugin != nil {
		return defaultPlugin.PostLLMHook(ctx, resp, bifrostErr)
	}
	return resp, bifrostErr, nil
}

func HTTPTransportPreAuthHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	if defaultPlugin != nil {
		return defaultPlugin.HTTPTransportPreAuthHook(ctx, req)
	}
	return nil, nil
}

func HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	if defaultPlugin != nil {
		return defaultPlugin.HTTPTransportPreHook(ctx, req)
	}
	return nil, nil
}

func HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	if defaultPlugin != nil {
		return defaultPlugin.HTTPTransportPostHook(ctx, req, resp)
	}
	return nil
}

func HTTPTransportResponseHeadersHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponseMetadata) error {
	if defaultPlugin != nil {
		return defaultPlugin.HTTPTransportResponseHeadersHook(ctx, req, resp)
	}
	return nil
}

func HTTPTransportStreamChunkHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	if defaultPlugin != nil {
		return defaultPlugin.HTTPTransportStreamChunkHook(ctx, req, chunk)
	}
	return chunk, nil
}

