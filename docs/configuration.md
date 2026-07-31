# Configuration Reference

lamigrate supports multiple configuration sources for database connection parameters. This document covers the complete configuration contract.

> **Status: experimental, pre-1.0.** The configuration policy is defined in
> LM-003 and implemented in LM-013. Production deployment guidance requires
> the production architecture (see [architecture.md](../architecture.md)).

## Precedence Order

Configuration is resolved in this order. The first non-empty value wins:

1. **`-dsn` flag** -- highest priority. Warns about shell history exposure.
2. **`LAMIGRATE_DSN` environment variable.**
3. **`-config` / `--config` flag** -- explicit path to a config file.
4. **Default search** in the current working directory, tried in order:
   - `config.yaml`
   - `config.yml`
   - `.env`

If none of these provide a DSN, lamigrate exits with an error.

**Offline commands** (`migration create`, `make`, `make:migration`, `version`, `help`) never read configuration files and do not require a DSN.

## YAML Configuration

### File Format

```yaml
dbMySQL:
  host: localhost
  port: 3306
  user: migration_user
  pass: your_password
  dbName: my_application
  timeout: 30s
```

### Field Reference

| Field | Type | Default | Required | Description |
|-------|------|---------|----------|-------------|
| `dbMySQL.host` | string | -- | yes | MySQL server hostname or IP address |
| `dbMySQL.port` | int | `3306` | no | MySQL server port (1-65535) |
| `dbMySQL.user` | string | -- | yes | MySQL username |
| `dbMySQL.pass` | string | `""` | no | MySQL password (may be empty for passwordless auth) |
| `dbMySQL.dbName` | string | -- | yes | Target database name |
| `dbMySQL.timeout` | string | `"30s"` | no | Connection, read, and write timeout as a Go duration |

### Timeout Format

The timeout field accepts Go duration strings:

- `5s` -- 5 seconds
- `30s` -- 30 seconds (default)
- `60s` -- 60 seconds
- `1m` -- 1 minute
- `5m30s` -- 5 minutes 30 seconds

This value is used for the MySQL driver's `Timeout`, `ReadTimeout`, and `WriteTimeout` parameters. It must be a positive duration.

### YAML Parsing Rules

- The file must contain exactly one YAML document (multi-document files are rejected).
- Unknown fields are rejected (strict parsing via `KnownFields(true)`).
- Only `.yaml` and `.yml` extensions are recognized.

### DSN Generation

lamigrate does not accept a raw DSN string from the YAML file. Instead, it constructs a MySQL DSN from the individual fields using `go-sql-driver/mysql`. The generated DSN automatically enables:

- `parseTime=true` (robust time parsing)
- `multiStatements=true` (required for multi-statement migration files)
- TCP transport with the specified host and port

## Environment Variables (.env)

### .env File Format

```bash
# Database connection
LAMIGRATE_DB_HOST=localhost
LAMIGRATE_DB_PORT=3306
LAMIGRATE_DB_USER=migration_user
LAMIGRATE_DB_PASS=your_password
LAMIGRATE_DB_NAME=my_application
LAMIGRATE_DB_TIMEOUT=30s
```

### Direct DSN

A `.env` file may also contain a direct DSN, which takes precedence over individual fields:

```bash
LAMIGRATE_DSN="user:pass@tcp(localhost:3306)/dbname"
```

### .env Key Reference

Each database parameter has a fallback chain. The first non-empty key found is used:

| Parameter | Primary | Fallback 1 | Fallback 2 | Fallback 3 |
|-----------|---------|-----------|-----------|-----------|
| Host | `LAMIGRATE_DB_HOST` | `DB_MYSQL_HOST` | `DB_HOST` | -- |
| Port | `LAMIGRATE_DB_PORT` | `DB_MYSQL_PORT` | `DB_PORT` | -- |
| User | `LAMIGRATE_DB_USER` | `DB_MYSQL_USER` | `DB_USER` | -- |
| Password | `LAMIGRATE_DB_PASS` | `DB_MYSQL_PASS` | `DB_PASS` | `DB_PASSWORD` |
| Database | `LAMIGRATE_DB_NAME` | `DB_MYSQL_DB_NAME` | `DB_NAME` | `DB_DATABASE` |
| Timeout | `LAMIGRATE_DB_TIMEOUT` | `DB_MYSQL_TIMEOUT` | `DB_TIMEOUT` | -- |

### .env Parsing Rules

- Lines starting with `#` are comments.
- Empty lines are ignored.
- `export` prefix is stripped (e.g., `export KEY=value` works).
- Keys must contain only `[A-Za-z0-9_]` and start with a letter or underscore.
- Values can be:
  - Unquoted: `KEY=value` (trailing ` #comment` is stripped)
  - Single-quoted: `KEY='value'` (literal, no escaping)
  - Double-quoted: `KEY="value"` (supports Go escape sequences via `strconv.Unquote`)
