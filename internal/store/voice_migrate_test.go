package store

import (
	"path/filepath"
	"testing"
)

func TestVoiceColumnsMigrationIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Representative pre-stage3 database: migration must retain existing data.
	if err := db.Exec(`CREATE TABLE assets (id TEXT PRIMARY KEY, kind TEXT, model_slug TEXT, title TEXT, version TEXT, language TEXT, region TEXT, channel TEXT, notes TEXT, original_filename TEXT, storage_filename TEXT, size_bytes INTEGER, sha256 TEXT, status TEXT, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO assets(id,kind,model_slug,title,version,language,region,channel,notes,original_filename,storage_filename,size_bytes,sha256,status) VALUES('old','voicepack','roborock-s5','Legacy','1','ru','','','','legacy.zip','old.blob',16,'abc','approved')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Migrate(); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	rows, err := db.Query(`SELECT title,author,license,series_slug,package_format,preview_storage_filename FROM assets WHERE id='old'`)
	if err != nil || len(rows) != 1 || rows[0]["title"] != "Legacy" || rows[0]["series_slug"] != "" {
		t.Fatalf("migrated row %+v %v", rows, err)
	}
}
