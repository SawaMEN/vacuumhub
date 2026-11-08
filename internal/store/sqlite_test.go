package store

import (
	"path/filepath"
	"testing"
)

func TestSQLiteRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Seed(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT slug FROM models WHERE brand=?", "Roborock")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["slug"] != "roborock-s5" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	if err := db.Exec("INSERT INTO models(slug,brand,name) VALUES(?,?,?)", "one", "X", "One"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO models(slug,brand,name) VALUES(?,?,?)", "one", "X", "One"); err == nil {
		t.Fatal("expected duplicate key rejection")
	}
}

func TestBatchRollback(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "atomic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
	err = db.Batch([]Statement{
		{SQL: `INSERT INTO manufacturers(slug,name) VALUES(?,?)`, Args: []any{"first", "First"}},
		{SQL: `INSERT INTO manufacturers(slug,name) VALUES(?,?)`, Args: []any{"first", "Duplicate"}},
	})
	if err == nil {
		t.Fatal("expected batch conflict")
	}
	rows, err := db.Query(`SELECT slug FROM manufacturers WHERE slug='first'`)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rollback broken: %v %v", rows, err)
	}
}
