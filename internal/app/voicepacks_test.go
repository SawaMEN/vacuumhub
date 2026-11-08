package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipTestFile(t *testing.T, filename string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func tarTestFile(t *testing.T, filename string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: filename, Size: int64(len(data)), Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func uploadVoice(t *testing.T, h http.Handler, version, packName string, data, preview []byte, previewName string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := map[string]string{"kind": "voicepack", "model_slug": "roborock-s5", "series_slug": "s5-narrator", "title": "S5 Russian", "language": "ru", "version": version, "author": "Tester", "license": "CC-BY-4.0"}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	f, err := mw.CreateFormFile("file", packName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(data); err != nil {
		t.Fatal(err)
	}
	if preview != nil {
		f, err = mw.CreateFormFile("preview", previewName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(preview); err != nil {
			t.Fatal(err)
		}
	}
	if err = mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httpReq(t, h, "POST", "/api/v1/admin/assets", &body, true, mw.FormDataContentType())
	if r.Code != 201 {
		return r.Code, r.Body.String()
	}
	var v struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return r.Code, v.ID
}
func approveVoice(t *testing.T, h http.Handler, id string) int {
	t.Helper()
	return httpReq(t, h, "PATCH", "/api/v1/admin/assets/"+id, strings.NewReader(`{"status":"approved"}`), true, "application/json").Code
}
func TestVoicePreviewHistoryAndModeration(t *testing.T) {
	a, h := testApp(t)
	zipData := zipTestFile(t, "sounds/hello.wav", []byte("demo"))
	preview := append([]byte("RIFF\x00\x00\x00\x00WAVE"), bytes.Repeat([]byte{0}, 128)...)
	code, id := uploadVoice(t, h, "1.0", "voice.zip", zipData, preview, "demo.wav")
	if code != 201 {
		t.Fatalf("upload %d: %s", code, id)
	}
	r := httpReq(t, h, "GET", "/api/v1/assets/"+id+"/preview", nil, false, "")
	if r.Code != 404 {
		t.Fatalf("pending preview visible: %d", r.Code)
	}
	if r = httpReq(t, h, "GET", "/api/v1/voicepacks/"+id+"/versions", nil, false, ""); r.Code != 404 {
		t.Fatalf("pending history visible: %d", r.Code)
	}
	if code = approveVoice(t, h, id); code != 200 {
		t.Fatalf("approve %d", code)
	}
	r = httpReq(t, h, "GET", "/api/v1/assets/"+id+"/preview", nil, false, "")
	if r.Code != 200 || r.Header().Get("Content-Type") != "audio/wav" || !bytes.Equal(r.Body.Bytes(), preview) {
		t.Fatalf("preview %d %s", r.Code, r.Header().Get("Content-Type"))
	}
	r = httpReq(t, h, "GET", "/api/v1/voicepacks", nil, false, "")
	rows := decodeArray(t, r.Body.Bytes())
	if len(rows) != 1 || rows[0]["author"] != "Tester" || rows[0]["license"] != "CC-BY-4.0" || rows[0]["series_slug"] != "s5-narrator" || rows[0]["has_preview"] != float64(1) {
		t.Fatalf("metadata: %+v", rows)
	}
	code, id2 := uploadVoice(t, h, "2.0", "voice.zip", zipData, nil, "")
	if code != 201 || approveVoice(t, h, id2) != 200 {
		t.Fatalf("second version %d %s", code, id2)
	}
	r = httpReq(t, h, "GET", "/api/v1/voicepacks/"+id+"/versions", nil, false, "")
	rows = decodeArray(t, r.Body.Bytes())
	if len(rows) != 2 || rows[0]["id"] == nil {
		t.Fatalf("history: %s", r.Body.String())
	}
	code, id3 := uploadVoice(t, h, "2.0", "voice.zip", zipData, nil, "")
	if code != 201 || approveVoice(t, h, id3) != 409 {
		t.Fatalf("duplicate published version not blocked %d %s", code, id3)
	}
	// Changes to a pending preview must prevent publishing the entire package.
	code, id4 := uploadVoice(t, h, "3.0", "voice.zip", zipData, preview, "demo.wav")
	if code != 201 {
		t.Fatal(code, id4)
	}
	if err := os.WriteFile(filepath.Join(a.StorageDir, id4+".preview"), []byte("not the same audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := approveVoice(t, h, id4); code != 409 {
		t.Fatalf("tampered preview approved: %d", code)
	}
}
func TestVoiceArchiveAndPreviewValidation(t *testing.T) {
	_, h := testApp(t)
	cases := []struct {
		name        string
		filename    string
		payload     []byte
		preview     []byte
		previewName string
	}{
		{"fake zip", "voice.zip", []byte("PKnotazip"), nil, ""},
		{"zip traversal", "voice.zip", zipTestFile(t, "../escape", []byte("malicious")), nil, ""},
		{"tar traversal", "voice.tgz", tarTestFile(t, "../escape"), nil, ""},
		{"unsupported proprietary", "voice.pkg", []byte("raw opaque proprietary format"), nil, ""},
		{"fake audio", "voice.zip", zipTestFile(t, "a.txt", []byte("x")), []byte("<script>alert(1)</script>"), "preview.mp3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, message := uploadVoice(t, h, "1.0", tc.filename, tc.payload, tc.preview, tc.previewName)
			if code != 400 {
				t.Fatalf("should reject %d: %s", code, message)
			}
		})
	}
	// A simple valid gzip tar is accepted, without executing its contents.
	code, id := uploadVoice(t, h, "1.0", "voice.tar.gz", tarTestFile(t, "sounds/1.wav"), nil, "")
	if code != 201 {
		t.Fatalf("tar expected 201, got %d: %s", code, id)
	}
}

func TestVoiceManifestVerification(t *testing.T) {
	_, h := testApp(t)
	var good bytes.Buffer
	zw := zip.NewWriter(&good)
	mf, err := zw.Create("vacuumhub.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mf.Write([]byte(`{"model_slug":"roborock-s5","series_slug":"s5-narrator","version":"1.0","language":"ru"}`)); err != nil {
		t.Fatal(err)
	}
	audio, err := zw.Create("audio/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = audio.Write([]byte("demo")); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	code, id := uploadVoice(t, h, "1.0", "voice.zip", good.Bytes(), nil, "")
	if code != 201 {
		t.Fatalf("matching manifest rejected: %d %s", code, id)
	}
	code, id = uploadVoice(t, h, "2.0", "voice.zip", good.Bytes(), nil, "")
	if code != 400 {
		t.Fatalf("mismatched manifest accepted: %d %s", code, id)
	}
}
