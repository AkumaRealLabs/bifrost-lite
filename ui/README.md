# Bifrost Lite UI

A focused web interface for Bifrost Lite: provider/key management, virtual keys, request logs, and cost/performance dashboards for the OpenAI-compatible gateway.

## Overview

Bifrost Lite UI is a React + Vite + TanStack Router dashboard for the retained Lite gateway surface. It monitors AI requests, configures providers and virtual keys, and analyzes performance metrics.

### Key Features

- **Request Log Monitoring** - Request dashboard with filtering and detailed request/response inspection
- **Provider Management** - Configure [15+ AI providers](https://docs.getbifrost.ai/quickstart/gateway/provider-configuration)
- **Virtual Keys** - Manage Lite gateway access keys
- **Analytics Dashboard** - Request metrics, success rates, latency tracking, and token usage
- **Modern UI** - Dark/light mode, responsive design, and accessible components

## Quick Start

### Prerequisites

The UI is designed to work with the Bifrost HTTP transport backend. Get started with the complete setup:

**[Gateway Setup Guide →](https://docs.getbifrost.ai/quickstart/gateway/setting-up)**

### Development

```bash
# Install dependencies
npm install

# Start development server
npm run dev
```

The development server runs on `http://localhost:3000` and connects to your Bifrost HTTP transport backend (default: `http://localhost:8080`).

### Environment Variables

```bash
# Development only - customize Bifrost backend port
BIFROST_PORT=8080
```

## Architecture

### Technology Stack

- **Framework**: React 19 + Vite + TanStack Router
- **Language**: TypeScript
- **Styling**: Tailwind CSS + Radix UI components
- **State Management**: Redux Toolkit with RTK Query
- **HTTP Client**: Axios with typed service layer
- **Theme**: Dark/light mode support

### Integration Model

```
┌─────────────────┐       HTTP API       ┌──────────────────┐
│   Bifrost UI    │ ◄─────────────────► │ Bifrost HTTP     │
│   (React+Vite)  │                     │ Transport (Go)   │
└─────────────────┘                     └──────────────────┘
        │                                        │
        │ Build artifacts                        │
        └────────────────────────────────────────┘
```

- **Development**: UI runs on port 3000, connects to Go backend on port 8080
- **Production**: UI built as static assets served directly by Go HTTP transport
- **Communication**: REST API

## Features

### Request Log Monitoring

The main dashboard provides request monitoring with advanced filtering and detailed request/response inspection.

**[Learn More →](https://docs.getbifrost.ai/features/observability)**

### Provider Configuration

Manage all your AI providers from a unified interface with support for multiple API keys, custom network configuration, and provider-specific settings.

**[View All Providers →](https://docs.getbifrost.ai/quickstart/gateway/provider-configuration)**

## Development

### Project Structure

```
ui/
├── app/                    # TanStack Router pages
│   ├── page.tsx           # Main logs dashboard
│   └── config/            # Lite gateway configuration
├── components/            # Reusable UI components
│   ├── logs/             # Log monitoring components
│   ├── config/           # Configuration forms
│   └── ui/               # Base UI components (Radix)
├── hooks/                # Custom React hooks
├── lib/                  # Utilities and services
│   ├── store/            # Redux store and API slices
│   ├── types/            # TypeScript definitions
│   └── utils/            # Helper functions
└── scripts/              # Build and deployment scripts
```

### API Integration

The UI uses Redux Toolkit + RTK Query for state management and API communication with the Bifrost HTTP transport backend:

```typescript
// Example API usage with RTK Query
import { useGetLogsQuery, useCreateProviderMutation, getErrorMessage } from "@/lib/store";

// Get logs with automatic caching
const { data: logs, error, isLoading } = useGetLogsQuery({ filters, pagination });

// Configure provider with optimistic updates
const [createProvider] = useCreateProviderMutation();

const handleCreate = async () => {
	try {
		await createProvider({
			provider: "openai",
			keys: [{ value: "sk-...", models: ["gpt-4"], weight: 1 }],
			// ... other config
		}).unwrap();
		// Success handling
	} catch (error) {
		console.error(getErrorMessage(error));
	}
};
```

### Component Guidelines

- **Composition**: Use Radix UI primitives for accessibility
- **Styling**: Tailwind CSS with CSS variables for theming
- **Types**: Full TypeScript coverage matching Go backend schemas
- **Error Handling**: Consistent error states and user feedback

### Adding New Features

1. **Backend Integration**: Add API endpoints to RTK Query slices in `lib/store/`
2. **Type Definitions**: Update types in `lib/types/`
3. **UI Components**: Build with Radix UI and Tailwind
4. **State Management**: Use RTK Query for API state, React hooks for local state
5. **Validation**: Run typecheck and production build

## Configuration

### Provider Setup

The UI supports comprehensive provider configuration including API keys with model assignments, network settings, and provider-specific options.

**[Complete Provider Configuration Guide →](https://docs.getbifrost.ai/quickstart/gateway/provider-configuration)**

### Access Control

Configure virtual keys and provider access through the UI.

## Monitoring & Analytics

The dashboard provides comprehensive observability including request metrics, token usage tracking, provider performance analysis, error categorization, and historical trend analysis.

**[Performance Benchmarks →](https://docs.getbifrost.ai/benchmarking/getting-started)**

## Contributing

We welcome contributions! See our [Contributing Guide](https://docs.getbifrost.ai/contributing/setting-up-repo) for:

- Code conventions and style guide
- Development setup and workflow
- Adding new providers or features
- Plugin development guidelines

## Documentation

**Complete Documentation:** [https://docs.getbifrost.ai](https://docs.getbifrost.ai)

### Quick Links

- [Gateway Setup](https://docs.getbifrost.ai/quickstart/gateway/setting-up) - Get started in 30 seconds
- [Provider Configuration](https://docs.getbifrost.ai/quickstart/gateway/provider-configuration) - Multi-provider setup
- [Architecture](https://docs.getbifrost.ai/architecture) - System design and internals

## Need Help?

**[Join our Discord](https://discord.gg/exN5KAydbU)** for community support and discussions.

Get help with:

- Quick setup assistance and troubleshooting
- Best practices and configuration tips
- Community discussions and support
- Real-time help with integrations

## Links

- **Main Repository**: [github.com/maximhq/bifrost](https://github.com/maximhq/bifrost)
- **HTTP Transport**: [../transports/bifrost-http](../transports/bifrost-http)
- **Documentation**: [docs.getbifrost.ai](https://docs.getbifrost.ai)
- **Website**: [getbifrost.ai](https://www.getbifrost.ai)

## License

Licensed under the Apache 2.0 License - see the [LICENSE](../LICENSE) file for details.

---

Built with ❤️ by [Maxim](https://github.com/maximhq)
