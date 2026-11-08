// Package store wraps the system SQLite library. It intentionally has no third-party
// Go modules so that single-node deployments are reproducible without Go proxy access.
package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

type DB struct {
	mu     sync.Mutex
	handle *C.sqlite3
}

func Open(path string) (*DB, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var handle *C.sqlite3
	flags := C.int(C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_FULLMUTEX)
	if rc := C.sqlite3_open_v2(cpath, &handle, flags, nil); rc != C.SQLITE_OK {
		var msg string
		if handle != nil {
			msg = C.GoString(C.sqlite3_errmsg(handle))
			C.sqlite3_close(handle)
		}
		return nil, fmt.Errorf("sqlite open: %s", msg)
	}
	db := &DB{handle: handle}
	C.sqlite3_busy_timeout(handle, 5000)
	if err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.handle == nil {
		return nil
	}
	if C.sqlite3_close(db.handle) != C.SQLITE_OK {
		return errors.New("sqlite close failed")
	}
	db.handle = nil
	return nil
}

func (db *DB) prepareLocked(query string, args []any) (*C.sqlite3_stmt, error) {
	if db.handle == nil {
		return nil, errors.New("database closed")
	}
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))
	var stmt *C.sqlite3_stmt
	if C.sqlite3_prepare_v2(db.handle, cquery, -1, &stmt, nil) != C.SQLITE_OK {
		return nil, fmt.Errorf("sqlite prepare: %s", C.GoString(C.sqlite3_errmsg(db.handle)))
	}
	if int(C.sqlite3_bind_parameter_count(stmt)) != len(args) {
		expected := int(C.sqlite3_bind_parameter_count(stmt))
		C.sqlite3_finalize(stmt)
		return nil, fmt.Errorf("sqlite parameters: expected %d, got %d", expected, len(args))
	}
	for i, a := range args {
		n := C.int(i + 1)
		var rc C.int
		switch v := a.(type) {
		case nil:
			rc = C.sqlite3_bind_null(stmt, n)
		case string:
			cs := C.CString(v)
			rc = C.sqlite3_bind_text(stmt, n, cs, C.int(len(v)), C.SQLITE_TRANSIENT)
			C.free(unsafe.Pointer(cs))
		case int:
			rc = C.sqlite3_bind_int64(stmt, n, C.sqlite3_int64(v))
		case int64:
			rc = C.sqlite3_bind_int64(stmt, n, C.sqlite3_int64(v))
		case bool:
			if v {
				rc = C.sqlite3_bind_int(stmt, n, 1)
			} else {
				rc = C.sqlite3_bind_int(stmt, n, 0)
			}
		default:
			C.sqlite3_finalize(stmt)
			return nil, fmt.Errorf("unsupported parameter type %T", a)
		}
		if rc != C.SQLITE_OK {
			C.sqlite3_finalize(stmt)
			return nil, fmt.Errorf("sqlite bind: %s", C.GoString(C.sqlite3_errmsg(db.handle)))
		}
	}
	return stmt, nil
}

func (db *DB) Exec(query string, args ...any) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	stmt, err := db.prepareLocked(query, args)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			return nil
		}
		if rc != C.SQLITE_ROW {
			return fmt.Errorf("sqlite exec: %s", C.GoString(C.sqlite3_errmsg(db.handle)))
		}
	}
}

// Query materializes result rows before releasing the DB lock. Never concatenate
// untrusted input into SQL; bind arguments using the query placeholders.
func (db *DB) Query(query string, args ...any) ([]map[string]any, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	stmt, err := db.prepareLocked(query, args)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	rows := make([]map[string]any, 0)
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			return rows, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, fmt.Errorf("sqlite query: %s", C.GoString(C.sqlite3_errmsg(db.handle)))
		}
		row := make(map[string]any)
		for i := C.int(0); i < C.sqlite3_column_count(stmt); i++ {
			k := C.GoString(C.sqlite3_column_name(stmt, i))
			switch C.sqlite3_column_type(stmt, i) {
			case C.SQLITE_INTEGER:
				row[k] = int64(C.sqlite3_column_int64(stmt, i))
			case C.SQLITE_FLOAT:
				row[k] = float64(C.sqlite3_column_double(stmt, i))
			case C.SQLITE_TEXT:
				row[k] = C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, i))))
			case C.SQLITE_BLOB:
				n := C.sqlite3_column_bytes(stmt, i)
				row[k] = C.GoBytes(unsafe.Pointer(C.sqlite3_column_blob(stmt, i)), n)
			default:
				row[k] = nil
			}
		}
		rows = append(rows, row)
	}
}
