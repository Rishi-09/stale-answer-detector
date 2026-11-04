# Architectural Decisions & Recon Answers (Phase 0)

> Source-of-truth: Bifrost code at target tag `transports/v2.4.0` (core revision `v1.14.1`, commit `9c6ac22ceb9794b7dafdceced5f25d93a43f18e4`).
> Date: November 4, 2026

---

## Answers to Recon Questions (Section 9)

### Q1: Exact Signatures in `core/schemas/plugin.go`

- **BasePlugin** (`core/schemas/plugin.go:285-293`):
  ```go
  type BasePlugin interface {
      GetName() string
      Cleanup() error
  }
  ```
- **LLMPlugin** (`core/schemas/plugin.go:408-429`):
  ```go
  type LLMPlugin interface {
      BasePlugin
      PreRequestHook(ctx *BifrostContext, req *BifrostRequest) error
      PreLLMHook(ctx *BifrostContext, req *BifrostRequest) (*BifrostRequest, *LLMPluginShortCircuit, error)
      PostLLMHook(ctx *BifrostContext, resp *BifrostResponse, bifrostErr *BifrostError) (*BifrostResponse, *BifrostError, error)
  }
  ```
- **MCPPlugin** (`core/schemas/plugin.go:431-436`):
  ```go
  type MCPPlugin interface {
      BasePlugin
      PreMCPHook(ctx *BifrostContext, req *BifrostMCPRequest) (*BifrostMCPRequest, *MCPPluginShortCircuit, error)
      PostMCPHook(ctx *BifrostContext, resp *BifrostMCPResponse, bifrostErr *BifrostError) (*BifrostMCPResponse, *BifrostError, error)
  }
  ```
- **HTTPTransportPlugin** (`core/schemas/plugin.go:295-393`):
  ```go
  type HTTPTransportPlugin interface {
      BasePlugin
      HTTPTransportPreAuthHook(ctx *BifrostContext, req *HTTPRequest) (*HTTPResponse, error)
      HTTPTransportPreHook(ctx *BifrostContext, req *HTTPRequest) (*HTTPResponse, error)
      HTTPTransportPostHook(ctx *BifrostContext, req *HTTPRequest, resp *HTTPResponse) error
      HTTPTransportResponseHeadersHook(ctx *BifrostContext, req *HTTPRequest, resp *HTTPResponseMetadata) error
      HTTPTransportStreamChunkHook(ctx *BifrostContext, req *HTTPRequest, chunk *BifrostStreamChunk) (*BifrostStreamChunk, error)
  }
  ```
- **ObservabilityPlugin** (`core/schemas/plugin.go:574-597`):
  ```go
  type ObservabilityPlugin interface {
      BasePlugin
      Inject(ctx context.Context, trace *Trace) error
  }
  ```

---

### Q2: Multiple Interface Implementation

**Finding:** Yes.
**Evidence:**
- In `framework/plugins/soplugin.go:10-69`, `DynamicPlugin` defines individual function pointers for every hook.
- In `framework/plugins/soloader.go:70-244`, `SharedObjectPluginLoader.LoadPlugin` performs separate `pluginObj.Lookup` calls for each hook. Any exported hook symbol is wired into `DynamicPlugin`.
- In-repo plugins (`plugins/governance`, `plugins/telemetry`) also implement multiple interfaces simultaneously.

---

### Q3: Observability Trace Content & Response Text Availability

**Finding:**
- Trace structure (`core/schemas/trace.go:12-26`): `Trace` contains `RequestID`, `TraceID`, `RootSpan`, `Spans`, `Attributes`, `RequestHeaders`, `PluginLogs`.
- Response attributes on spans (`core/schemas/trace.go:566, 1107`): `AttrOutputMessages` (`"gen_ai.output.messages"`), `AttrRespReasoningText`, and `AttrBifrostRawResponse`.
- **Critical Risk with Observability `Inject`:** If governance or configuration disables content logging (`ContentLoggingDisabledForTrace(t) == true`), content attributes on spans are REDACTED and stripped (`core/schemas/trace.go:55-65, 242-262, 580-608`). In that case, `Inject` receives an empty or redacted response text.
- Furthermore, traces are released to `sync.Pool` immediately when `Inject` returns (`core/schemas/plugin.go:592-596`).
- **Decision on Async Path:** Use `PostLLMHook` as the primary ingestion hook. `PostLLMHook` always receives the unredacted `*BifrostResponse` (choices, message content) for both unary and stream-accumulated responses. On the hot path, `PostLLMHook` runs Tier 1 regex detection and enqueues check jobs to a bounded async worker pool in microseconds, adding virtually zero latency to the client response.

---

### Q4: Response Annotation & Headers

**Finding:**
- **Response Headers:** `HTTPTransportResponseHeadersHook(ctx *BifrostContext, req *HTTPRequest, resp *HTTPResponseMetadata) error` (`core/schemas/plugin.go:361-375`). Called once for buffered and streaming responses at the last safe point before HTTP response headers are committed. The plugin calls `resp.SetHeader("X-Freshness-Status", ...)` and `resp.SetHeader("X-Freshness-Verdict-URL", ...)`.
- **Response Extra Fields / Body Annotation:** In strict mode, `PostLLMHook` or `HTTPTransportPostHook` (`core/schemas/plugin.go:344-360`) can annotate `resp.Body` (JSON) or attach freshness metadata.

