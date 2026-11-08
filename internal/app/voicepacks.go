package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxPreview        = 8 << 20   // 8 MiB
	maxExtracted      = 200 << 20 // maximum total unpacked payload
	maxArchiveEntries = 2000
)

// Structural archive verification prevents traversal, links, unsupported files,
// and common zip bombs. It is not antivirus scanning or protocol compatibility.
type voiceManifest struct {
	ModelSlug  string `json:"model_slug"`
	SeriesSlug string `json:"series_slug"`
	Version    string `json:"version"`
	Language   string `json:"language"`
}

func validateManifest(data []byte, expected voiceManifest) error {
	if len(data) == 0 || len(data) > 16<<10 {
		return errors.New("manifest exceeds 16 KiB or empty")
	}
	var m voiceManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return errors.New("invalid voicepack manifest")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing manifest data")
	}
	if m != expected {
		return errors.New("manifest metadata does not match the submitted voicepack")
	}
	return nil
}

func validateVoiceArchive(filename, storagePath string, expected voiceManifest) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip", validateZip(storagePath, expected)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz", validateTarGzip(storagePath, expected)
	default:
		return "", errors.New("voicepack must be ZIP or tar.gz; proprietary packages need a dedicated parser")
	}
}

func safeEntryName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return false
	}
	p := strings.TrimSuffix(name, "/")
	if p == "" || path.Clean(p) != p {
		return false
	}
	for _, component := range strings.Split(p, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func validateZip(filename string, expected voiceManifest) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return fmt.Errorf("invalid ZIP: %w", err)
	}
	if len(zr.File) == 0 || len(zr.File) > maxArchiveEntries {
		return errors.New("ZIP file count out of bounds")
	}
	var total uint64
	regular := 0
	seen := make(map[string]bool, len(zr.File))
	for _, z := range zr.File {
		if !safeEntryName(z.Name) || seen[z.Name] {
			return errors.New("unsafe or duplicate ZIP entry")
		}
		seen[z.Name] = true
		mode := z.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return errors.New("ZIP links and special files forbidden")
		}
		if z.Flags&1 != 0 {
			return errors.New("encrypted ZIP unsupported")
		}
		if z.Method != zip.Store && z.Method != zip.Deflate {
			return errors.New("unsupported ZIP compression")
		}
		if mode.IsDir() {
			continue
		}
		regular++
		if z.UncompressedSize64 > maxUpload || z.UncompressedSize64 > maxExtracted-total {
			return errors.New("ZIP extracted size exceeds limit")
		}
		if z.UncompressedSize64 > (z.CompressedSize64+1)*200 {
			return errors.New("ZIP decompression ratio too high")
		}
		total += z.UncompressedSize64
		reader, e := z.Open()
		if e != nil {
			return e
		}
		var target io.Writer = io.Discard
		var manifest bytes.Buffer
		if z.Name == "vacuumhub.json" {
			if z.UncompressedSize64 > 16<<10 {
				_ = reader.Close()
				return errors.New("manifest too large")
			}
			target = &manifest
		}
		copied, e := io.Copy(target, io.LimitReader(reader, int64(z.UncompressedSize64)+1))
		closeErr := reader.Close()
		if e == nil && z.Name == "vacuumhub.json" {
			e = validateManifest(manifest.Bytes(), expected)
		}
		if e != nil || closeErr != nil || uint64(copied) != z.UncompressedSize64 {
			return errors.New("invalid ZIP entry or checksum")
		}
	}
	if regular == 0 {
		return errors.New("empty voicepack")
	}
	return nil
}

func validateTarGzip(filename string, expected voiceManifest) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("invalid gzip: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	regular := 0
	count := 0
	seen := make(map[string]bool)
	for {
		header, e := reader.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return fmt.Errorf("invalid tar: %w", e)
		}
		count++
		if count > maxArchiveEntries {
			return errors.New("archive has too many files")
		}
		if !safeEntryName(header.Name) || seen[header.Name] {
			return errors.New("unsafe or duplicate tar entry")
		}
		seen[header.Name] = true
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return errors.New("tar links and special files forbidden")
		}
		regular++
		if header.Size < 0 || header.Size > maxUpload || header.Size > maxExtracted-total {
			return errors.New("tar extracted size exceeds limit")
		}
		var target io.Writer = io.Discard
		var manifest bytes.Buffer
		if header.Name == "vacuumhub.json" {
			if header.Size > 16<<10 {
				return errors.New("manifest too large")
			}
			target = &manifest
		}
		n, e := io.Copy(target, io.LimitReader(reader, header.Size+1))
		if e == nil && header.Name == "vacuumhub.json" {
			e = validateManifest(manifest.Bytes(), expected)
		}
		if e != nil || n != header.Size {
			return errors.New("truncated tar entry")
		}
		total += n
	}
	if regular == 0 {
		return errors.New("empty voicepack")
	}
	return nil
}

// Verify both extension and file signature; do not allow HTML masquerading as audio.
func previewMediaType(filename string, header []byte) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".wav") && len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE":
		return "audio/wav"
	case strings.HasSuffix(lower, ".ogg") && len(header) >= 4 && string(header[:4]) == "OggS":
		return "audio/ogg"
	case strings.HasSuffix(lower, ".mp3") && ((len(header) >= 3 && string(header[:3]) == "ID3") || (len(header) >= 2 && header[0] == 0xff && header[1]&0xe0 == 0xe0)):
		return "audio/mpeg"
	}
	return ""
}

func (a *App) preview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid id")
		return
	}
	rows, err := a.DB.Query(`SELECT preview_storage_filename,preview_mime FROM assets WHERE id=? AND kind='voicepack' AND status='approved' AND preview_storage_filename<>''`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		problem(w, 404, "preview unavailable")
		return
	}
	item := rows[0]
	f, err := os.Open(filepath.Join(a.StorageDir, item["preview_storage_filename"].(string)))
	if err != nil {
		problem(w, 404, "preview unavailable")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		problem(w, 500, "storage error")
		return
	}
	w.Header().Set("Content-Type", item["preview_mime"].(string))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "preview"}))
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, "preview", stat.ModTime(), f)
}

func (a *App) voiceHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid id")
		return
	}
	found, err := a.DB.Query(`SELECT model_slug,series_slug,language FROM assets WHERE kind='voicepack' AND status='approved' AND id=? AND series_slug<>''`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(found) == 0 {
		problem(w, 404, "voicepack unavailable")
		return
	}
	v := found[0]
	rows, err := a.DB.Query(`SELECT `+assetColumns+` FROM assets WHERE kind='voicepack' AND status='approved' AND model_slug=? AND series_slug=? AND language=? ORDER BY created_at DESC,id DESC LIMIT 100`, v["model_slug"], v["series_slug"], v["language"])
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, rows)
}

func readPreview(file io.Reader, filename string) ([]byte, string, string, error) {
	data, err := io.ReadAll(io.LimitReader(file, maxPreview+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) == 0 || len(data) > maxPreview {
		return nil, "", "", errors.New("audio preview exceeds 8 MiB")
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	mediaType := previewMediaType(filename, sample)
	if mediaType == "" {
		return nil, "", "", errors.New("preview must be WAV, OGG or MP3 with matching signature")
	}
	sum := sha256.Sum256(data)
	return data, mediaType, hex.EncodeToString(sum[:]), nil
}
