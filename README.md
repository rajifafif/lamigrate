# lamigrate

Laravel-style database migrations for Go + MySQL.

Timestamp-based filenames, batch-tracked rollback, pretend mode — the workflow you know from `php artisan migrate`, now in Go.

## Features

- **Timestamp filenames** — `20260730094235_create_users.up.sql` (no collision, sortable)
- **Batch tracking** — rollback by batch, not by version number
- **Pretend mode** — see SQL before executing (`--pretend`)
- **Legacy import** — one-command migration from numbered files (000001-style)
- **Zero deps** — only `go-sql-driver/mysql`
- **Standalone CLI** or **library** — use however you want

## Install

```bash
go install github.com/lamigrate/lamigrate/cmd/lamigrate@latest
```

## CLI

```bash
lamigrate -dsn "user:pass@tcp(host:3306)/dbname" <command>
```

| Command | Description |
|---------|-------------|
| `up [N]` | Apply next N pending migrations (all if omitted) |
| `down [N]` | Rollback N from last batch (all in batch if omitted) |
| `reset` | Rollback ALL migrations |
| `status` | Show applied vs pending |
| `make <name>` | Create new migration file pair |
| `import` | Import legacy numbered files as already applied |

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-dir` | `sql/migrations` | Migrations directory |
| `-dsn` | — | MySQL DSN (or `LAMIGRATE_DSN` env) |
| `-table` | `migrations` | Tracking table name |
| `-pretend` | false | Show SQL without executing |

### Examples

```bash
# Apply all pending
lamigrate -dsn "user:pass@tcp(localhost:3306)/mydb" up

# Rollback last batch
lamigrate -dsn "..." down

# Dry run
lamigrate -dsn "..." -pretend down

# See status
lamigrate -dsn "..." status

# Create new migration
lamigrate -dsn "..." make create_users_table

# Import from golang-migrate numbered files
lamigrate -dsn "..." import
```

## Library

```go
import "github.com/lamigrate/lamigrate"

m, err := lamigrate.New("sql/migrations", "user:pass@tcp(host:3306)/dbname")
if err != nil {
    log.Fatal(err)
}
defer m.Close()

// Apply all pending
m.Up(ctx)

// Apply next 3
m.Up(ctx, 3)

// Rollback last batch
m.Down(ctx)

// Rollback 2 from last batch
m.Down(ctx, 2)

// Rollback everything
m.Reset(ctx)

// Status
statuses, _ := m.Status(ctx)
for _, s := range statuses {
    fmt.Printf("%s  %s\n", s.Filename, map[bool]string{true: "APPLIED", false: "PENDING"}[s.Applied])
}

// Create new migration files
base, _ := m.Make("create_users_table")
fmt.Println(base) // 20260730094235_create_users_table

// Import legacy numbered files (one-time)
m.ImportLegacy(ctx)
```

## File Format

```
sql/migrations/
├── 20260730094235_create_users.up.sql
├── 20260730094235_create_users.down.sql
├── 20260730102802_add_email_index.up.sql
└── 20260730102802_add_email_index.down.sql
```

- Filename: `YYYYMMDDHHMMSS_description.up.sql` / `.down.sql`
- Timestamp determines execution order
- No duplicate timestamps (creation time)

## Tracking Table

```sql
CREATE TABLE migrations (
    id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration  VARCHAR(255) NOT NULL,
    batch      INT UNSIGNED NOT NULL,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_migrations_migration (migration)
);
```

Created automatically on first run. Configurable via `-table` flag.

## Migrating from golang-migrate

1. Run `lamigrate -dsn "..." import` — marks all numbered files as already applied (batch 0)
2. Rename your next migration to timestamp format: `YYYYMMDDHHMMSS_description.up.sql`
3. Use `lamigrate` from now on
4. Old `schema_migrations` table is untouched — you can drop it later

## License

MIT
