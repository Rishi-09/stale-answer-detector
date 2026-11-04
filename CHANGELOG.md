# Changelog

All notable changes to `stale-answer-detector` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-11-04

### Added
- Pure-Go `freshness` engine with zero Bifrost core imports:
  - Tier 1 regex and pattern matcher for candidate detection across 4 categories (`pricing`, `api_version`, `regulation`, `company`).
  - Tier 2 LLM claim extractor with strict JSON schema and source span verification.
  - Volatility-aware scoring model evaluating category half-life, cutoff gap, and source authority.
  - Pluggable `Verifier` interface with Tavily live search implementation and record/replay mode.
  - Bounded LRU + TTL claim cache with single-flight deduplication.
  - Rate limiting, sliding-window credit caps, and circuit breaker.
  - Sensitive entity and secret redactor protecting search queries and payloads.
- Bifrost Gateway plugin adapter (`staledetector`):
  - `PostLLMHook` implementation with microsecond hot-path enqueue for unary and accumulated stream responses.
  - Strict mode support with hard context timeout fallback to async mode.
  - Pre-routing interception for verdict lookup (`GET /api/plugins/staledetector/verdicts/{requestId}`) and metrics (`GET /api/plugins/staledetector/metrics`).
  - Header injection (`X-Freshness-Status`, `X-Freshness-Verdict-URL`).
  - Semantic cache invalidation when stale cached answers are discovered.
  - Full fail-open error handling and panic recovery.
- Docker dynamic build harness (`golang:1.27.3-alpine3.24`) and docker-compose deployment.
- 60-case evaluation benchmark dataset and runner.