---

### Q5: Semantic Cache Storage & Eviction Path

**Finding:**
- **Storage:** `plugins/semanticcache/main.go:943-950, 1019-1064` stores entries in vector databases (Weaviate, Qdrant, Pinecone, Redis). When an entry is served or written, the cache metadata is stamped on `BifrostResponseExtraFields.CacheDebug.CacheID` and `CacheKey`.
- **Eviction Path:** `transports/bifrost-http/handlers/cache.go:15-18` defines:
  ```go
  type CacheClearer interface {
      ClearCacheForCacheID(cacheID string) error
      ClearCacheForKey(cacheKey string) error
  }
  ```
  Exposed via HTTP routes (`handlers/cache.go:40-43`):
  - `DELETE /api/cache/clear/{cacheId}`
  - `DELETE /api/cache/clear-by-key/{cacheKey}`
- **Action on Stale Cache Entry:** When a response with `CacheHit == true` is found to contain a stale claim, the plugin calls `ClearCacheForCacheID` (or issues `DELETE /api/cache/clear/{cacheId}`) to invalidate the outdated entry.

---

### Q6: Prometheus Metrics Alongside Telemetry Plugin

**Finding:**
- In `transports/bifrost-http/server/server.go:3205-3221`, `/metrics` collects exclusively from `telemetry.PrometheusPlugin.GetMetricsGatherer()`, which uses private registries (`p.registry` and `p.systemRegistry`).
- The telemetry plugin does not expose a registration API for external `.so` plugins.
- **Decision:** The plugin creates its own dedicated `prometheus.Registry` with low-cardinality metrics (claims detected, verified, verdicts by category, Tavily searches, credits spent, cache hits, queue depth, circuit state). The plugin exposes its metrics via `HTTPTransportPreHook` at `GET /api/plugins/staledetector/metrics` and registers with `prometheus.DefaultRegisterer`.

---

### Q7: Exposing Custom HTTP Routes & Result Surfaces

**Finding:**
- Gateway routes are registered statically at server bootstrap (`server.go:2863-3251`); dynamic plugins cannot add routes to `s.Router` directly.
- **Interception Mechanism:** `HTTPTransportPreHook` (`core/schemas/plugin.go:328-342`) intercepts incoming requests before core routing. Returning `(*schemas.HTTPResponse, nil)` short-circuits the gateway and serves custom responses immediately.
- **Chosen Result Surfaces (F6):**
  1. **Verdict Lookup API by Request ID:** `GET /api/plugins/staledetector/verdicts/{requestId}` (intercepted via `HTTPTransportPreHook`).
  2. **Response Header:** `X-Freshness-Status`, `X-Freshness-Score`, `X-Freshness-Verdict-URL: /api/plugins/staledetector/verdicts/{requestId}` injected via `HTTPTransportResponseHeadersHook`.
  3. **Strict-Mode Response Annotation:** In strict mode, claims and scores are appended directly to the response body/headers within the deadline.
  4. **Structured Audit Log Entries:** Emitted via `BifrostContext.Log()` / `schemas.Logger` with `claim_id`, `category`, `verdict`, `score`, and `evidence`.

---

### Q8: Model Catalog Knowledge Cutoff

**Finding:**
- In `framework/modelcatalog/modelinfo.go:23-43` and `framework/modelcatalog/models.go`, the catalog stores pricing, capabilities, context window, and deprecation, but NO knowledge cutoff date.
- **Decision:** Maintain a configurable cutoff mapping in plugin config (`knowledge_cutoffs`), with built-in defaults for common model families (e.g. `gpt-4o`: 2023-10, `claude-3-5-sonnet`: 2024-04).

---

### Q9: Pinned Release Tag, Core Revision & Go Toolchain

**Finding:**
- **Target Tag:** `transports/v2.4.0` (commit `9c6ac22ceb9794b7dafdceced5f25d93a43f18e4`).
- **Core Revision:** `github.com/maximhq/bifrost/core v1.14.1` (from `transports/go.mod`).
- **Go Toolchain:** `go 1.27.3` (from `go.work` and `core/go.mod`).
- **Dynamic Build Image:** `golang:1.27.3-alpine3.24` (`examples/plugins/hello-world/Makefile:131`).
- Building a `.so` plugin requires matching the Go minor version, toolchain, and dependency versions.

---

### Q10: Config Passing & Adapter Structure

**Finding:**
- In `framework/plugins/soloader.go:81-89`, `Init` is invoked as:
  ```go
  func Init(config any) error
  ```
  `config` is passed as `any` (a `map[string]any` parsed from the JSON configuration).
- `.so` dynamic loading requires `package main` with exported functions (`Init`, `GetName`, `Cleanup`, `PostLLMHook`, `HTTPTransportPreHook`, `HTTPTransportResponseHeadersHook`).
- In-repo plugins use a package struct `Plugin` satisfying `schemas.LLMPlugin`, `schemas.HTTPTransportPlugin`.
- **Dual Support Pattern:**
  - `staledetector.go` defines `type Plugin struct` with all interface methods (for in-repo usage).
  - Package-level functions (`Init`, `GetName`, `Cleanup`, `PostLLMHook`, etc.) delegate to a package-level `defaultPlugin` instance for `.so` builds.
  - The business logic lives entirely in `freshness/` with zero Bifrost imports.
