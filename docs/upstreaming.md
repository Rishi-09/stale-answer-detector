# Upstreaming Guide: Bifrost Plugin Submission

## 1. Destination in Bifrost Repository

To upstream `stale-answer-detector` as a first-party built-in plugin in `maximhq/bifrost`:

1. Copy the entire repository directory into the upstream repository at:
   ```
   plugins/staledetector/
   ├── go.mod
   ├── staledetector.go
   ├── staledetector_test.go
   ├── freshness/
   │   ├── types.go
   │   ├── detect.go
   │   ├── detect_test.go
   │   ├── extract.go
   │   ├── extract_test.go
   │   ├── score.go
   │   ├── score_test.go
   │   ├── verify.go
   │   ├── verify_test.go
   │   ├── tavily.go
   │   ├── tavily_test.go
   │   ├── cache.go
   │   ├── cache_test.go
   │   ├── budget.go
   │   ├── budget_test.go
   │   ├── breaker.go
   │   ├── breaker_test.go
   │   ├── redact.go
   │   └── redact_test.go
   ├── eval/
   └── README.md
   ```

2. Register the module in root `go.work`:
   ```go
   use (
       ./core
       ./framework
       ./transports
       ...
       ./plugins/staledetector
   )
   ```

3. Remove the local development replace line from `plugins/staledetector/go.mod`:
   ```diff
   - replace github.com/maximhq/bifrost/core => ../core
   ```

---

## 2. Zero Core Diffs

The plugin strictly adheres to Bifrost extension rules:
- **Zero changes to `core/`**: No core interface modifications or monkey patches.
- **Pure Go domain logic**: The `freshness/` package has zero Bifrost imports and can be compiled, tested, and audited independently.
- **Uses sanctioned extension points only**: `LLMPlugin` (`PostLLMHook`) and `HTTPTransportPlugin` (`HTTPTransportPreHook`, `HTTPTransportResponseHeadersHook`).

---

## 3. Upstream Configuration Schema (`transports/config.schema.json`)

To enable IDE autocompletion in upstream `config.json`, add the schema definition to `transports/config.schema.json` under `plugins.items.properties.config`:

```json
{
  "staledetector_config": {
    "type": "object",
    "properties": {
      "mode": { "type": "string", "enum": ["async", "strict"], "default": "async" },
      "categories": {
        "type": "array",
        "items": { "type": "string", "enum": ["pricing", "api_version", "regulation", "company"] }
      },
      "tavily_api_key": { "type": "string" },
      "extractor_model": { "type": "string", "default": "openai/gpt-4o-mini" },
      "flag_below": { "type": "number", "minimum": 0.0, "maximum": 1.0, "default": 0.50 },
      "limits": {
        "type": "object",
        "properties": {
          "max_claims_per_response": { "type": "integer", "default": 8 },
          "max_searches_per_response": { "type": "integer", "default": 4 },
          "max_response_chars": { "type": "integer", "default": 8000 },
          "queue_size": { "type": "integer", "default": 256 },
          "workers": { "type": "integer", "default": 4 },
          "tavily_credits_per_hour": { "type": "integer", "default": 200 }
        }
      }
    }
  }
}
```

---

## 4. Upstream Makefile Integration

Add the plugin test target to the upstream `Makefile`:
```makefile
test-staledetector:
	cd plugins/staledetector && go test -v -race -cover ./...
```

