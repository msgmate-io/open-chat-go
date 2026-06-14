package admin

// Admin GORM Introspection APIs
//
// @doc:open-chat-admin-gorm-apis-overview
// These admin endpoints expose runtime GORM metadata and controlled table access for debugging,
// schema understanding, and internal ops tooling.
//
// Access model:
// - All endpoints under `/api/v1/admin/*` require an authenticated admin user.
// - Handlers consistently call `util.GetDBAndUser(r)` and enforce `user.IsAdmin`.
// - Snapshot refresh/write endpoints in `DocsSnapshots.go` are additionally gated to debug runtime.
//
// Source of truth for exposed models:
// - The handlers iterate over `database.Tabels` (project model registry).
// - Table names come from parsed GORM schema (`gorm.Statement{DB: DB}.Parse(model)`).
// - This prevents arbitrary table access; only registered models are accessible.
//
// Main endpoint groups:
// - Discovery:
//   - GET `/api/v1/admin/tables` -> list registered table names.
//   - GET `/api/v1/admin/table/{table_name}` -> table field metadata.
//   - GET `/api/v1/admin/schema/sql` -> generated SQL + inferred relations.
// - Data access:
//   - GET `/api/v1/admin/table/{table_name}/data?page=&limit=` -> paginated rows.
//   - GET `/api/v1/admin/table/{table_name}/{id}` -> single row (+ configured preloads).
// - Mutations:
//   - DELETE `/api/v1/admin/table/{table_name}/{id}` -> delete one row by id.
//   - DELETE `/api/v1/admin/delete_all_entries/{table_name}` -> delete all rows in table.
//
// Response behavior highlights:
// - `GetTableInfo` supports `?full=1` for exhaustive field schema details.
// - `GetTableDataPaginated` and `GetUsersWithDetails` use `database.Pagination`.
// - `GetTableItemById` returns 410 when a record exists but is soft-deleted.
// - `tableConfigurations` controls include-lists, preloads, and JSON field handling.

// Admin Documentation Tag API
//
// @doc:open-chat-admin-doc-tags
// Use `GET /api/v1/admin/docs/tag/{tag}` to fetch documentation snippets embedded in Go comments.
//
// Tag extraction details:
// - Implemented in `GetCodeDocByTag.go`.
// - Scanner walks backend `.go` files and parses comment groups.
// - Tags are discovered via `@doc:<tag-name>`.
// - Returned payload includes:
//   - `tag`
//   - `content`
//   - `source_url` (GitHub line link)
//
// This mechanism is intended for docs pages that need server-authored, code-adjacent explanations.

// Admin GORM Operations Checklist
//
// @doc:open-chat-admin-gorm-ops-checklist
// Recommended flow for safe admin debugging:
// 1. Discover available tables with `GET /api/v1/admin/tables`.
// 2. Inspect schema with `GET /api/v1/admin/table/{table_name}?full=1`.
// 3. Page through rows via `GET /api/v1/admin/table/{table_name}/data`.
// 4. Inspect specific records using `GET /api/v1/admin/table/{table_name}/{id}`.
// 5. Prefer single-row deletes over bulk deletes where possible.
// 6. Reserve `DELETE /api/v1/admin/delete_all_entries/{table_name}` for controlled dev/ops scenarios.
