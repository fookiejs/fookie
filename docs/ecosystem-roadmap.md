# Ecosystem Roadmap

## CLI

- `fookie start|stop|status|logs` as primary UX contract
- `fookie doctor` for local dependency checks
- `fookie config` for runtime profile visibility
- `fookie upgrade` as release-guided upgrade entrypoint

## TypeScript SDK

- Package: `@fookie/worker-sdk`
- Scope:
  - GraphQL client wrapper
  - Worker loop helper
  - Shared request/response types

## FQL Extension

- Syntax highlighting for `.fql`
- Snippets for model/seed/cron scaffolding
- Next steps:
  - diagnostics integration
  - hover docs for builtin calls
