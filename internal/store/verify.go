package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// VerifyData checks an *existing, offline* backup's SQLite integrity,
// foreign-key references and every referenced file's size and SHA-256.
// It must be run on an isolated restore volume, not the live service.
func VerifyData(db *DB, storageDir string) error {
	rows, err := db.Query("PRAGMA integrity_check")
	if err != nil || len(rows) != 1 || rows[0]["integrity_check"] != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %v / %v", rows, err)
	}
	fk, err := db.Query("PRAGMA foreign_key_check")
	if err != nil || len(fk) != 0 {
		return fmt.Errorf("SQLite foreign key check failed: %v / %v", fk, err)
	}
	assets, err := db.Query(`SELECT storage_filename,sha256,size_bytes,
		preview_storage_filename,preview_sha256,preview_size_bytes FROM assets`)
	if err != nil {
		return fmt.Errorf("asset inventory: %w", err)
	}
	for _, a := range assets {
		if err := verifyStoredFile(storageDir, a["storage_filename"].(string),
			a["sha256"].(string), a["size_bytes"].(int64)); err != nil {
			return fmt.Errorf("asset verification: %w", err)
		}
		if name := a["preview_storage_filename"].(string); name != "" {
			if err := verifyStoredFile(storageDir, name,
				a["preview_sha256"].(string), a["preview_size_bytes"].(int64)); err != nil {
				return fmt.Errorf("preview verification: %w", err)
			}
		}
	}
	return nil
}

func verifyStoredFile(root, name, expectedSum string, expectedSize int64) error {
	if name == "" || name == "." || filepath.Base(name) != name ||
		strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("unsafe storage filename %q", name)
	}
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil { return fmt.Errorf("missing file %q: %w", name, err) }
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return fmt.Errorf("invalid type or size for %q", name)
	}
	f, err := os.Open(path)
	if err != nil { return err }
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil { return err }
	if expectedSum == "" || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expectedSum) {
		return fmt.Errorf("SHA-256 mismatch for %q", name)
	}
	return nil
}
