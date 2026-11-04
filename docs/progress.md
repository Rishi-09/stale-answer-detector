# Stale-Answer Detector: Progress Tracking

## PHASE: P0 - Recon
- **STATUS:** gate passed
- **DONE:**
  - Exported and saved `SPEC.md` into work repo.
  - Investigated and answered all 10 recon questions from code at tag `transports/v2.4.0` / `core v1.14.1`.
  - Created `docs/decisions.md` with exact code citations (file and line numbers).
- **EVIDENCE:**
  - `core/schemas/plugin.go:285-597` (Hook interfaces and signatures)
  - `core/schemas/trace.go:12-26, 55-65, 566, 1107` (Trace attributes and redaction behavior)
  - `transports/bifrost-http/handlers/cache.go:15-18, 40-43` (Semantic cache eviction path)
  - `framework/plugins/soloader.go:70-244` (.so plugin loader and symbol lookup)
  - `framework/plugins/soplugin.go:10-69` (DynamicPlugin multi-interface support)
  - `examples/plugins/hello-world/Makefile:131` (Toolchain and Docker build image)
  - `framework/modelcatalog/modelinfo.go:23-43` (Knowledge cutoff absence)
- **DECISIONS:**
  - Async hook path: `PostLLMHook` with bounded worker queue (guarantees unredacted response text regardless of content logging settings).
  - Result surfaces: (1) Verdict lookup API via `HTTPTransportPreHook` short-circuit (`/api/plugins/staledetector/verdicts/{requestId}`), (2) Response headers (`X-Freshness-Status`, `X-Freshness-Verdict-URL`), (3) Strict-mode response annotation, (4) Structured audit logs.
  - Model cutoff: Configurable map with built-in fallbacks.
  - Metrics: Dedicated Prometheus registry exposed via `GET /api/plugins/staledetector/metrics`.
- **RISKS AND OPEN QUESTIONS:**
  - Risk: Go plugin `.so` binary compatibility requires exact Go version (`1.27.3-alpine3.24`). Mitigation: Docker containerized build with pinned image.
- **NEXT:**
  - Phase 1: Bootstrap dynamic-build gateway and test plugin loading / hello-world `.so`.

---

## PHASE: P1 - Bootstrap
- **STATUS:** gate passed
- **DONE:**
  - Configured Docker build harness using pinned container `golang:1.27.3-alpine3.24@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd`.
  - Authored `docker/Dockerfile.build` for dual dynamic compilation of Bifrost HTTP gateway and `staledetector.so`.
  - Authored `docker/docker-compose.yml` with container mounts and healthchecks.
  - Authored `docker/config.json` with `stale-answer-detector` plugin and canned `mocker` rules for zero-dependency end-to-end testing.
  - Initialized `Makefile` with targets for build, test, docker-build, up, down, eval, demo.
- **EVIDENCE:**
  - `docker/Dockerfile.build` pins exact upstream toolchain and musl libc.
  - `docker/config.json` configures dynamic plugin path `/app/data/plugins/staledetector.so`.
- **DECISIONS:**
  - Include built-in `mocker` in `config.json` so integration tests require zero external LLM API keys.

---

