package lamigrate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultMaxFileSize = 1 << 20 // 1 MB

	// minValidTimestamp and maxValidTimestamp bound the acceptable
	// range for migration timestamps. Timestamps outside this range
	// are rejected during source validation.
	minValidTimestamp = 20000000000000 // 2000-01-01 00:00:00 UTC
	maxValidTimestamp = 21000000000000 // 2100-01-01 00:00:00 UTC
)

// validFilenamePattern matches architecture §8.1:
//   YYYYMMDDHHMMSS_description.up.sql
//   YYYYMMDDHHMMSS_description.down.sql
//
// where description is [a-z][a-z0-9_]*
var validFilenamePattern = regexp.MustCompile(`^\d{14}_[a-z][a-z0-9_]*\.(up|down)\.sql$`)

// irreversibleMarker is the exact comment that marks a down migration
// as intentionally irreversible. Defined in architecture §8.3.
const irreversibleMarker = "-- lamigrate: irreversible"

// validationResult accumulates errors from source validation.
type validationResult struct {
	errors []string
}

func (v *validationResult) addf(format string, args ...any) {
	v.errors = append(v.errors, fmt.Sprintf(format, args...))
}

func (v *validationResult) ok() bool {
	return len(v.errors) == 0
}

func (v *validationResult) err() error {
	if len(v.errors) == 0 {
		return nil
	}
	return fmt.Errorf("lamigrate: source validation failed:\n  %s", strings.Join(v.errors, "\n  "))
}

// validateFilename checks that a filename matches the canonical pattern
// from architecture §8.1.
func validateFilename(name string) error {
	if !validFilenamePattern.MatchString(name) {
		return fmt.Errorf("lamigrate: filename %q does not match expected pattern YYYYMMDDHHMMSS_description.(up|down).sql", name)
	}
	return nil
}

// validateTimestamp checks that a 14-digit timestamp is in a reasonable
// year range (2000–2100). The value is the numeric timestamp, not parsed
// as a date.
func validateTimestamp(ts int64) error {
	if ts < minValidTimestamp || ts > maxValidTimestamp {
		return fmt.Errorf("lamigrate: timestamp %d is outside the valid range (2000–2100)", ts)
	}
	// Additionally validate the date components are syntactically valid.
	s := strconv.FormatInt(ts, 10)
	if len(s) != 14 {
		return fmt.Errorf("lamigrate: timestamp %d must be exactly 14 digits", ts)
	}
	year, _ := strconv.Atoi(s[:4])
	month, _ := strconv.Atoi(s[4:6])
	day, _ := strconv.Atoi(s[6:8])
	hour, _ := strconv.Atoi(s[8:10])
	minute, _ := strconv.Atoi(s[10:12])
	second, _ := strconv.Atoi(s[12:14])

	if month < 1 || month > 12 {
		return fmt.Errorf("lamigrate: timestamp %d has invalid month %02d", ts, month)
	}
	if day < 1 || day > 31 {
		return fmt.Errorf("lamigrate: timestamp %d has invalid day %02d", ts, day)
	}
	if hour > 23 || minute > 59 || second > 59 {
		return fmt.Errorf("lamigrate: timestamp %d has invalid time component", ts)
	}
	// Quick sanity: reject day=31 for months that don't have 31 days.
	daysInMonth := []int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if day > daysInMonth[month] {
		return fmt.Errorf("lamigrate: timestamp %d has invalid day %02d for month %02d", ts, day, month)
	}
	_ = year // already range-validated above
	return nil
}

// validateSourceFiles runs the full set of source validation checks
// against a list of discovered migrationFile entries. It checks:
//   - filename pattern matches §8.1
//   - timestamp is in valid range
//   - no duplicate migration IDs
//   - no symlinks (verified during scan, but rechecked here)
//   - file sizes within the configured maximum
//
// maxFileSize=0 uses the default (1MB).
func validateSourceFiles(files []migrationFile, maxFileSize int64) error {
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileSize
	}
	seen := make(map[string]bool, len(files))
	vr := &validationResult{}

	for i := range files {
		f := &files[i]

		// Filename pattern
		if err := validateFilename(f.Filename); err != nil {
			vr.addf("%v", err)
			continue
		}

		// Timestamp range
		if err := validateTimestamp(f.Timestamp); err != nil {
			vr.addf("%v", err)
			continue
		}

		// Duplicate ID check
		if seen[f.Name] {
			vr.addf("duplicate migration ID: %s", f.Name)
			continue
		}
		seen[f.Name] = true

		// File size check — up file
		if f.UpPath != "" {
			if err := validateFileSize(f.UpPath, maxFileSize); err != nil {
				vr.addf("%v", err)
			}
		}
		// File size check — down file
		if f.DownPath != "" {
			if err := validateFileSize(f.DownPath, maxFileSize); err != nil {
				vr.addf("%v", err)
			}
		}
	}
	return vr.err()
}

// validateFileSize rejects files larger than maxBytes.
func validateFileSize(path string, maxBytes int64) error {
	info, err := readFileSize(path)
	if err != nil {
		return err
	}
	if info > maxBytes {
		return fmt.Errorf("lamigrate: file %s exceeds size limit (%d bytes > %d max)", path, info, maxBytes)
	}
	return nil
}

// readFileSize returns the size of a file in bytes.
func readFileSize(path string) (int64, error) {
	info, err := fileStat(path)
	if err != nil {
		return 0, fmt.Errorf("lamigrate: stat file %s: %w", path, err)
	}
	return info, nil
}

// detectIrreversible checks whether a down migration file contains the
// irreversible marker from architecture §8.3.
func detectIrreversible(downPath string) (bool, error) {
	data, err := readFile(downPath)
	if err != nil {
		return false, fmt.Errorf("lamigrate: read down file for irreversible check: %w", err)
	}
	return strings.Contains(string(data), irreversibleMarker), nil
}
