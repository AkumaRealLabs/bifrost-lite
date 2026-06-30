# Bifrost Lite

Bifrost Lite is an AkumaRealLabs fork of `maximhq/bifrost` trimmed for the production shape used by the 157 gateway: OpenAI-compatible HTTP routing, providers and keys, virtual keys, weighted fallback, request logs, and lightweight cost/reporting views.

This fork intentionally removes the broad platform surface from upstream Bifrost. The first Lite version is not a clean-room rewrite; it keeps the upstream gateway/core structure so future upstream merges remain practical.

## Retained Scope

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/images/generations`
- `POST /v1/images/edits`
- Provider and provider-key management
- Virtual keys
- Provider/key weighting and fallback
- PostgreSQL/SQLite config and log stores
- Request logs, success/error samples, token usage, and cost aggregation
- Lite admin UI for providers, keys, virtual keys, logs, and dashboard views

## Removed Scope

- Tool-server gateway/runtime, tool registry/session/log/group management, and related admin pages
- Live socket transport runtime and UI socket layer
- Built-in skill repository serving and short-lived scoped serving tokens
- Teams/customers/business units, budget/rate-limit pages, routing rules, prompt repository, plugin management, observability connectors, guardrails, and related admin pages
- Most upstream docs/examples/release workflows that are not needed for Lite build and verification

OpenAI/Anthropic protocol compatibility structs may still contain provider-native tool fields where those are part of request/response schemas. They are not the removed internal tool gateway runtime.

## Repository Layout

```text
bifrost-lite/
├── core/                 # Core gateway logic and provider implementations
├── framework/            # Config/log stores, model catalog, tracing helpers
├── transports/           # HTTP gateway and embedded UI build
├── ui/                   # Lite admin UI
├── plugins/              # Retained runtime plugins used by Lite
└── config/157-lite.example.json
```

## Local Verification

```bash
cd core && GOCACHE=/tmp/bifrost-lite-go-cache go test -run '^$' ./...
cd ../framework && GOCACHE=/tmp/bifrost-lite-go-cache go test -run '^$' ./...
cd ../transports && GOCACHE=/tmp/bifrost-lite-go-cache go test -run '^$' ./...
cd ../ui && npm run typecheck && npm run build-enterprise
jq empty ../transports/config.schema.json
```

## Docker Build

```bash
docker build -f transports/Dockerfile -t bifrost-lite:local .
```

## Example Config

`config/157-lite.example.json` is a sanitized seed matching the Lite deployment shape. It uses placeholder provider keys and Lite model aliases such as `gpt_low`, `gpt_stable`, and `gpt_image`.
