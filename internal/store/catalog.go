package store

import "fmt"

// Statement is a parameterized SQL statement executed by Batch atomically.
type Statement struct {
	SQL  string
	Args []any
}

// Batch holds the single SQLite connection lock for the full transaction.
// A failed statement rolls back all changes, including catalog imports.
func (db *DB) Batch(statements []Statement) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.execLocked("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	for i, statement := range statements {
		if err := db.execLocked(statement.SQL, statement.Args...); err != nil {
			_ = db.execLocked("ROLLBACK")
			return fmt.Errorf("batch statement %d: %w", i+1, err)
		}
	}
	if err := db.execLocked("COMMIT"); err != nil {
		_ = db.execLocked("ROLLBACK")
		return fmt.Errorf("batch commit: %w", err)
	}
	return nil
}
