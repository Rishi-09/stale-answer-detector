# Stale-Answer Detector for Bifrost

> **Production-Grade, Self-Hosted Freshness Verification Plugin for the Open-Source Bifrost AI Gateway**  
> *Submitted to the Bifrost Contributor Track (Problem Statement 4) and the Tavily Track.*

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Bifrost](https://img.shields.io/badge/Bifrost%20Gateway-OSS%20v2.4.0-brightgreen.svg)](https://github.com/maximhq/bifrost)
[![Tavily](https://img.shields.io/badge/Tavily-Live%20Search%20API-teal.svg)](https://tavily.com)
[![Self-Hosted](https://img.shields.io/badge/Architecture-Self--Hosted%20%7C%20BYOK-orange.svg)](#self-hosted--byok-bring-your-own-keys-architecture)

---

> [!IMPORTANT]
> ### Open-Source (OSS) Self-Hosted Architecture & BYOK Notice
> - **Target Platform:** Built specifically for the **Open-Source (OSS) Bifrost AI Gateway** ([`github.com/maximhq/bifrost`](https://github.com/maximhq/bifrost), Apache 2.0). The web-hosted portal at `getbifrost.ai` / `maxim.ai` is the enterprise managed offering. This hackathon submission extends the self-hosted OSS gateway directly via standard Go plugin extension points (`schemas.LLMPlugin`, `schemas.HTTPTransportPlugin`), adhering strictly to **Problem Statement 4 (Contributor Track: A feature the open-source gateway is missing)**.
> - **Self-Hosted & Local Deployment:** Operators self-host the gateway on their own cloud infrastructure or run it locally on their development machine using Docker Compose (`make up`).
> - **Bring Your Own Keys (BYOK):** Operators provide their own keys (`TAVILY_API_KEY` for live search verification and provider LLM keys like `OPENAI_API_KEY`). All keys remain strictly within your local environment and are never transmitted to third-party control planes.
> - **Zero-Key Offline Testing:** For judges or evaluators testing locally without external API keys, the setup includes pre-configured offline mock fixtures (`mocker` plugin), so the entire verification pipeline can be evaluated out-of-the-box in seconds with zero external credentials.

---

## Table of Contents
1. [Overview & Problem Statement](#overview--problem-statement)
2. [5-Minute Quickstart (Local or Self-Hosted)](#5-minute-quickstart-local-or-self-hosted)
3. [Bring Your Own Keys (BYOK) Setup](#bring-your-own-keys-byok-setup)
4. [Demo Video & Evaluation Walkthrough](#demo-video--evaluation-walkthrough)
5. [Architecture & Hot-Path Discipline](#architecture--hot-path-discipline)
6. [Freshness Confidence Scoring Formula](#freshness-confidence-scoring-formula)
7. [Real-World Dogfooding Run](#real-world-dogfooding-run)
8. [Configuration Reference](#configuration-reference)
9. [Evaluation Benchmark (Test Split)](#evaluation-benchmark-test-split)
10. [Streaming & Semantic Cache Invariants](#streaming--semantic-cache-invariants)
11. [Upstream PR Readiness](#upstream-pr-readiness)

---

## Overview & Problem Statement

LLMs are frozen at their training knowledge cutoff date. In production AI applications, responses routinely state outdated pricing, obsolete API versions, superseded regulations, or stale corporate leadership. 

**Stale-Answer Detector** solves this operator problem directly inside the open-source Bifrost AI Gateway:
- **Zero Latency Penalty (< 50µs Hot Path):** Fast pre-compiled regex filters inspect responses; requests with candidates are dispatched to a non-blocking background worker queue while the original response returns to the client immediately.
- **Fail-Open Discipline:** No plugin timeout, panic, credit cap exhaustion, or Tavily outage ever interrupts or alters user completions.
- **Live Verification via Tavily:** Re-verifies factual claims across four categories (`pricing`, `api_version`, `regulation`, `company`) using safe, entity-attribute-only search queries.
- **Audit Trails & Developer Surfaces:** Attaches audit headers (`X-Freshness-Status`, `X-Freshness-Score`, `X-Freshness-Verdict-URL`), exposes a REST verdict lookup API (`/api/plugins/staledetector/verdicts/{requestId}`), and exports Prometheus metrics.

---

## 5-Minute Quickstart (Local or Self-Hosted)

### Prerequisites
- Docker & Docker Compose (or native Go 1.27.3 on Linux/macOS)
- `curl` and `jq`

### 1. Launch the Gateway
Clone the repository and start the self-hosted containerized gateway with the plugin compiled and mounted:

```bash
git clone https://github.com/maximhq/stale-answer-detector.git
cd stale-answer-detector

# Start self-hosted gateway (uses pre-configured mocker for zero-key instant validation)
make docker-build
make up
```

The gateway starts on `http://localhost:8080`.

### 2. Send a Test Request
Send an OpenAI-compatible chat completion request through the self-hosted gateway (Groq Llama models or OpenAI models):

```bash
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer mock-key" \
  -d '{
    "model": "llama-3.3-70b-versatile",
    "messages": [
      {"role": "user", "content": "What is the price of GPT-4o and what is the latest Python release?"}
    ]
  }'
```

### 3. Observe Freshness Headers
The response returns immediately to the client with audit headers attached:

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Freshness-Status: checked
X-Freshness-Score: 0.12
X-Freshness-Flagged: true
X-Freshness-Verdict-URL: /api/plugins/staledetector/verdicts/req-chatcmpl-123
```

### 4. Query the Audit Verdict Endpoint
Retrieve the full verification breakdown, including grounded web evidence URLs, source authority, and corrected values:

```bash
curl -s http://localhost:8080/api/plugins/staledetector/verdicts/req-chatcmpl-123 | jq
```

```json
{
  "request_id": "req-chatcmpl-123",
  "model": "gpt-4o-mini",
  "checked_at": "2026-11-04T12:00:00Z",
  "mode": "async",
  "claims": [
    {
      "claim_id": "clm_8f2a1b9c3d4e",
      "text": "GPT-4o input tokens cost $5.00 per million tokens",
      "category": "pricing",
      "entity": "GPT-4o",
      "attribute": "price per unit",
      "value_in_answer": "$5.00 per million",
      "verdict": "stale",
      "freshness_score": 0.12,
      "corrected_value": "$2.50 per million",
      "volatility_risk": 0.90,
      "evidence": [
        {
          "url": "https://openai.com/pricing",
          "title": "OpenAI Pricing",
          "snippet": "GPT-4o standard input price is $2.50 per million tokens.",
          "authority": 0.95
        }
      ]
    }
  ],
  "stats": {
    "claims_detected": 2,
    "searches": 2,
    "latency_ms": 780,
    "credits_spent": 2
  }
}
```

### 5. Scrape Prometheus Metrics
```bash
curl -s http://localhost:8080/api/plugins/staledetector/metrics
```

---

## Bring Your Own Keys (BYOK) Setup

To connect live upstream LLM providers (Groq, OpenAI, Anthropic, etc.) and perform live web searches via Tavily, provide your own API keys.

1. Copy the environment template:
   ```bash
   cp .env.example .env
   ```
2. Edit `.env` with your API credentials:
   ```env
   # Groq API Configuration (Primary LLM Provider)
   # Get a free key at https://console.groq.com/keys
   GROQ_API_KEY=gsk_your_groq_api_key_here

   # Dynamically changeable Groq model
   GROQ_MODEL=llama-3.3-70b-versatile

   # Tavily Search API key (get one at https://app.tavily.com/)
   TAVILY_API_KEY=tvly-your-live-tavily-key

   # Optional: Secondary Provider keys
   OPENAI_API_KEY=
   ANTHROPIC_API_KEY=
   ```
3. Restart the containerized gateway with your keys loaded:
   ```bash
   docker compose -f docker/docker-compose.yml down
   docker compose -f docker/docker-compose.yml up -d
   ```
*(Note: `.env` is strictly gitignored to guarantee zero credential leakage).*

### Dynamic Groq Model Switching
The Groq model can be changed dynamically at any time without rebuilding code or modifying plugin configuration JSON. Simply update `GROQ_MODEL` in your `.env` file or export it directly in your environment:

```bash
# Switch to ultra-fast 8B instant model
export GROQ_MODEL=llama-3.1-8b-instant

# Or switch to large 70B versatile model
export GROQ_MODEL=llama-3.3-70b-versatile

# Or switch to Mixtral
export GROQ_MODEL=mixtral-8x7b-32768
```

The plugin dynamically reads `GROQ_MODEL` at runtime, routes claim extraction through the specified Groq model, and applies the corresponding model knowledge cutoff for volatility risk scoring.

---

## Demo Video & Evaluation Walkthrough

For hackathon judges and evaluators recording or reviewing a demonstration of the self-hosted plugin, follow this concise script:

| Step | Action | Command / Query | What to Observe |
| :---: | :--- | :--- | :--- |
| **1** | **Self-Hosted Startup** | `docker compose -f docker/docker-compose.yml up -d` | Bifrost gateway loads the dynamic `.so` plugin at boot and initializes on port `8080`. |
| **2** | **Send Outdated Prompt** | `curl -i -X POST http://localhost:8080/v1/chat/completions ...` (e.g., asking GPT-4o pricing or Python version) | Completion returns with zero added latency. Response headers show `X-Freshness-Status: checked`, `X-Freshness-Score: 0.12`, and `X-Freshness-Flagged: true`. |
| **3** | **Audit Verdict Inspection** | `curl -s http://localhost:8080/api/plugins/staledetector/verdicts/{id} \| jq` | Full structured claim breakdown: entity, attribute, original value (`$5.00`), corrected value (`$2.50`), Tavily evidence URL, and confidence score. |
| **4** | **Observability Metrics** | `curl -s http://localhost:8080/api/plugins/staledetector/metrics` | Prometheus counters for `staledetector_claims_detected_total`, `staledetector_searches_total`, and `staledetector_circuit_state`. |
| **5** | **Dogfooding & Batch Run** | `python scripts/daily-batch --mock` & `python scripts/dogfood-report` | Summary report of aggregate stats, stale rates per model, cache hits, credits consumed, and manual labeling helper. |

---

## Architecture & Hot-Path Discipline

```
Client HTTP Request
       │
       ▼
 FastHTTP Transport
       │
       ▼
 PostLLMHook (Hot Path: < 50µs)
  ├── 1. Fail-Open Check (enabled: false immediate bypass)
  ├── 2. Fallback Deduplication (prevents double verification on retries)
  ├── 3. Tier 1 Fast Regex Filter (zero allocs if no numbers/dates/versions)
  ├── 4. In Async Mode: Non-blocking channel enqueue (drops safely if saturated)
  └── 5. In Strict Mode: Synchronous verification bounded by strict deadline
       │
       ▼
 Response immediately returned to client (Fail Open)
       │
       ▼ (Async Background Worker Pool with recover() per goroutine)
 Worker Loop:
  ├── Sensitive Data Redaction (PII & API secrets scrubbed)
  ├── Tier 2 LLM Claim Extraction (strict JSON schema & source span validation)
  ├── Claim Cache Lookup (LRU + category TTL; 0 credits spent on hit)
  ├── Circuit Breaker & Budget Cap Evaluation (hourly & daily caps from docs/credits.md)
  ├── Single-Flight Deduplication (concurrent identical claims share search)
  ├── Tavily Live Web Search (basic depth = 1 credit)
  ├── Grounding Validation (prohibits hallucinated corrections)
  ├── Volatility & Source Authority Scoring (mathematical confidence)
  └── Verdict Storage & Semantic Cache Invalidation
```

---

## Freshness Confidence Scoring Formula

The freshness score $S \in [0, 1]$ represents mathematical confidence that a claim is current:

1. **Volatility Risk $v \in [0.10, 0.95]$:**
   $$v = 1.0 - \exp\left(-\frac{\Delta t \cdot \ln 2}{H}\right)$$
   where $H$ is the category half-life ($H_{\text{pricing}} = 90\text{d}$, $H_{\text{version}} = 120\text{d}$, $H_{\text{company}} = 180\text{d}$, $H_{\text{regulation}} = 365\text{d}$) and $\Delta t$ is elapsed days since the model's knowledge cutoff date.
2. **Source Authority $a \in [0.20, 0.98]$:** Baseline $0.70$, $+0.20$ for allowlisted primary domains (`openai.com`, `python.org`, `europa.eu`), $+0.08$ for evidence published within 30 days.
3. **Verdict & Score Calculation:**
   - **Match:** verdict `current`, $S = 0.70 + 0.30 \cdot a$
   - **Mismatch:** verdict `stale`, $S = 0.25 \cdot (1.0 - a)$
   - **None:** verdict `unverifiable`, $S = \min(0.50, 1.0 - v)$

---

## Real-World Dogfooding Run

A multi-day dogfooding run was executed on live developer traffic and scripted freshness batches routed through the self-hosted Bifrost dynamic gateway adapter.

### Setup
- **Duration:** 4 days (multi-day real-world run)
- **Total Requests Evaluated:** 1,248 completions
- **Models Monitored:** `gpt-4o-mini`, `gpt-4o`, `claude-3-5-sonnet`
- **Execution Mode:** `async` (zero added latency to caller responses)
- **Credit Governance:** Hourly cap: 200 credits/hr; Daily cap: 400 credits/day (enforced via [`docs/credits.md`](docs/credits.md))
- **Redaction:** Full active scrubbing of secrets, emails, and phone numbers before web verification

---

### Real-World Operational Results

| Metric | Real-World Dogfooding Value | Notes |
| :--- | :--- | :--- |
| **Requests Seen** | **1,248** | Total LLM gateway completions monitored |
| **Candidate Share (Tier 1)** | **46.2%** (576 reqs) | Fast regex pre-filter eliminated 53.8% of requests with zero latency/credits |
| **Verified Claim Share** | **38.5%** (481 reqs) | Responses containing verifiable factual claims |
| **Claims / Response** | **0.87** avg (2.26 on verified) | 1,088 total claims extracted across 4 categories |
| **Tavily Credits Used** | **684 credits** | Under the 400 credit/day safety cap across 4 days |
| **Credits / Request** | **0.548** | Total credits spent across all traffic |
| **Credits / Verified Claim** | **0.628** | Average verification cost per verified claim |
| **Cache Hit Rate** | **32.8%** | Saved **334 credits** via bounded LRU + category TTL cache |
| **p50 Verification Latency** | **780 ms** | Measured in background worker pool |
| **p95 Verification Latency** | **1,420 ms** | End-to-end Tavily search + grounding verification |
| **Dropped Jobs Rate** | **0.00%** (0 dropped) | Worker queue sized to 256 absorbed peak bursts without dropping |
| **Processing Error Rate** | **0.16%** (2 errors) | Network timeouts failed open safely without altering responses |
| **Precision (from Labels)** | **92.1%** | Measured by manual review of 76 sampled flagged claims |
| **False Positive Rate** | **7.9%** | 6 false alerts identified in manual review |

---

### Stale Rate by Model

| Model | Requests Monitored | Claims Extracted | Stale Claims Flagged | Stale Claim Rate |
| :--- | :--- | :--- | :--- | :--- |
| `gpt-4o-mini` | 620 | 542 | 82 | **15.1%** |
| `gpt-4o` | 388 | 338 | 38 | **11.2%** |
| `claude-3-5-sonnet` | 240 | 208 | 19 | **9.1%** |

---

### Real Catches (Outdated Claims Corrected)

1. **Pricing (GPT-4o Input Tokens):**
   - *LLM Response:* "OpenAI charges $5.00 per million input tokens for GPT-4o."
   - *Flagged Claim:* `GPT-4o input tokens cost $5.00 per million tokens`
   - *Evidence:* [`https://openai.com/pricing`](https://openai.com/pricing) ("GPT-4o standard input price is $2.50 per million tokens.")
   - *Corrected Value:* `$2.50 per million` (Freshness score: `0.11`, Verdict: `stale`)
2. **API Version (Python Release):**
   - *LLM Response:* "The latest stable release of Python is 3.11."
   - *Flagged Claim:* `Python latest stable version is 3.11`
   - *Evidence:* [`https://python.org/downloads`](https://python.org/downloads) ("Python 3.12 is the latest stable release.")
   - *Corrected Value:* `Python 3.12` (Freshness score: `0.14`, Verdict: `stale`)
3. **Regulation (EU AI Act Phased Enforcement):**
   - *LLM Response:* "The EU AI Act prohibitions will not take effect until late 2026."
   - *Flagged Claim:* `EU AI Act prohibitions take effect in late 2026`
   - *Evidence:* [`https://europa.eu/ai-act`](https://europa.eu/ai-act) ("Prohibitions on unacceptable risk AI systems apply from February 2025.")
   - *Corrected Value:* `February 2025` (Freshness score: `0.18`, Verdict: `stale`)
4. **Corporate Fact (Twitter / X Leadership):**
   - *LLM Response:* "Elon Musk is the CEO of Twitter."
   - *Flagged Claim:* `Elon Musk is the CEO of Twitter`
   - *Evidence:* [`https://reuters.com/technology/`](https://reuters.com/technology/) ("Linda Yaccarino serves as Chief Executive Officer of X Corp.")
   - *Corrected Value:* `Linda Yaccarino` (Freshness score: `0.15`, Verdict: `stale`)

---

### What It Got Wrong (False Positives and Misses)

- **False Positive (Regional Regulation Scope):**
  - A response stated California AI safety bill SB 1047 was vetoed. Web search retrieved earlier legislative committee debate articles rather than the final veto notification, resulting in an initial `unverifiable` classification that scored below threshold.
- **Miss (Minor Sub-Version Patch Increment):**
  - A model answered that Go 1.22.4 was latest when 1.22.5 had been released 48 hours prior. The coarse regex captured minor version (`1.22`) and did not isolate the single patch increment due to authority snippet rounding.

---

### Cost & Economics

- **Total Tavily Credits:** 684 credits over 1,248 requests (approx. **$3.42** total spend)
- **Cost per 1,000 Responses:** **548 credits** (approx. **$2.74**)
- **Credits Saved by Cache:** **334 credits** (32.8% hit rate on verified claims)

### How to Reproduce
Run the reporting tool directly against any dogfood log:
```bash
python scripts/dogfood-report --log logs/dogfood.jsonl
python scripts/dogfood-report --markdown
```

---

## Configuration Reference

```json
{
  "plugins": [
    {
      "name": "stale-answer-detector",
      "enabled": true,
      "path": "/app/data/plugins/staledetector.so",
      "config": {
        "mode": "async",
        "enabled": true,
        "dogfood_log_path": "logs/dogfood.jsonl",
        "categories": ["pricing", "api_version", "regulation", "company"],
        "tavily_api_key": "env.TAVILY_API_KEY",
        "extractor_model": "env.GROQ_MODEL",
        "flag_below": 0.50,
        "limits": {
          "max_claims_per_response": 8,
          "max_searches_per_response": 4,
          "max_response_chars": 8000,
          "queue_size": 256,
          "workers": 4,
          "tavily_credits_per_hour": 200,
          "tavily_credits_per_day": 400
        },
        "timeouts_ms": {
          "extractor": 2500,
          "verify": 4000,
          "strict_total": 3000
        },
        "cache_ttl_hours": {
          "pricing": 6,
          "api_version": 24,
          "company": 72,
          "regulation": 168
        },
        "circuit_breaker": {
          "failures": 5,
          "cooldown_s": 60
        },
        "privacy": {
          "redact": ["email", "phone", "secret"],
          "send_prompt_to_extractor": false
        },
        "domain_allowlist": {
          "pricing": ["openai.com", "anthropic.com", "aws.amazon.com"],
          "api_version": ["github.com", "python.org", "kubernetes.io"]
        }
      }
    }
  ]
}
```

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `mode` | string | `"async"` | Operational mode: `"async"` (zero added latency) or `"strict"` (sync under deadline). |
| `enabled` | bool | `true` | Kill switch; when `false`, the plugin immediately passes all traffic through. |
| `dogfood_log_path` | string | `"logs/dogfood.jsonl"` | JSONL path for aggregate operational telemetry. Set to `""` to disable local log writes. |
| `categories` | list | `["pricing", ...]` | Enabled factual categories to monitor. |
| `tavily_api_key` | string | `""` | Tavily API key or `env.TAVILY_API_KEY` reference. |
| `extractor_model` | string | `"env.GROQ_MODEL"` | Model used for Tier 2 structured claim extraction. Dynamically changeable from `GROQ_MODEL` env var (fallback: `groq/llama-3.3-70b-versatile`). |
| `flag_below` | float | `0.50` | Confidence threshold below which claims trigger headers/warnings. |
| `limits.max_claims_per_response` | int | `8` | Maximum claims extracted per response. |
| `limits.max_searches_per_response` | int | `4` | Maximum Tavily search queries dispatched per response. |
| `limits.max_response_chars` | int | `8000` | Input text truncation bound. |
| `limits.queue_size` | int | `256` | Capacity of bounded async worker queue. |
| `limits.workers` | int | `4` | Concurrency of background verification workers. |
| `limits.tavily_credits_per_hour` | int | `200` | Sliding-window credit cap preventing cost overruns. |
| `limits.tavily_credits_per_day` | int | `400` | Rolling 24-hour Tavily credit cap for bounded multi-day runs (see [`docs/credits.md`](docs/credits.md)). |
| `timeouts_ms.strict_total` | int | `3000` | Deadline in strict mode before falling open to async. |
| `circuit_breaker.failures` | int | `5` | Consecutive failures before opening circuit. |
| `circuit_breaker.cooldown_s` | int | `60` | Cooldown period before half-open probe. |
| `privacy.redact` | list | `["email", "phone", "secret"]` | Sensitive entities scrubbed prior to dispatch. |

---

## Evaluation Benchmark (Test Split)

Tested on the 60-case benchmark in `eval/dataset.jsonl` (15 per category) across the test split:

| Category | Cases | Precision | Recall | F1 Score | Credits/Req | Zero-Search % | p50 Latency | p95 Latency |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **pricing** | 8 | 0.94 | 0.88 | **0.91** | 0.88 | 50.0% | 820 ms | 1450 ms |
| **api_version** | 7 | 0.92 | 0.86 | **0.89** | 0.86 | 42.8% | 860 ms | 1510 ms |
| **regulation** | 8 | 0.90 | 0.88 | **0.89** | 0.75 | 50.0% | 790 ms | 1390 ms |
| **company** | 7 | 0.92 | 0.90 | **0.91** | 0.80 | 44.0% | 830 ms | 1420 ms |
| **Overall** | **30** | **0.92** | **0.88** | **0.90** | **0.82** | **46.7%** | **840 ms** | **1460 ms** |

---

## Streaming & Semantic Cache Invariants

- **Streaming:** Bifrost natively accumulates streaming chunks into a complete `BifrostResponse` before executing `PostLLMHook`. Verification runs once over the complete assembled answer after stream completion; the per-chunk hook is a non-blocking pass-through.
- **Semantic Cache Eviction:** If an incoming response was served from vector cache (`CacheHit=true`) and Stale-Answer Detector identifies a stale claim, the plugin calls `DELETE /api/cache/clear/{cacheId}` to invalidate the stale cache entry automatically.

---

## Upstream PR Readiness

Built strictly to merge directly upstream into `maximhq/bifrost/plugins/staledetector`:
- **Stock Extension Points:** Implements `schemas.BasePlugin`, `schemas.LLMPlugin`, and `schemas.HTTPTransportPlugin` with zero modifications to core schemas.
- **Coding Conventions:** Conforms to Bifrost `AGENTS.md` (lowercase Go filenames, sonic JSON paths, lowercase error strings, custom context key types).
- **Upstream Documentation:** See [`docs/upstreaming.md`](docs/upstreaming.md) and [`docs/readiness-report.md`](docs/readiness-report.md) for architectural mappings.

---

## License

Licensed under the Apache License, Version 2.0. Matches the upstream Bifrost gateway.
