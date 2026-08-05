package lamigrate

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	defaultTableName   = "migrations"
	defaultLockTimeout = 30 * time.Second
	// defaultMaxFileSize is defined in file_validate.go.
	maxLockTimeout  = 24 * time.Hour
	maxTableNameLen = 64
)

var validTableName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// NewMySQL validates options and clones the mysql.Config. It does NOT
// connect to MySQL, create tables, or write to stdout/stderr.
//
// The supplied config is defensively cloned so later caller changes
// cannot alter runtime behaviour.
func NewMySQL(config *mysql.Config, opts Options) (*Migrator, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: mysql.Config is nil", ErrInvalidConfig)
	}

	// Validate and apply defaults to options.
	validated, err := validateOptions(opts)
	if err != nil {
		return nil, err
	}

	// Clone the DSN from the config so we own it.
	dsn := config.FormatDSN()

	// Parse back to verify the config round-trips and enable required
	// settings on the clone.
	clone, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot parse config DSN: %v", ErrInvalidConfig, err)
	}

	// Enforce required driver settings.
	clone.MultiStatements = true
	clone.ParseTime = true

	// Re-parse after mutations so FormatDSN picks them up.
	dsn = clone.FormatDSN()

	return &Migrator{
		dsn:                 dsn,
		config:              clone,
		directory:           validated.Directory,
		legacyDir:           validated.LegacyDir,
		tableName:           validated.TableName,
		lockTime:            validated.LockTimeout,
		maxFile:             validated.MaxFileSize,
		ignoreMissingSource: validated.IgnoreMissingSource,
		ignoreChecksumDrift:  validated.IgnoreChecksumDrift,
	}, nil
}

// OpenMySQL parses a DSN and delegates to [NewMySQL]. DSN parsing
// performs no network or database I/O.
func OpenMySQL(dsn string, opts Options) (*Migrator, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("%w: DSN is empty", ErrInvalidConfig)
	}

	// Parse the DSN to get a mysql.Config. No network I/O.
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: parse DSN: %v", ErrInvalidConfig, err)
	}

	return NewMySQL(config, opts)
}

// validateOptions applies defaults and validates all Options fields.
// Returns a copy with defaults filled in.
func validateOptions(opts Options) (Options, error) {
	// Directory — required, trimmed.
	opts.Directory = strings.TrimSpace(opts.Directory)
	if opts.Directory == "" {
		return opts, fmt.Errorf("%w: Directory must not be empty", ErrInvalidConfig)
	}

	// TableName — default "migrations", lowercase ASCII only.
	opts.TableName = strings.TrimSpace(opts.TableName)
	if opts.TableName == "" {
		opts.TableName = defaultTableName
	}
	if len(opts.TableName) > maxTableNameLen {
		return opts, fmt.Errorf("%w: TableName exceeds %d characters", ErrInvalidConfig, maxTableNameLen)
	}
	if !validTableName.MatchString(opts.TableName) {
		return opts, fmt.Errorf("%w: TableName %q must match [a-z][a-z0-9_]*", ErrInvalidConfig, opts.TableName)
	}

	// LockTimeout — default 30s, max 24h.
	if opts.LockTimeout == 0 {
		opts.LockTimeout = defaultLockTimeout
	}
	if opts.LockTimeout < 0 {
		return opts, fmt.Errorf("%w: LockTimeout must be positive", ErrInvalidConfig)
	}
	if opts.LockTimeout > maxLockTimeout {
		return opts, fmt.Errorf("%w: LockTimeout must not exceed %v", ErrInvalidConfig, maxLockTimeout)
	}

	// MaxFileSize — default 1MB, must be positive.
	if opts.MaxFileSize == 0 {
		opts.MaxFileSize = defaultMaxFileSize
	}
	if opts.MaxFileSize < 0 {
		return opts, fmt.Errorf("%w: MaxFileSize must be positive", ErrInvalidConfig)
	}

	return opts, nil
}