- Maximum file size: 1 MB.

## Config File Discovery

When no `-config` flag is provided, lamigrate searches the current working directory for:

1. `config.yaml`
2. `config.yml`
3. `.env`

The first file that exists and is a regular file is used. Symlinks and non-regular files are rejected. If multiple config files exist, the one found first in the search order wins (i.e., `config.yaml` takes precedence over `config.yml` which takes precedence over `.env`).

### Explicit Path

Use `-config <path>` or `--config <path>` to specify an explicit config file path. This bypasses the default search entirely.

```bash
lamigrate -config /etc/myapp/db-config.yaml up
lamigrate -config .env.local up
```

## Security Guidance

### Credential Files

- **Never commit config files with real credentials** to version control.
- Add config files to `.gitignore`:
  ```gitignore
  config.yaml
  config.yml
  .env
  .env.*
  ```
- Provide an example file for contributors:
  ```bash
  cp config.yaml config.yaml.example
  # Edit config.yaml.example with placeholder values
  ```

### Shell History

The `-dsn` flag passes credentials on the command line, where they appear in shell history (`~/.bash_history`, etc.) and process listings (`ps aux`). The CLI emits a warning when `-dsn` is used directly.

**Recommended alternatives:**

```bash
# Option 1: Environment variable
export LAMIGRATE_DSN="user:pass@tcp(host:3306)/dbname"
lamigrate up

# Option 2: Config file
lamigrate -config config.yaml up
```

### File Permissions

Config files should be readable only by the owner:

```bash
chmod 600 config.yaml
chmod 600 .env
```

lamigrate reads config files but does not check or enforce POSIX permissions. The security boundary is the operating system's file permission model.

### Config File Type

Only regular files are accepted for configuration. Symlinks are rejected as a defense-in-depth measure against symlink-based path confusion.

### Maximum File Size

Configuration files must not exceed 1 MB. Files exceeding this limit are rejected before parsing.

## TLS Configuration

lamigrate uses the `go-sql-driver/mysql` driver, which supports TLS via DSN parameters. To use TLS, include the appropriate parameters in a direct DSN or environment variable:

```bash
# Full TLS with server certificate verification
LAMIGRATE_DSN="user:pass@tcp(host:3306)/dbname?tls=true"

# Custom CA certificate
LAMIGRATE_DSN="user:pass@tcp(host:3306)/dbname?tls=custom&tlsCAFile=/path/to/ca.pem"

# Skip certificate verification (development only, never use in production)
LAMIGRATE_DSN="user:pass@tcp(host:3306)/dbname?tls=skip-verify"
```

YAML and `.env` configurations use `go-sql-driver/mysql`'s DSN builder, which does not directly expose TLS parameters. For TLS with YAML/.env, use `LAMIGRATE_DSN` with the TLS parameters included.

> **Production guidance:** Always use authenticated TLS for remote MySQL
> connections. Plaintext connections should only be used on trusted local
> networks. See [architecture.md](../architecture.md) section 17 for
> production documentation requirements.

## Generated DSN Parameters

When a DSN is constructed from YAML or `.env` fields (rather than provided directly), lamigrate sets these MySQL driver parameters automatically:

| Parameter | Value | Reason |
|-----------|-------|--------|
| `parseTime` | `true` | Robust `DATETIME`/`TIMESTAMP` parsing |
| `multiStatements` | `true` | Required for multi-statement migration files |
| `net` | `tcp` | TCP transport |
| `Timeout` | from `timeout` field | Connection timeout |
| `ReadTimeout` | from `timeout` field | Read timeout |
| `WriteTimeout` | from `timeout` field | Write timeout |

## Example Configuration Files

### config.yaml (development)

```yaml
dbMySQL:
  host: localhost
  port: 3306
  user: root
  pass: ""
  dbName: myapp_dev
  timeout: 10s
```

### .env (development)

```bash
LAMIGRATE_DB_HOST=localhost
LAMIGRATE_DB_PORT=3306
LAMIGRATE_DB_USER=root
LAMIGRATE_DB_PASS=""
LAMIGRATE_DB_NAME=myapp_dev
LAMIGRATE_DB_TIMEOUT=10s
```

### .env with direct DSN

```bash
LAMIGRATE_DSN="root@tcp(localhost:3306)/myapp_dev?parseTime=true&multiStatements=true"
```

## Task Card Reference

The configuration policy and implementation are complete. The architecture requirements are in [architecture.md](../architecture.md) sections 15 and §5.8.
