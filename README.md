# Orchestra Sync Cloud Plugin

Cloud sync plugin for [Orchestra MCP](https://orchestra-mcp.dev) — push local projects, features, notes, and plans to the Orchestra web dashboard for team visibility.

## Install

```bash
go get github.com/orchestra-mcp/plugin-sync-cloud
```

## Usage

```bash
# Build
go build -o bin/sync-cloud ./cmd/

# Run (started automatically by the orchestrator)
bin/sync-cloud --workspace /path/to/project --orchestrator-addr localhost:9100
```

## MCP Tools

| Tool | Description |
|------|-------------|
| `orchestra_login` | Login to Orchestra cloud (email/password, supports 2FA) |
| `orchestra_logout` | Logout and stop automatic sync |
| `sync_status` | Show auth status, last sync time, pending changes, config |
| `sync_now` | Trigger an immediate sync of all local changes |
| `sync_config` | Configure auto-sync, polling interval, cloud API URL |

## Authentication

Credentials are stored locally at `~/.orchestra/auth.json`. Two methods:

1. **Email/Password** — via `orchestra_login` tool
2. **Token** — set `ORCHESTRA_TOKEN` environment variable

## How It Works

1. Reads local data via the storage plugin (SQLite or markdown)
2. Compares with the cloud dashboard state
3. Pushes new/updated entities (projects, features, persons, plans, notes)
4. Runs automatically in the background when auto-sync is enabled

```
Local (SQLite/Markdown) ──push──> Orchestra Cloud Dashboard
                                  https://orchestra-mcp.dev
```

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `auto_sync` | `false` | Enable automatic background sync |
| `poll_interval` | `60s` | How often to check for local changes |
| `api_url` | `https://orchestra-mcp.dev/api` | Cloud API endpoint |

Override the API URL with `ORCHESTRA_API_URL` environment variable.

## Related Packages

| Package | Description |
|---------|-------------|
| [sdk-go](https://github.com/orchestra-mcp/sdk-go) | Plugin SDK |
| [plugin-storage-sqlite](https://github.com/orchestra-mcp/plugin-storage-sqlite) | SQLite storage (source of truth) |
| [plugin-tools-features](https://github.com/orchestra-mcp/plugin-tools-features) | Feature management tools |
| [cli](https://github.com/orchestra-mcp/cli) | Orchestra CLI |

## License

[MIT](LICENSE)
