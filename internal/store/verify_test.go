package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyRecoveredDataAndTampering(t *testing.T) {
	root := t.TempDir()
	db, err := Open(filepath.Join(root, "vacuumhub.sqlite"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	if err := db.Migrate(); err != nil { t.Fatal(err) }
	if err := db.Seed(); err != nil { t.Fatal(err) }
	assets := filepath.Join(root, "assets")
	if err := os.Mkdir(assets,0700); err != nil { t.Fatal(err) }
	data := []byte("known test file")
	sum := sha256.Sum256(data)
	name := "testasset.blob"
	if err := os.WriteFile(filepath.Join(assets,name),data,0600); err != nil {t.Fatal(err)}
	if err := db.Exec(`INSERT INTO assets(id,kind,model_slug,title,version,language,
		original_filename,storage_filename,size_bytes,sha256,status) VALUES(
		'asset1','voicepack','roborock-s5','test','1','ru','voice.zip',?,?,?,'pending')`,
		name,int64(len(data)),hex.EncodeToString(sum[:])); err != nil {t.Fatal(err)}
	if err := VerifyData(db,assets); err != nil {t.Fatalf("unmodified backup: %v",err)}
	if err := os.WriteFile(filepath.Join(assets,name),[]byte("tampered file!!"),0600);err!=nil{t.Fatal(err)}
	if err := VerifyData(db,assets);err==nil{t.Fatal("tampered backup accepted")}
	if err := os.Remove(filepath.Join(assets,name));err!=nil{t.Fatal(err)}
	if err := VerifyData(db,assets);err==nil{t.Fatal("missing backup asset accepted")}
}
