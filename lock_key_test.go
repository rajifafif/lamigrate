package lamigrate

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// --- Fixed test vectors (architecture §10.1) ---

func TestDeriveLockKeyFixedVector(t *testing.T) {
	// Exact vector: database="testdb", trackingTable="migrations"
	// scope = "lamigrate-lock-v1" || 0x00 || "testdb" || 0x00 || "migrations"
	// digest = SHA-256(scope), first 24 bytes hex-encoded.
	key, err := deriveLockKey("testdb", "migrations")
	if err != nil {
		t.Fatalf("deriveLockKey returned error: %v", err)
	}

	// Recompute expected key independently.
	scope := []byte("lamigrate-lock-v1\x00testdb\x00migrations")
	digest := sha256.Sum256(scope)
	expected := "lamigrate:v1:" + fmt.Sprintf("%x", digest[:lockKeyHexLen])

	if key != expected {
		t.Errorf("key = %q, want %q", key, expected)
	}
	if len(key) != lockKeyTotalLen {
		t.Errorf("key length = %d, want %d", len(key), lockKeyTotalLen)
	}

	// Cross-check: verify the key is exactly the known-good value
	// from the independent SHA-256 computation.
	knownGood := "lamigrate:v1:48e319dd30e64bd57456bd6ec20d2c971d49c8553b612221"
	if key != knownGood {
		t.Errorf("key = %q, known-good = %q", key, knownGood)
	}
}

func TestDeriveLockKeyFixedVectorMyapp(t *testing.T) {
	// Second vector: database="myapp_production", trackingTable="schema_migrations"
	key, err := deriveLockKey("myapp_production", "schema_migrations")
	if err != nil {
		t.Fatalf("deriveLockKey returned error: %v", err)
	}

	scope := []byte("lamigrate-lock-v1\x00myapp_production\x00schema_migrations")
	digest := sha256.Sum256(scope)
	expected := "lamigrate:v1:" + fmt.Sprintf("%x", digest[:lockKeyHexLen])

	if key != expected {
		t.Errorf("key = %q, want %q", key, expected)
	}

	knownGood := "lamigrate:v1:2acef00375b7a0305ed32c431ccef5a448617c004b6409f3"
	if key != knownGood {
		t.Errorf("key = %q, known-good = %q", key, knownGood)
	}
}

func TestDeriveLockKeyInvalidDatabase(t *testing.T) {
	cases := []struct {
		name     string
		database string
	}{
		{"empty", ""},
		{"starts with digit", "1db"},
		{"contains dash", "my-db"},
		{"contains space", "my db"},
		{"contains dot", "my.db"},
		{"non-ASCII", "über"},
		{"unicode emoji", "🚀db"},
		{"too long", strings.Repeat("a", 65)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := deriveLockKey(tc.database, "migrations")
			if err == nil {
				t.Errorf("deriveLockKey(%q, ...) = nil error, want error", tc.database)
			}
			if !strings.Contains(err.Error(), "database name") {
				t.Errorf("error = %v, should mention 'database name'", err)
			}
		})
	}
}

func TestDeriveLockKeyInvalidTable(t *testing.T) {
	cases := []struct {
		name  string
		table string
	}{
		{"empty", ""},
		{"starts with digit", "1migrations"},
		{"uppercase M", "Migrations"},
		{"mixed case", "MyTable"},
		{"contains dash", "my-table"},
		{"contains dot", "my.table"},
		{"non-ASCII", "über"},
		{"too long", strings.Repeat("a", 65)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := deriveLockKey("testdb", tc.table)
			if err == nil {
				t.Errorf("deriveLockKey(..., %q) = nil error, want error", tc.table)
			}
			if !strings.Contains(err.Error(), "tracking table name") {
				t.Errorf("error = %v, should mention 'tracking table name'", err)
			}
		})
	}
}

func TestDeriveLockKeyDeterministic(t *testing.T) {
	// Same input always produces the same key.
	key1, err := deriveLockKey("testdb", "migrations")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	key2, err := deriveLockKey("testdb", "migrations")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if key1 != key2 {
		t.Errorf("non-deterministic: %q != %q", key1, key2)
	}

	// Different inputs produce different keys.
	key3, err := deriveLockKey("testdb", "schema_migrations")
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if key1 == key3 {
		t.Errorf("different inputs produced same key: %q", key1)
	}

	key4, err := deriveLockKey("otherdb", "migrations")
	if err != nil {
		t.Fatalf("fourth call: %v", err)
	}
	if key1 == key4 {
		t.Errorf("different databases produced same key: %q", key1)
	}
}

