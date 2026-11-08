package store

import "fmt"

func (db *DB) Migrate() error {
	// C API prepares exactly one statement at a time.
	for _, sql := range []string{
		`CREATE TABLE IF NOT EXISTS models (slug TEXT PRIMARY KEY CHECK(length(slug) BETWEEN 2 AND 100), brand TEXT NOT NULL, name TEXT NOT NULL, device_id TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS assets (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('voicepack','firmware')), model_slug TEXT NOT NULL REFERENCES models(slug), title TEXT NOT NULL, version TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', region TEXT NOT NULL DEFAULT '', channel TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '', original_filename TEXT NOT NULL, storage_filename TEXT NOT NULL UNIQUE, size_bytes INTEGER NOT NULL CHECK(size_bytes > 0), sha256 TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')), created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_models_brand ON models(brand)`,
		`CREATE INDEX IF NOT EXISTS idx_assets_discovery ON assets(kind,status,model_slug,created_at)`,
	} {
		if err := db.Exec(sql); err != nil {
			return fmt.Errorf("migration: %w", err)
		}
	}
	return nil
}

// The initial catalog is illustrative, not a claim that a model is flash-compatible.
func (db *DB) Seed() error {
	rows := [][4]string{
		{"xiaomi-mi-robot", "Xiaomi", "Mi Robot Vacuum", "rockrobo.vacuum.v1"},
		{"roborock-s5", "Roborock", "Roborock S5", "roborock.vacuum.s5"},
		{"dreame-l10s-ultra", "Dreame", "Dreame L10s Ultra", ""},
	}
	for _, r := range rows {
		if err := db.Exec(`INSERT OR IGNORE INTO models(slug,brand,name,device_id) VALUES(?,?,?,?)`, r[0], r[1], r[2], r[3]); err != nil {
			return err
		}
	}
	return nil
}
