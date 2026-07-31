package lamigrate

import (
	"crypto/sha256"
	"fmt"
	"regexp"
)

const (
	// lockProtocolVersion is the canonical version prefix for all lock keys.
	// Permanent for lock protocol v1 (architecture §10.1).
	lockProtocolVersion = "lamigrate:v1:"

	// lockProtocolScope is the fixed scope prefix in the hash input.
	// This string is permanent for v1; changing it produces different keys.
	lockProtocolScope = "lamigrate-lock-v1"

	// lockKeyHexLen is the number of hex characters from the SHA-256 digest
	// used in the final key (24 bytes = 192 bits).
	lockKeyHexLen = 24

	// lockKeyTotalLen is the exact character length of a v1 lock key.
	// "lamigrate:v1:" (14) + 48 hex chars = 62. Wait, let me recount.
	// "lamigrate:v1:" = l-a-m-i-g-r-a-t-e-:-v-1-: = 13 chars
	// + 48 hex chars = 61 chars total.
	lockKeyTotalLen = 61

	// maxIdentifierLen is the MySQL maximum identifier length.
	maxIdentifierLen = 64
)

// validDatabaseName matches the ASCII database-name domain: [A-Za-z_][A-Za-z0-9_]*,
// max 64 bytes (architecture §9, §10.1).
var validDatabaseName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validTrackingTable matches the lowercase tracking-table domain: [a-z_][a-z0-9_]*,
// max 64 bytes (architecture §9).
var validTrackingTable = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// deriveLockKey computes the v1 advisory lock key.
//
// scope bytes = UTF-8("lamigrate-lock-v1") || 0x00 || UTF-8(database) || 0x00 || UTF-8(tracking-table)
// digest      = SHA-256(scope bytes)
// lock key    = "lamigrate:v1:" + lowercase_hex(digest[0:24])
//
// Returns a 61-character key providing a 192-bit digest within MySQL's
// 64-character lock-name limit. Returns error for invalid database or
// tracking-table names.
func deriveLockKey(database, trackingTable string) (string, error) {
	// Validate database name.
	if database == "" {
		return "", fmt.Errorf(
			"%w: database name must not be empty",
			ErrInvalidConfig,
		)
	}
	if len(database) > maxIdentifierLen {
		return "", fmt.Errorf(
			"%w: database name %q exceeds %d bytes",
			ErrInvalidConfig, database, maxIdentifierLen,
		)
	}
	if !validDatabaseName.MatchString(database) {
		return "", fmt.Errorf(
			"%w: database name %q must match [A-Za-z_][A-Za-z0-9_]*",
			ErrInvalidConfig, database,
		)
	}

	// Validate tracking table name.
	if trackingTable == "" {
		return "", fmt.Errorf(
			"%w: tracking table name must not be empty",
			ErrInvalidConfig,
		)
	}
	if len(trackingTable) > maxIdentifierLen {
		return "", fmt.Errorf(
			"%w: tracking table name %q exceeds %d bytes",
			ErrInvalidConfig, trackingTable, maxIdentifierLen,
		)
	}
	if !validTrackingTable.MatchString(trackingTable) {
		return "", fmt.Errorf(
			"%w: tracking table name %q must match [a-z_][a-z0-9_]*",
			ErrInvalidConfig, trackingTable,
		)
	}

	return computeLockKey(database, trackingTable), nil
}

// computeLockKey computes the v1 lock key from pre-validated inputs.
// This is the raw computation shared by deriveLockKey and
// bootstrapLockKey. It performs no input validation.
func computeLockKey(database, trackingComponent string) string {
	// Build scope bytes per architecture §10.1.
	// scope = "lamigrate-lock-v1" || 0x00 || database || 0x00 || trackingComponent
	scope := make([]byte, 0, len(lockProtocolScope)+1+len(database)+1+len(trackingComponent))
	scope = append(scope, lockProtocolScope...)
	scope = append(scope, 0x00)
	scope = append(scope, database...)
	scope = append(scope, 0x00)
	scope = append(scope, trackingComponent...)

	// SHA-256 digest.
	digest := sha256.Sum256(scope)

	// Take first 24 bytes (192 bits) and hex-encode.
	return lockProtocolVersion + fmt.Sprintf("%x", digest[:lockKeyHexLen])
}
