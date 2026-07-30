package lamigrate

import (
	"context"
	"fmt"
)

// importLegacy reads numbered migration files and marks them as applied in batch 0.
// It does NOT execute any SQL — it only records them as already applied.
func (m *Migrate) importLegacy(ctx context.Context) error {
	files, err := scanLegacyMigrations(m.dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("No legacy numbered migration files found.")
		return nil
	}

	applied, err := m.allAppliedSet(ctx)
	if err != nil {
		return err
	}

	imported := 0
	for _, f := range files {
		if applied[f.Name] {
			fmt.Printf("Already tracked: %s\n", f.Name)
			continue
		}
		if err := m.recordMigration(ctx, f.Name, 0); err != nil {
			return err
		}
		fmt.Printf("Imported: %s\n", f.Name)
		imported++
	}
	fmt.Printf("Imported %d legacy migration(s) as already applied (batch 0).\n", imported)
	return nil
}
