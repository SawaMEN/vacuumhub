package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SawaMEN/vacuumhub/internal/store"
)

const testToken = "test-secret-key-length-more-than-24"

func testApp(t *testing.T) (*App, http.Handler) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Seed(); err != nil {
		t.Fatal(err)
	}
	a, err := New(db, filepath.Join(t.TempDir(), "uploads"), testToken, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return a, a.Handler()
}
func httpReq(t *testing.T, h http.Handler, method, url string, body io.Reader, auth bool, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, body)
	if auth {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
func TestCatalogAndAuth(t *testing.T) {
	_, h := testApp(t)
	r := httpReq(t, h, "GET", "/api/v1/models", nil, false, "")
	if r.Code != 200 {
		t.Fatalf("catalog: %d %s", r.Code, r.Body.String())
	}
	var data []map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil || len(data) != 3 {
		t.Fatalf("models: %#v; %v", data, err)
	}
	r = httpReq(t, h, "GET", "/api/v1/admin/assets", nil, false, "")
	if r.Code != 401 {
		t.Fatalf("unauthenticated admin request: %d", r.Code)
	}
	r = httpReq(t, h, "GET", "/api/v1/firmware", nil, false, "")
	if r.Code != 200 || r.Body.String() != "[]\n" {
		t.Fatalf("unapproved firmware visible: %d %q", r.Code, r.Body.String())
	}
}
func TestUploadModerateAndDownload(t *testing.T) {
	a, h := testApp(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fields := map[string]string{"kind": "voicepack", "model_slug": "roborock-s5", "title": "Тестовая озвучка", "language": "ru", "series_slug": "roborock-s5-ru", "version": "1.0", "author": "Tester", "license": "CC-BY-4.0"}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	f, err := mw.CreateFormFile("file", "demo.zip")
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	z := zip.NewWriter(&zipped)
	entry, e := z.Create("sample.txt")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = entry.Write([]byte("demo audio package")); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	fileBytes := zipped.Bytes()
	if _, err := f.Write(fileBytes); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httpReq(t, h, "POST", "/api/v1/admin/assets", &buf, true, mw.FormDataContentType())
	if r.Code != 201 {
		t.Fatalf("upload: %d %s", r.Code, r.Body.String())
	}
	var upload map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	id := upload["id"].(string)
	r = httpReq(t, h, "GET", "/api/v1/voicepacks", nil, false, "")
	if r.Code != 200 || r.Body.String() != "[]\n" {
		t.Fatal("pending asset publicly visible")
	}
	r = httpReq(t, h, "GET", "/api/v1/assets/"+id+"/download", nil, false, "")
	if r.Code != 404 {
		t.Fatal("pending asset downloadable")
	}
	r = httpReq(t, h, "PATCH", "/api/v1/admin/assets/"+id, bytes.NewBufferString(`{"status":"approved"}`), true, "application/json")
	if r.Code != 200 {
		t.Fatalf("approve: %d %s", r.Code, r.Body.String())
	}
	r = httpReq(t, h, "GET", "/api/v1/voicepacks?model=roborock-s5", nil, false, "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte("Тестовая озвучка")) {
		t.Fatal("approved pack missing from public catalog")
	}
	r = httpReq(t, h, "GET", "/api/v1/assets/"+id+"/download", nil, false, "")
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), fileBytes) {
		t.Fatalf("download code=%d data=%q", r.Code, r.Body.Bytes())
	}
	if r.Header().Get("Content-Disposition") == "" {
		t.Fatal("download should be attachment")
	}
	files, err := os.ReadDir(a.StorageDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("stored files: %v, %v", files, err)
	}
}
func TestRejectInvalidTokenAndFields(t *testing.T) {
	_, h := testApp(t)
	r := httpReq(t, h, "POST", "/api/v1/admin/models", bytes.NewBufferString(`{"slug":"unsafe/../","brand":"B","name":"N"}`), true, "application/json")
	if r.Code != 400 {
		t.Fatalf("path unsafe model accepted: %d", r.Code)
	}
	req := httptest.NewRequest("GET", "/api/v1/admin/assets", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal("wrong admin key accepted")
	}
}
