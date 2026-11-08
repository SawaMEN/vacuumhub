package app

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uploadTestFirmware(t *testing.T, h http.Handler, version, filename string, contents []byte) string {
	t.Helper()
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	for k, v := range map[string]string{
		"kind": "firmware", "model_slug": "roborock-s5",
		"title": "Test release", "version": version, "region": "global", "channel": "stable",
	} {
		if err := w.WriteField(k, v); err != nil { t.Fatal(err) }
	}
	f, err := w.CreateFormFile("file", filename)
	if err != nil { t.Fatal(err) }
	if _, err := f.Write(contents); err != nil { t.Fatal(err) }
	if err := w.Close(); err != nil { t.Fatal(err) }
	r := httpReq(t, h, "POST", "/api/v1/admin/assets", &data, true, w.FormDataContentType())
	if r.Code != 201 { t.Fatalf("upload %d: %s", r.Code, r.Body.String()) }
	var result struct { ID string `json:"id"` }
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil { t.Fatal(err) }
	return result.ID
}

func prepareTestVariant(t *testing.T, a *App) {
	t.Helper()
	if err := a.DB.Exec(`INSERT INTO model_variants(id,model_slug,region,hardware_revision)
		VALUES('roborock-s5-global-r1','roborock-s5','global','rev1')`); err != nil { t.Fatal(err) }
	if err := a.DB.Exec(`INSERT INTO variant_identifiers(variant_id,kind,value,source_url,verification)
		VALUES('roborock-s5-global-r1','hardware_id','test.hardware.r1','https://example.org/id','verified')`); err != nil { t.Fatal(err) }
}

func setTestFirmwareMetadata(t *testing.T, h http.Handler, id, signature, signer string) int {
	t.Helper()
	p := map[string]string{
		"variant_id": "roborock-s5-global-r1", "source_url": "https://example.org/fw",
		"source_name": "Example vendor", "license": "Permission granted",
		"rights_statement": "Licensed to distribute on this site",
		"evidence_url": "https://example.org/hardware", "downgrade_policy": "unknown",
		"signature_hex": signature, "signer": signer,
	}
	b, err := json.Marshal(p)
	if err != nil { t.Fatal(err) }
	return httpReq(t, h, "PATCH", "/api/v1/admin/firmware/"+id+"/metadata",
		bytes.NewReader(b), true, "application/json").Code
}

func fakeClamd(t *testing.T, result string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil { return }
			func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10*time.Second))
				r := bufio.NewReader(c)
				prefix := make([]byte, len("zINSTREAM\x00"))
				if _, err := io.ReadFull(r, prefix); err != nil || string(prefix) != "zINSTREAM\x00" { return }
				for {
					var length uint32
					if err := binary.Read(r, binary.BigEndian, &length); err != nil { return }
					if length == 0 { break }
					if length > 1<<20 { return }
					if _, err := io.CopyN(io.Discard, r, int64(length)); err != nil { return }
				}
				_, _ = io.WriteString(c, "stream: "+result+"\x00")
			}()
		}
	}()
	return ln.Addr().String()
}

