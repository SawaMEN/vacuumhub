package store

import "fmt"

func (db *DB) Migrate() error {
	// C API prepares exactly one statement at a time.
	for _, sql := range []string{
		`CREATE TABLE IF NOT EXISTS models (slug TEXT PRIMARY KEY CHECK(length(slug) BETWEEN 2 AND 100), brand TEXT NOT NULL, name TEXT NOT NULL, device_id TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS assets (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('voicepack','firmware')), model_slug TEXT NOT NULL REFERENCES models(slug), title TEXT NOT NULL, version TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', region TEXT NOT NULL DEFAULT '', channel TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '', original_filename TEXT NOT NULL, storage_filename TEXT NOT NULL UNIQUE, size_bytes INTEGER NOT NULL CHECK(size_bytes > 0), sha256 TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')), created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS manufacturers (slug TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE COLLATE NOCASE, website TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS model_variants (
			id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 2 AND 100),
			model_slug TEXT NOT NULL REFERENCES models(slug) ON DELETE RESTRICT,
			sku TEXT NOT NULL DEFAULT '', region TEXT NOT NULL DEFAULT '', hardware_revision TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS variant_identifiers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			variant_id TEXT NOT NULL REFERENCES model_variants(id) ON DELETE CASCADE,
			kind TEXT NOT NULL CHECK(kind IN ('hardware_id','miot_model','sku','model_number','vendor')),
			value TEXT NOT NULL, source_url TEXT NOT NULL DEFAULT '',
			verification TEXT NOT NULL DEFAULT 'unverified' CHECK(verification IN ('verified','unverified')),
			UNIQUE(variant_id,kind,value)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_models_brand ON models(brand)`,
		`CREATE INDEX IF NOT EXISTS idx_variants_model_region ON model_variants(model_slug,region,hardware_revision)`,
		`CREATE INDEX IF NOT EXISTS idx_identifiers_lookup ON variant_identifiers(kind,value)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_verified_hardware_id ON variant_identifiers(value COLLATE NOCASE) WHERE kind='hardware_id' AND verification='verified'`,
		`CREATE TRIGGER IF NOT EXISTS invalidate_variant_identifiers_on_revision_change
         AFTER UPDATE OF region,hardware_revision ON model_variants
         WHEN OLD.region<>NEW.region OR OLD.hardware_revision<>NEW.hardware_revision
         BEGIN UPDATE variant_identifiers SET verification='unverified' WHERE variant_id=NEW.id; END`,
		`CREATE INDEX IF NOT EXISTS idx_assets_discovery ON assets(kind,status,model_slug,created_at)`,
		`CREATE TABLE IF NOT EXISTS firmware_events (id INTEGER PRIMARY KEY AUTOINCREMENT,asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,event TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_firmware_events_asset ON firmware_events(asset_id,id)`,
		`CREATE TABLE IF NOT EXISTS install_dry_runs (
			id TEXT PRIMARY KEY CHECK(length(id)=32),
			firmware_id TEXT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
			variant_id TEXT NOT NULL REFERENCES model_variants(id) ON DELETE RESTRICT,
			mode TEXT NOT NULL CHECK(mode='dry_run'),
			decision TEXT NOT NULL CHECK(decision='blocked'),
			report_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_install_dry_runs_created ON install_dry_runs(created_at,id)`,
		`CREATE TABLE IF NOT EXISTS admin_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			method TEXT NOT NULL CHECK(method IN ('POST','PATCH','PUT','DELETE')),
			path TEXT NOT NULL,
			status_code INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_audit_created ON admin_audit(created_at,id)`,
	} {
		if err := db.Exec(sql); err != nil {
			return fmt.Errorf("migration: %w", err)
		}
	}
	// Additive columns preserve existing assets for upgraded installations.
	existing := make(map[string]bool)
	cols, err := db.Query(`PRAGMA table_info(assets)`)
	if err != nil {
		return err
	}
	for _, col := range cols {
		existing[col["name"].(string)] = true
	}
	for _, column := range []struct{ name, sql string }{
		{"author", `ALTER TABLE assets ADD COLUMN author TEXT NOT NULL DEFAULT ''`},
		{"license", `ALTER TABLE assets ADD COLUMN license TEXT NOT NULL DEFAULT ''`},
		{"series_slug", `ALTER TABLE assets ADD COLUMN series_slug TEXT NOT NULL DEFAULT ''`},
		{"package_format", `ALTER TABLE assets ADD COLUMN package_format TEXT NOT NULL DEFAULT ''`},
		{"preview_storage_filename", `ALTER TABLE assets ADD COLUMN preview_storage_filename TEXT NOT NULL DEFAULT ''`},
		{"preview_sha256", `ALTER TABLE assets ADD COLUMN preview_sha256 TEXT NOT NULL DEFAULT ''`},
		{"preview_size_bytes", `ALTER TABLE assets ADD COLUMN preview_size_bytes INTEGER NOT NULL DEFAULT 0`},
		{"preview_mime", `ALTER TABLE assets ADD COLUMN preview_mime TEXT NOT NULL DEFAULT ''`},
		{"firmware_variant_id", `ALTER TABLE assets ADD COLUMN firmware_variant_id TEXT NOT NULL DEFAULT ''`},
		{"firmware_source_url", `ALTER TABLE assets ADD COLUMN firmware_source_url TEXT NOT NULL DEFAULT ''`},
		{"firmware_source_name", `ALTER TABLE assets ADD COLUMN firmware_source_name TEXT NOT NULL DEFAULT ''`},
		{"firmware_rights", `ALTER TABLE assets ADD COLUMN firmware_rights TEXT NOT NULL DEFAULT ''`},
		{"firmware_evidence_url", `ALTER TABLE assets ADD COLUMN firmware_evidence_url TEXT NOT NULL DEFAULT ''`},
		{"firmware_downgrade_policy", `ALTER TABLE assets ADD COLUMN firmware_downgrade_policy TEXT NOT NULL DEFAULT 'unknown'`},
		{"firmware_signer", `ALTER TABLE assets ADD COLUMN firmware_signer TEXT NOT NULL DEFAULT ''`},
		{"firmware_signature_hex", `ALTER TABLE assets ADD COLUMN firmware_signature_hex TEXT NOT NULL DEFAULT ''`},
		{"firmware_authenticity", `ALTER TABLE assets ADD COLUMN firmware_authenticity TEXT NOT NULL DEFAULT 'unverified'`},
		{"firmware_compatibility", `ALTER TABLE assets ADD COLUMN firmware_compatibility TEXT NOT NULL DEFAULT 'unverified'`},
		{"firmware_scan_status", `ALTER TABLE assets ADD COLUMN firmware_scan_status TEXT NOT NULL DEFAULT 'not_scanned'`},
		{"firmware_inspection", `ALTER TABLE assets ADD COLUMN firmware_inspection TEXT NOT NULL DEFAULT 'unchecked'`},
	} {
		if !existing[column.name] {
			if err := db.Exec(column.sql); err != nil {
				return fmt.Errorf("migration %s: %w", column.name, err)
			}
		}
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_voicepack_history ON assets(kind,model_slug,series_slug,language,status,created_at)`); err != nil {
		return err
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_voicepack_approved_version ON assets(model_slug,series_slug,language,version) WHERE kind='voicepack' AND status='approved' AND series_slug<>''`); err != nil {
		return err
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_firmware_approved_version ON assets(firmware_variant_id,version,channel) WHERE kind='firmware' AND status='approved' AND firmware_variant_id<>''`); err != nil {
		return err
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_firmware_variant_history ON assets(firmware_variant_id,channel,status,created_at)`); err != nil {
		return err
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
	for _, brand := range [][2]string{{"xiaomi", "Xiaomi"}, {"roborock", "Roborock"}, {"dreame", "Dreame"}, {"mova", "MOVA"}, {"ecovacs", "ECOVACS"}} {
		if err := db.Exec(`INSERT OR IGNORE INTO manufacturers(slug,name) VALUES(?,?)`, brand[0], brand[1]); err != nil {
			return err
		}
	}
	for _, r := range rows {
		if err := db.Exec(`INSERT OR IGNORE INTO models(slug,brand,name,device_id) VALUES(?,?,?,?)`, r[0], r[1], r[2], r[3]); err != nil {
			return err
		}
	}
	return nil
}
