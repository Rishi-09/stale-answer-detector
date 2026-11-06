# Live Test Runbook

Run the detector in `async` mode for 3 to 5 days. Async verification is queued after the
completion is returned, so it never adds verification latency to callers.

## Scope and privacy

Route only deliberately approved, non-sensitive traffic through the test gateway. Do not route
proprietary source code, client data, credentials, or private conversations. Keep
`privacy.redact` enabled for `email`, `phone`, and `secret`; it is applied before extraction,
web verification, and dogfood-log serialization. The JSONL log intentionally stores no prompt or
full response, but it does retain redacted extracted claims for review. Treat the log as
operational data and keep it local.

## Gateway configuration

The included Docker configuration enables `async` mode, redaction, a 200-credit hourly cap, a
400-credit rolling daily cap, and writes the log to `logs/dogfood.jsonl` on the host. For a live
gateway, use its normal provider credentials and change only its base URL in your LLM tools.

For the included Docker setup, create your local key file once:

```bash
cp .env.example .env
```

Set `TAVILY_API_KEY` for live verification and the credential for each provider you route through
(for example, `OPENAI_API_KEY`). Docker Compose reads `.env` automatically. `.env` is ignored by
Git; do not put credentials in `docker/config.json` or the dogfood log.

The plugin has a kill switch:

```json
{ "enabled": false }
```

Disabling it passes traffic through without queuing, searching, or logging it.

## Daily comparison batch

Once per day, run 20 public freshness-sensitive questions against two or three models:

```bash
python3 scripts/daily-batch --base-url http://localhost:8080 --models gpt-4o-mini,gpt-4o,claude-3-5-sonnet
```

The batch sends no private inputs and appends to the same dogfood log produced by the gateway.
Use `--api-key` only when the gateway requires one. It returns nonzero if one or more requests
fail, while completing the rest of the batch.

## Review and README evidence

At the end of the run, inspect aggregate outcomes and sample both alerts and non-alerts:

```bash
python3 scripts/dogfood-report --log logs/dogfood.jsonl
python3 scripts/dogfood-report --log logs/dogfood.jsonl --label
python3 scripts/dogfood-report --log logs/dogfood.jsonl --markdown
```

Use the generated Markdown only with the actual log from the run. It reports stale rate per
model, cost, cache behavior, latency, drops, errors, and precision from the saved manual labels.