func TestFirmwareProvenanceScanningHistory(t *testing.T) {
	a, h := testApp(t)
	prepareTestVariant(t, a)
	data := []byte("firmware test bytes, not an actual appliance firmware")
	id := uploadTestFirmware(t, h, "1.0.0", "robot.bin", data)
	publish := func(assetID string) *bytes.Buffer {
		r := httpReq(t, h, "PATCH", "/api/v1/admin/assets/"+assetID,
			strings.NewReader(`{"status":"approved"}`), true, "application/json")
		b := bytes.NewBufferString(fmt.Sprintf("%d: %s", r.Code, r.Body.String()))
		return b
	}
	r := httpReq(t, h, "GET", "/api/v1/firmware/"+id+"/details", nil, false, "")
	if r.Code != 404 { t.Fatal("pending firmware visible") }
	r = httpReq(t, h, "GET", "/api/v1/assets/"+id+"/download", nil, false, "")
	if r.Code != 404 { t.Fatal("pending firmware downloadable") }
	if status := publish(id).String(); !strings.HasPrefix(status, "409") { t.Fatal("metadata gate:", status) }
	if code := setTestFirmwareMetadata(t, h, id, "", ""); code != 200 { t.Fatal("metadata:", code) }
	t.Setenv("FIRMWARE_CLAMD_ADDR", "")
	if status := publish(id).String(); !strings.HasPrefix(status, "409") { t.Fatal("scanner fail-closed:", status) }
	t.Setenv("FIRMWARE_CLAMD_ADDR", fakeClamd(t, "OK"))
	if status := publish(id).String(); !strings.HasPrefix(status, "200") { t.Fatal("publish:", status) }
	r = httpReq(t, h, "GET", "/api/v1/firmware?region=global&variant=roborock-s5-global-r1", nil, false, "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(id)) ||
		!bytes.Contains(r.Body.Bytes(), []byte("documented")) { t.Fatalf("catalog: %d %s", r.Code, r.Body.String()) }
	r = httpReq(t, h, "GET", "/api/v1/firmware/"+id+"/details", nil, false, "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte("unverified")) ||
		!bytes.Contains(r.Body.Bytes(), []byte("https://example.org/fw")) { t.Fatal("provenance missing:", r.Body.String()) }
	r = httpReq(t, h, "GET", "/api/v1/assets/"+id+"/download", nil, false, "")
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), data) { t.Fatal("download failed") }
	r = httpReq(t, h, "GET", "/api/v1/firmware/"+id+"/versions", nil, false, "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte("1.0.0")) { t.Fatal("history:", r.Body.String()) }
	id2 := uploadTestFirmware(t, h, "1.0.0", "other.bin", []byte("second package"))
	if code := setTestFirmwareMetadata(t, h, id2, "", ""); code != 200 { t.Fatal(code) }
	if status := publish(id2).String(); !strings.HasPrefix(status, "409") { t.Fatal("duplicate version:", status) }
	events, err := a.DB.Query(`SELECT event FROM firmware_events WHERE asset_id=? ORDER BY id`, id)
	if err != nil || len(events) != 2 { t.Fatalf("events: %+v %v", events, err) }
}

func TestFirmwareRejectUnsafePackageAndMalware(t *testing.T) {
	a, h := testApp(t)
	prepareTestVariant(t, a)
	t.Setenv("FIRMWARE_CLAMD_ADDR", fakeClamd(t, "OK"))
	id := uploadTestFirmware(t, h, "bad-zip", "robot.zip", zipTestFile(t, "../bad", []byte("traversal")))
	if code := setTestFirmwareMetadata(t, h, id, "", ""); code != 200 { t.Fatal(code) }
	r := httpReq(t, h, "PATCH", "/api/v1/admin/assets/"+id,
		strings.NewReader(`{"status":"approved"}`), true, "application/json")
	if r.Code != 409 { t.Fatal("traversal ZIP approved:", r.Code) }

	t.Setenv("FIRMWARE_CLAMD_ADDR", fakeClamd(t, "Eicar-Test-Signature FOUND"))
	id = uploadTestFirmware(t, h, "infected", "robot.bin", []byte("benign dummy"))
	if code := setTestFirmwareMetadata(t, h, id, "", ""); code != 200 { t.Fatal(code) }
	r = httpReq(t, h, "PATCH", "/api/v1/admin/assets/"+id,
		strings.NewReader(`{"status":"approved"}`), true, "application/json")
	if r.Code != 409 { t.Fatal("ClamAV-found package approved:", r.Code) }
}

func TestFirmwarePinnedSignature(t *testing.T) {
	a, h := testApp(t)
	prepareTestVariant(t, a)
	t.Setenv("FIRMWARE_CLAMD_ADDR", fakeClamd(t, "OK"))
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil { t.Fatal(err) }
	t.Setenv("FIRMWARE_TRUSTED_KEYS", "test-signer:"+hex.EncodeToString(pub))
	data := []byte("a signed test firmware")
	id := uploadTestFirmware(t, h, "signed", "signed.bin", data)
	digest := sha256.Sum256(data)
	sig := ed25519.Sign(priv, digest[:])
	if code := setTestFirmwareMetadata(t, h, id, hex.EncodeToString(sig), "test-signer"); code != 200 { t.Fatal("metadata:", code) }
	r := httpReq(t, h, "PATCH", "/api/v1/admin/assets/"+id, strings.NewReader(`{"status":"approved"}`), true, "application/json")
	if r.Code != 200 { t.Fatalf("signed approval %d: %s", r.Code, r.Body.String()) }
	info := httpReq(t, h, "GET", "/api/v1/firmware/"+id+"/details", nil, false, "")
	if !strings.Contains(info.Body.String(), "trusted_key_signature") { t.Fatal(info.Body.String()) }
	id2 := uploadTestFirmware(t, h, "tampered", "signed.bin", []byte("different file"))
	if code := setTestFirmwareMetadata(t, h, id2, hex.EncodeToString(sig), "test-signer"); code != 200 { t.Fatal(code) }
	r = httpReq(t, h, "PATCH", "/api/v1/admin/assets/"+id2, strings.NewReader(`{"status":"approved"}`), true, "application/json")
	if r.Code != 409 { t.Fatal("bad signature approved:", r.Code) }
}

func TestFirmwareLegacyApprovedHidden(t *testing.T) {
	a, h := testApp(t)
	if err := a.DB.Exec(`INSERT INTO assets(id,kind,model_slug,title,version,region,original_filename,storage_filename,size_bytes,sha256,status)
		VALUES('old-firmware','firmware','roborock-s5','Old','1.0','global','old.bin','old.blob',10,'00','approved')`); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(a.StorageDir, "old.blob"), []byte("old fw"), 0600); err != nil { t.Fatal(err) }
	r := httpReq(t, h, "GET", "/api/v1/firmware", nil, false, "")
	if bytes.Contains(r.Body.Bytes(), []byte("Old")) { t.Fatal("legacy unscanned firmware shown") }
	r = httpReq(t, h, "GET", "/api/v1/assets/old-firmware/download", nil, false, "")
	if r.Code != 400 && r.Code != 404 { t.Fatal("legacy unscanned firmware downloadable", r.Code) }
}

func TestFirmwareZipInspection(t *testing.T) {
	f := filepath.Join(t.TempDir(), "archive.zip")
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.Create("images/firmware.img")
	if err != nil { t.Fatal(err) }
	if _, err := w.Write([]byte("test")); err != nil { t.Fatal(err) }
	if err := z.Close(); err != nil { t.Fatal(err) }
	if err := os.WriteFile(f, b.Bytes(), 0600); err != nil { t.Fatal(err) }
	kind, err := inspectFirmware(f, "archive.zip")
	if err != nil || kind != "zip_checked" { t.Fatalf("%s %v", kind, err) }
}
