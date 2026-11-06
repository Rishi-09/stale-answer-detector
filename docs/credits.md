# Tavily Credit Caps & Budget Governance

This document defines the credit consumption model, budget caps, rate limiting policies, and cost management rules for the Stale-Answer Detector plugin.

---

## 1. Credit Consumption Model

Stale-Answer Detector uses the Tavily Search API for live web verification:

| Action | API Depth | Credits Consumed | Notes |
| :--- | :--- | :--- | :--- |
| **Standard Verification** | `basic` | **1 credit** | Default query mode. Extracts top 3 web snippets per claim. |
| **Advanced Verification** | `advanced` | **2 credits** | Optional high-depth verification when configured. |
| **Cache Hit** | Local Cache | **0 credits** | Verified claims within TTL serve from local LRU cache. |
| **Tier 1 Negative Filter** | Local Regex | **0 credits** | Answers with no candidate patterns bypass search completely. |
| **Deduplicated Query** | Single-Flight | **0 credits** | Concurrent identical claims share a single inflight search. |

---

## 2. Configured Budget Caps

To prevent cost overruns, runaway traffic loops, or accidental billing spikes during production and multi-day dogfooding runs, the plugin enforces both **hourly** and **daily** hard limits:

```json
{
  "limits": {
    "max_claims_per_response": 8,
    "max_searches_per_response": 4,
    "max_response_chars": 8000,
    "tavily_credits_per_hour": 200,
    "tavily_credits_per_day": 400
  }
}
```

### Caps Breakdown
- **Daily Cap: 400 credits / day**
  - Hard limit on Tavily API credits consumed across a rolling 24-hour window.
  - Bounds dogfooding expenditure to under **$1.00 - $2.00 / day** (at standard API rates).
  - Multi-day runs (3 to 5 days) will consume at most 1,200 to 2,000 total credits.
- **Hourly Cap: 200 credits / hour**
  - Hard limit on Tavily API credits consumed within a 60-minute window.
  - Absorbs traffic bursts while preventing a sudden surge from consuming the entire day's budget in minutes.
- **Per-Response Cap: 4 searches / response**
  - Caps the maximum web searches dispatched for any single LLM completion, even if the model generates dozens of claims.

---

## 3. Enforcement Architecture (`BudgetController`)

Credit tracking is governed by `freshness.BudgetController` (`freshness/budget.go`), instantiated per engine:

1. **Pre-Search Allowance Check (`AllowSearch(credits)`):**
   - Before dispatching any Tavily HTTP request, the engine invokes `budget.AllowSearch(1)`.
   - The controller atomically checks:
     $$\text{creditsSpentInHour} + \text{credits} \le \text{hourlyCap}$$
     $$\text{creditsSpentInDay} + \text{credits} \le \text{dailyCap}$$
   - If either condition is violated, `AllowSearch` returns `false`.

2. **Window Sliding:**
   - The hourly window resets after `time.Since(creditHourStart) >= 1 * time.Hour`.
   - The daily window resets after `time.Since(creditDayStart) >= 24 * time.Hour`.

3. **Budget Exhaustion (Strict Fail-Open):**
   - When the budget cap is reached:
     - No search queries are dispatched to Tavily.
     - The claim verdict is marked as `unverifiable` (score computed via volatility decay $S = \min(0.50, 1.0 - v)$).
     - The gateway response is returned to the user immediately and unaltered.
     - Telemetry metric `staledetector_credits_spent_total` ceases incrementing, and dropped searches are logged.

---

## 4. Multi-Day Dogfooding Budget Projection

Estimated credit consumption during a 3 to 5 day dogfood run with mixed user and scripted traffic:

| Traffic Component | Requests / Day | Tier 1 Filter Rate | Cache Hit Rate | Searches / Day | Credits / Day |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Real User Traffic** | ~300 - 500 | 45% bypassed (0 credits) | 25% cache hit | ~150 - 225 | ~150 - 225 |
| **Scripted Daily Batch** | 60 (20 q × 3 models) | 0% bypassed (freshness-targeted) | 30% cache hit | ~40 - 50 | ~40 - 50 |
| **Total Daily Spend** | **360 - 560 req** | — | — | **~190 - 275 searches** | **~190 - 275 credits** |

**Conclusion:** Normal operational load comfortably operates within the **400 credits/day** ceiling (48% - 69% utilization), leaving ample headroom for unexpected burst traffic without breaching caps.

