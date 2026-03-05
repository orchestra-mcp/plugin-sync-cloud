### V1.0.0

- Initial release
- SQLite storage with WAL mode and pure Go driver (modernc.org/sqlite)
- Path-based routing to 13 typed SQL tables + KV fallback
- Compare-and-swap (CAS) optimistic concurrency
- Dual-write: SQLite source of truth + async markdown export for git
- Auto-migration from existing `.projects/` markdown on first run
- Workspace-scoped databases at `~/.orchestra/db/<hash>.db`
- Global workspace index for cross-workspace discovery