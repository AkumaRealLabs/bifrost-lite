# Bifrost Lite HTTP Gateway

This module contains the retained HTTP gateway for Bifrost Lite. It serves the OpenAI-compatible API surface, the Lite admin API, and the embedded UI build.

## Retained API Surface

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/images/generations`
- `POST /v1/images/edits`
- Provider/key admin APIs
- Virtual-key admin APIs
- Logs/dashboard APIs required by the Lite UI

## Removed From This Fork

- Internal tool-gateway routes and handlers
- Live socket transport routes
- Built-in skill repository serving and short-lived scoped serving token APIs
- Prompt repository, routing-rule UI/API surface, plugin management UI, and unused enterprise admin pages

Provider protocol schemas may still include provider-native tool fields where OpenAI or Anthropic expose those fields directly. Those are compatibility data structures, not the removed internal tool runtime.

## Development

From the repository root:

```bash
cd transports
GOCACHE=/tmp/bifrost-lite-go-cache go test -run '^$' ./...
```

The UI build is embedded by the transport Dockerfile. Build it from `ui/` before packaging when needed:

```bash
cd ../ui
npm run build-enterprise
```