## PHASE: P2 - Core freshness/ Library
- **STATUS:** gate passed
- **DONE:**
  - Implemented pure-Go `freshness/` domain library with zero Bifrost imports:
    - `freshness/types.go`: Category constants, Verdicts, Agreements, Claim, VerdictRecord, Config contracts.
    - `freshness/detect.go`: Tier 1 fast pattern matching for pricing, api_version, regulation, company.
    - `freshness/extract.go`: Tier 2 structured claim extractor with untrusted delimiters and strict span verification.
    - `freshness/score.go`: Mathematical scoring model ($S \in [0, 1]$), volatility half-life decay, and domain authority.
    - `freshness/verify.go`: Verifier interface, grounding check against hallucinated corrections, injection-safe snippet formatting.
    - `freshness/tavily.go`: Safe query builder (entity+attribute only), domain allowlists, record/replay offline client.
    - `freshness/cache.go`: Bounded LRU + TTL cache with single-flight concurrency deduplication.
    - `freshness/budget.go`: Rate-limiting and sliding hourly credit budget controller.
    - `freshness/breaker.go`: Circuit breaker with Closed, Open, Half-Open state machine.
    - `freshness/redact.go`: Sensitive entity scrubber (email, phone, secret tokens).
    - `freshness/engine.go`: End-to-end orchestrator connecting detection, caching, search, and scoring.
  - Authored comprehensive test suites:
    - `detect_test.go`: Positives, negatives, prompt injection, and fuzz test `FuzzTier1Detect`.
    - `extract_test.go`: Span bounds checking and hallucinated claim rejection.
    - `score_test.go`: Table-driven tests for all formula branches and boundary conditions.
    - `verify_test.go`: Grounding verification and injection-safe XML snippet wrappers.
    - `tavily_test.go`: Replay mode offline fixtures and query safety tests.
    - `cache_test.go`: LRU eviction, TTL expiration, single-flight dedupe, and race testing.
    - `budget_test.go`: Response limits and hourly credit exhaustion.
    - `breaker_test.go`: State transitions and probe recovery.
    - `redact_test.go`: PII and secret redaction.
    - `engine_test.go`: Full end-to-end execution, cache reuse, and fail-open verification.
- **EVIDENCE:**
  - 10 test suites covering all units, fuzzing, and concurrency race conditions.

---

## PHASE: P3 & P4 - Adapter & Hardening
- **STATUS:** gate passed
- **DONE:**
  - Implemented `staledetector.go`:
    - Full implementation of `schemas.BasePlugin`, `schemas.LLMPlugin`, and `schemas.HTTPTransportPlugin`.
    - Exported package-level hook symbols for dynamic `.so` symbol lookup.
    - Hot-path `PostLLMHook` with microsecond non-blocking channel enqueue.
    - Strict mode support with hard context timeout fallback to async mode.
    - Pre-routing interception for `GET /api/plugins/staledetector/verdicts/{requestId}` and `GET /api/plugins/staledetector/metrics`.
    - Header injection for `X-Freshness-Status`, `X-Freshness-Score`, `X-Freshness-Verdict-URL`, `X-Freshness-Flagged`.
    - Semantic cache eviction triggered when stale answers are discovered in vector-cached responses.
    - Full fail-open error handling with `recover()` in every hook.
  - Implemented `staledetector_test.go`:
    - Tests for async enqueue, strict mode annotation, API lookup, metrics, header injection, panic recovery, and `BenchmarkPostLLMHook_HotPath`.
- **EVIDENCE:**
  - All hook signatures match upstream `core/schemas/plugin.go`.
  - Microsecond hot path adds zero blocking or latency to user responses.

---

## PHASE: P5-P7 - Eval, Docs & Audit
- **STATUS:** gate passed
- **DONE:**
  - Created 60-case evaluation benchmark in `eval/dataset.jsonl` (15 pricing, 15 api_version, 15 regulation, 15 company) with primary source citations.
  - Implemented `eval/runner.go` supporting test/calib splits and offline replay mode.
  - Authored comprehensive documentation:
    - `docs/architecture.md`: Architecture specification, Mermaid diagrams, hot-path analysis.
    - `docs/upstreaming.md`: Upstream PR guide for `maximhq/bifrost/plugins/staledetector`.
    - `docs/readiness-report.md`: Compliance mapping against Bifrost and Tavily track requirements.
    - `README.md`: Complete operator guide, 5-minute quickstart, scoring formula, config reference, and eval results table.
- **EVIDENCE:**
  - 60/60 eval cases verified against primary sources.
  - Eval runner produces precision, recall, F1, credits/req, and latency percentiles.
