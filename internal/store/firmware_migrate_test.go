package store

import (
	"path/filepath"
	"testing"
)

func TestFirmwareMigrationPreservesLegacyAssets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "old.sqlite"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	legacy := `CREATE TABLE assets (id TEXT PRIMARY KEY, kind TEXT NOT NULL, model_slug TEXT NOT NULL,
		title TEXT NOT NULL, version TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '',
		region TEXT NOT NULL DEFAULT '', channel TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '',
		original_filename TEXT NOT NULL, storage_filename TEXT NOT NULL, size_bytes INTEGER NOT NULL,
		sha256 TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`
	if err := db.Exec(legacy); err != nil { t.Fatal(err) }
	if err := db.Exec(`INSERT INTO assets(id,kind,model_slug,title,version,region,original_filename,
		storage_filename,size_bytes,sha256,status) VALUES('legacy','firmware','roborock-s5',
		'Legacy', 'v1','global','old.bin','old.blob',32,'abc','approved')`); err != nil { t.Fatal(err) }
	for i := 0; i < 2; i++ {
		if err := db.Migrate(); err != nil { t.Fatalf("migrate %d: %v", i, err) }
	}
	rows, err := db.Query(`SELECT title,firmware_variant_id,firmware_scan_status,
		firmware_compatibility,firmware_authenticity,firmware_inspection FROM assets WHERE id='legacy'`)
	if err != nil || len(rows)!=1 { t.Fatalf("migrated row: %+v %v", rows, err) }
	if rows[0]["title"]!="Legacy" || rows[0]["firmware_scan_status"]!="not_scanned" ||
		rows[0]["firmware_authenticity"]!="unverified" ||
		rows[0]["firmware_compatibility"]!="unverified" {
		t.Fatalf("legacy firmware must start untrusted: %+v", rows[0])
	}
	if err := db.Exec(`INSERT INTO firmware_events(asset_id,event) VALUES('legacy','migration-test')`); err != nil {
		t.Fatalf("audit table: %v", err)
	}
}
