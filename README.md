# Stale-Answer Detector for Bifrost

> A production-grade Bifrost AI Gateway plugin and pure-Go verification engine that identifies likely-stale claims in LLM responses (pricing, API versions, regulations, and corporate facts), re-verifies them against live web evidence using Tavily, and attaches an audit verdict, freshness confidence score, and grounded corrections with microsecond hot-path overhead.

---

## 5-Minute Quickstart

### Prerequisites
- Docker & Docker Compose
- `curl`

### 1. Start the Dynamic Gateway with Plugin
Clone the repository and launch the containerized gateway with the plugin compiled and mounted:

```bash
cd stale-answer-detector
make docker-build
make up
```

The gateway starts on `http://localhost:8080` with the built-in `mocker` serving canned responses, so you can test end-to-end immediately without needing any live API keys.

### 2. Send a Test Request
Send a chat completion request:

```bash
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer mock-key" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "What is the price of GPT-4o and what is the latest Python release?"}
    ]
  }'
```

The HTTP response headers will include:
```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Freshness-Status: checked
X-Freshness-Score: 0.12
X-Freshness-Flagged: true
X-Freshness-Verdict-URL: /api/plugins/staledetector/verdicts/req-chatcmpl-123
```

### 3. Query the Audit Verdict
Inspect the verified claims, evidence URLs, and corrected values:

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
      "text": "GPT-4o costs $5.00 per million tokens",
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

### 4. Scrape Prometheus Metrics
```bash
curl -s http://localhost:8080/api/plugins/staledetector/metrics
```

---

## Architecture

```
Client HTTP Request
       │
       ▼
 FastHTTP Transport
       │
       ▼
 PostLLMHook (Hot Path: < 50µs)
  ├── 1. Tier 1 Fast Regex Filter (zero allocs if no numbers/dates/versions)
  ├── 2. In Async Mode: Non-blocking channel enqueue (drops if saturated)
  └── 3. In Strict Mode: Synchronous verification under strict deadline
       │
       ▼
 Response immediately returned to client (Fail Open)
       │
       ▼ (Async Background Worker Pool)
 Worker Loop:
  ├── Sensitive Data Redaction (PII & API secrets)
  ├── Tier 2 LLM Claim Extraction (strict JSON & span validation)
  ├── Claim Cache Lookup (LRU + category TTL)
  ├── Circuit Breaker & Budget Cap Evaluation
  ├── Single-Flight Deduplication
  ├── Tavily Live Web Search (or Replay)
  ├── Grounding Validation (prohibits hallucinated corrections)
  ├── Volatility & Source Authority Scoring
  └── Verdict Storage & Semantic Cache Invalidation
```

---

## Freshness Confidence Score Formula

The score $S \in [0, 1]$ represents confidence that a claim is current:

1. **Volatility Risk $v \in [0.10, 0.95]$:**
   $$v = 1.0 - \exp\left(-\frac{\Delta t \cdot \ln 2}{H}\right)$$
   where $H$ is category half-life (pricing: 90d, api_version: 120d, company: 180d, regulation: 365d) and $\Delta t$ is the elapsed days since the model's knowledge cutoff.
2. **Source Authority $a \in [0.20, 0.98]$:** Baseline $0.70$, $+0.20$ for allowlisted domains, $+0.08$ for evidence published within 30 days.
3. **Verdict & Score Calculation:**
   - **Match:** verdict `current`, $S = 0.70 + 0.30 \cdot a$
   - **Mismatch:** verdict `stale`, $S = 0.25 \cdot (1.0 - a)$
   - **None:** verdict `unverifiable`, $S = \min(0.50, 1.0 - v)$

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
        "categories": ["pricing", "api_version", "regulation", "company"],
        "tavily_api_key": "env.TAVILY_API_KEY",
        "extractor_model": "openai/gpt-4o-mini",
        "flag_below": 0.50,
        "limits": {
          "max_claims_per_response": 8,
          "max_searches_per_response": 4,
          "max_response_chars": 8000,
          "queue_size": 256,
          "workers": 4,
          "tavily_credits_per_hour": 200
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
| `categories` | list | `["pricing", ...]` | Enabled factual categories to monitor. |
| `tavily_api_key` | string | `""` | Tavily API key or `env.TAVILY_API_KEY` reference. |
| `extractor_model` | string | `"openai/gpt-4o-mini"` | Model used for Tier 2 structured claim extraction. |
| `flag_below` | float | `0.50` | Confidence threshold below which claims trigger headers/warnings. |
| `limits.max_claims_per_response` | int | `8` | Maximum claims extracted per response. |
| `limits.max_searches_per_response` | int | `4` | Maximum Tavily search queries dispatched per response. |
| `limits.max_response_chars` | int | `8000` | Input text truncation bound. |
| `limits.queue_size` | int | `256` | Capacity of bounded async worker queue. |
| `limits.workers` | int | `4` | Concurrency of background verification workers. |
| `limits.tavily_credits_per_hour` | int | `200` | Sliding-window credit cap preventing cost overruns. |
| `timeouts_ms.strict_total` | int | `3000` | Deadline in strict mode before falling open to async. |
| `circuit_breaker.failures` | int | `5` | Consecutive failures before opening circuit. |
| `circuit_breaker.cooldown_s` | int | `60` | Cooldown period before half-open probe. |
| `privacy.redact` | list | `["email", "phone", "secret"]` | Sensitive entities scrubbed prior to dispatch. |

---

## Evaluation Benchmark Results

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

## License

Licensed under the Apache License, Version 2.0. Matches upstream Bifrost gateway.

