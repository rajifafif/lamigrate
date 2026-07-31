package lamigrate

import "context"

// Make creates a new migration file pair. It is an offline operation
// that requires no DSN or database connection.
//
// Make delegates to [CreateMigration] and wraps it in the new public API.
func Make(ctx context.Context, directory, name string) (CreatedMigration, error) {
	// Validate inputs before touching the filesystem.
	if directory == "" {
		return CreatedMigration{}, ErrInvalidConfig
	}
	if name == "" {
		return CreatedMigration{}, ErrInvalidConfig
	}

	return CreateMigration(directory, name)
}