func TestBootstrapKeyDifferentFromScopeLock(t *testing.T) {
	database := "testdb"

	bsKey, err := bootstrapLockKey(database)
	if err != nil {
		t.Fatalf("bootstrapLockKey: %v", err)
	}

	// Bootstrap key must differ from any valid scope lock key.
	validTables := []string{"migrations", "schema_migrations", "tracks"}
	for _, table := range validTables {
		scopeKey, err := deriveLockKey(database, table)
		if err != nil {
			t.Fatalf("deriveLockKey(%q, %q): %v", database, table, err)
		}
		if bsKey == scopeKey {
			t.Errorf(
				"bootstrap key equals scope key for table %q: %q",
				table, bsKey,
			)
		}
	}

	// Verify bootstrap key length.
	if len(bsKey) != lockKeyTotalLen {
		t.Errorf("bootstrap key length = %d, want %d", len(bsKey), lockKeyTotalLen)
	}

	// Verify the known-good bootstrap vector.
	expectedBS := "lamigrate:v1:25ef5445d87a2128885808925126270b898d80bb4155fc29"
	if bsKey != expectedBS {
		t.Errorf("bootstrap key = %q, want %q", bsKey, expectedBS)
	}
}

func TestDeriveLockKeyLength(t *testing.T) {
	cases := []struct {
		db    string
		table string
	}{
		{"a", "b"},
		{"testdb", "migrations"},
		{"myapp_production", "schema_migrations"},
		{strings.Repeat("a", 64), strings.Repeat("b", 64)}, // max length
	}

	for _, tc := range cases {
		t.Run(tc.db+"/"+tc.table, func(t *testing.T) {
			key, err := deriveLockKey(tc.db, tc.table)
			if err != nil {
				t.Fatalf("deriveLockKey: %v", err)
			}
			if len(key) != lockKeyTotalLen {
				t.Errorf("key length = %d, want %d", len(key), lockKeyTotalLen)
			}
			if !strings.HasPrefix(key, lockProtocolVersion) {
				t.Errorf("key %q does not start with %q", key, lockProtocolVersion)
			}
		})
	}
}

func TestDeriveLockKeyMaxBoundary(t *testing.T) {
	// Verify exactly 64-byte database and table names work.
	db := strings.Repeat("a", 64)
	table := strings.Repeat("b", 64)
	key, err := deriveLockKey(db, table)
	if err != nil {
		t.Fatalf("deriveLockKey with 64-byte names: %v", err)
	}
	if len(key) != lockKeyTotalLen {
		t.Errorf("key length = %d, want %d", len(key), lockKeyTotalLen)
	}

	// 65-byte names must fail.
	_, err = deriveLockKey(strings.Repeat("a", 65), table)
	if err == nil {
		t.Error("expected error for 65-byte database name")
	}
	_, err = deriveLockKey(db, strings.Repeat("b", 65))
	if err == nil {
		t.Error("expected error for 65-byte table name")
	}
}

func TestDeriveLockKeyUpperCaseDatabase(t *testing.T) {
	// Uppercase is allowed in database names.
	key, err := deriveLockKey("TestDB", "migrations")
	if err != nil {
		t.Fatalf("deriveLockKey(TestDB, migrations): %v", err)
	}
	if len(key) != lockKeyTotalLen {
		t.Errorf("key length = %d, want %d", len(key), lockKeyTotalLen)
	}

	// Should differ from lowercase version.
	keyLower, err := deriveLockKey("testdb", "migrations")
	if err != nil {
		t.Fatalf("deriveLockKey(testdb, migrations): %v", err)
	}
	if key == keyLower {
		t.Error("uppercase and lowercase database names produce same key")
	}
}

func TestDeriveLockKeyUnderscoreFirst(t *testing.T) {
	// Underscore is allowed as first character.
	key, err := deriveLockKey("_test", "_migrations")
	if err != nil {
		t.Fatalf("deriveLockKey(_test, _migrations): %v", err)
	}
	if len(key) != lockKeyTotalLen {
		t.Errorf("key length = %d, want %d", len(key), lockKeyTotalLen)
	}
}
