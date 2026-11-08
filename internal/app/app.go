package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SawaMEN/vacuumhub/internal/store"
	webassets "github.com/SawaMEN/vacuumhub/web"
)

const maxUpload = 100 << 20 // 100 MiB

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,99}$`)
var idRE = regexp.MustCompile(`^[a-f0-9]{32}$`)

type App struct {
	DB                             *store.DB
	TrustProxyHeaders              bool
	failures                       authAttempts
	firmwareMu                     sync.Mutex
	StorageDir, AdminToken, WebDir string
	AdminUsername, AdminPassword string
}

func New(db *store.DB, storageDir, adminToken, webDir string) (*App, error) {
	if len(adminToken) < 24 {
		return nil, errors.New("ADMIN_API_TOKEN must be at least 24 characters")
	}
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, err
	}
	user, pass, err := loadBasicCredentials()
	if err != nil { return nil, err }
	return &App{DB: db, StorageDir: storageDir, AdminToken: adminToken, WebDir: webDir, AdminUsername: user, AdminPassword: pass, TrustProxyHeaders: readProxyConfig()}, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/auth-mode", func(w http.ResponseWriter, r *http.Request) { mode := "token"; if a.AdminUsername != "" {mode = "basic"}; reply(w, 200, map[string]string{"mode": mode}) })
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, map[string]any{"status": "ok"}) })
	mux.HandleFunc("GET /api/v1/ready", a.readiness)
	mux.HandleFunc("GET /api/v1/manufacturers", a.manufacturers)
	mux.HandleFunc("GET /api/v1/models", a.catalogModels)
	mux.HandleFunc("GET /api/v1/models/{slug}/variants", a.variants)
	mux.HandleFunc("POST /api/v1/admin/manufacturers", a.admin(a.createManufacturer))
	mux.HandleFunc("POST /api/v1/admin/catalog/import", a.admin(a.importCatalog))
	mux.HandleFunc("GET /api/v1/models/{slug}", a.model)
	mux.HandleFunc("GET /api/v1/voicepacks", a.publicAssets("voicepack"))
	mux.HandleFunc("GET /api/v1/voicepacks/{id}/versions", a.voiceHistory)
	mux.HandleFunc("GET /api/v1/assets/{id}/preview", a.preview)
	mux.HandleFunc("GET /api/v1/firmware", a.firmwareCatalog)
	mux.HandleFunc("GET /api/v1/firmware/{id}/details", a.firmwareDetails)
	mux.HandleFunc("GET /api/v1/firmware/{id}/versions", a.firmwareVersions)
	mux.HandleFunc("PATCH /api/v1/admin/firmware/{id}/metadata", a.admin(a.firmwareMetadata))
	mux.HandleFunc("POST /api/v1/admin/install/dry-runs", a.admin(a.createInstallDryRun))
	mux.HandleFunc("GET /api/v1/admin/install/dry-runs", a.admin(a.listInstallDryRuns))
	mux.HandleFunc("GET /api/v1/admin/install/dry-runs/{id}", a.admin(a.installDryRunDetails))
	mux.HandleFunc("GET /api/v1/assets/{id}/download", a.download)
	mux.HandleFunc("POST /api/v1/admin/models", a.admin(a.createModel))
	mux.HandleFunc("POST /api/v1/admin/assets", a.admin(a.upload))
	mux.HandleFunc("GET /api/v1/admin/assets", a.admin(a.adminAssets))
	mux.HandleFunc("GET /api/v1/admin/audit", a.admin(a.adminAudit))
	mux.HandleFunc("PATCH /api/v1/admin/assets/{id}", a.admin(a.review))
	if a.WebDir == "" {
		mux.Handle("GET /", http.FileServer(http.FS(webassets.Files)))
	} else {
		mux.Handle("GET /", http.FileServer(http.Dir(a.WebDir)))
	}
	return a.headers(mux)
}

func (a *App) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.URL.Path == "/admin.html" || strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if r.URL.Path == "/admin.html" && a.AdminUsername != "" && !a.basicAuthenticated(r) {
			a.basicChallenge(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func problem(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func clean(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return ""
	}
	return s
}
func (a *App) models(w http.ResponseWriter, r *http.Request) {
	q := clean(r.URL.Query().Get("q"), 100)
	brand := clean(r.URL.Query().Get("brand"), 80)
	rows, err := a.DB.Query(`SELECT slug,brand,name,device_id,description FROM models WHERE (?='' OR brand=?) AND (?='' OR name LIKE ? OR device_id LIKE ?) ORDER BY brand,name LIMIT 200`, brand, brand, q, "%"+q+"%", "%"+q+"%")
	if err != nil {
		slog.Error("models", "error", err)
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, rows)
}
func (a *App) model(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRE.MatchString(slug) {
		problem(w, 400, "invalid model slug")
		return
	}
	rows, err := a.DB.Query(`SELECT slug,brand,name,device_id,description FROM models WHERE slug=?`, slug)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		problem(w, 404, "model not found")
		return
	}
	reply(w, 200, rows[0])
}

const assetColumns = `id,kind,model_slug,title,version,language,region,channel,notes,original_filename,size_bytes,sha256,status,created_at,author,license,series_slug,package_format,preview_size_bytes,preview_mime,CASE WHEN preview_storage_filename<>'' THEN 1 ELSE 0 END AS has_preview`

func (a *App) publicAssets(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		model := clean(r.URL.Query().Get("model"), 100)
		q := clean(r.URL.Query().Get("q"), 100)
		rows, err := a.DB.Query(`SELECT `+assetColumns+` FROM assets WHERE kind=? AND status='approved' AND (?='' OR model_slug=?) AND (?='' OR title LIKE ?) ORDER BY created_at DESC LIMIT 100`, kind, model, model, q, "%"+q+"%")
		if err != nil {
			problem(w, 500, "database error")
			return
		}
		reply(w, 200, rows)
	}
}
func (a *App) createModel(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Slug        string `json:"slug"`
		Brand       string `json:"brand"`
		Name        string `json:"name"`
		DeviceID    string `json:"device_id"`
		Description string `json:"description"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<15)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		problem(w, 400, "invalid JSON")
		return
	}
	v.Brand = clean(v.Brand, 80)
	v.Name = clean(v.Name, 120)
	v.DeviceID = clean(v.DeviceID, 120)
	v.Description = clean(v.Description, 1000)
	if !slugRE.MatchString(v.Slug) || v.Brand == "" || v.Name == "" {
		problem(w, 400, "invalid model fields")
		return
	}
	known, err := a.DB.Query(`SELECT slug FROM manufacturers WHERE name=? COLLATE NOCASE`, v.Brand)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(known) == 0 {
		problem(w, 400, "unknown manufacturer; add it first")
		return
	}
	if err := a.DB.Exec(`INSERT INTO models(slug,brand,name,device_id,description) VALUES(?,?,?,?,?)`, v.Slug, v.Brand, v.Name, v.DeviceID, v.Description); err != nil {
		problem(w, 409, "could not create model (possibly duplicate)")
		return
	}
	reply(w, 201, map[string]string{"slug": v.Slug})
}
func allowedFile(kind, filename string) bool {
	s := strings.ToLower(filename)
	for _, ext := range []string{".zip", ".pkg", ".tar.gz", ".tgz"} {
		if strings.HasSuffix(s, ext) {
			return true
		}
	}
	if kind == "firmware" {
		for _, ext := range []string{".bin", ".img"} {
			if strings.HasSuffix(s, ext) {
				return true
			}
		}
	}
	return false
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+maxPreview+(2<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		problem(w, 413, "multipart form too large or invalid")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	kind := r.FormValue("kind")
	model := r.FormValue("model_slug")
	title := clean(r.FormValue("title"), 120)
	version := clean(r.FormValue("version"), 80)
	language := clean(r.FormValue("language"), 40)
	region := clean(r.FormValue("region"), 40)
	channel := clean(r.FormValue("channel"), 40)
	notes := clean(r.FormValue("notes"), 5000)
	author := clean(r.FormValue("author"), 120)
	license := clean(r.FormValue("license"), 120)
	series := clean(r.FormValue("series_slug"), 100)
	if kind == "voicepack" && (!slugRE.MatchString(series) || author == "" || license == "" || version == "") {
		problem(w, 400, "voicepack requires series_slug, author, license and version")
		return
	}
	if kind != "firmware" && kind != "voicepack" || !slugRE.MatchString(model) || title == "" {
		problem(w, 400, "invalid asset fields")
		return
	}
	if (kind == "firmware" && (version == "" || region == "")) || (kind == "voicepack" && language == "") {
		problem(w, 400, "firmware requires version/region; voicepack requires language")
		return
	}
	m, err := a.DB.Query(`SELECT slug FROM models WHERE slug=?`, model)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(m) == 0 {
		problem(w, 400, "unknown model")
		return
	}
	file, fh, err := r.FormFile("file")
	if err != nil {
		problem(w, 400, "file missing")
		return
	}
	defer file.Close()
	filename := filepath.Base(strings.ReplaceAll(fh.Filename, "\\", "/"))
	if len(filename) > 200 || !allowedFile(kind, filename) {
		problem(w, 400, "unsupported file type or filename")
		return
	}
	rnd := make([]byte, 16)
	if _, err := rand.Read(rnd); err != nil {
		problem(w, 500, "random generator unavailable")
		return
	}
	id := hex.EncodeToString(rnd)
	storageName := id + ".blob"
	path := filepath.Join(a.StorageDir, storageName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		problem(w, 500, "storage error")
		return
	}
	saved := false
	defer func() {
		if !saved {
			_ = os.Remove(path)
		}
	}()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(file, maxUpload+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		problem(w, 500, "write error")
		return
	}
	if n == 0 || n > maxUpload {
		problem(w, 413, "file must be 1 to 100 MiB")
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	packageFormat := ""
	previewName, previewSum, previewMime := "", "", ""
	previewSize := 0
	if kind == "voicepack" {
		packageFormat, err = validateVoiceArchive(filename, path, voiceManifest{model, series, version, language})
		if err != nil {
			problem(w, 400, "invalid voicepack archive: "+err.Error())
			return
		}
		// Preview audio is separate from the installable archive and is never executed.
		previewFile, previewHeader, previewErr := r.FormFile("preview")
		if previewErr != nil && !errors.Is(previewErr, http.ErrMissingFile) {
			problem(w, 400, "invalid preview")
			return
		}
		if previewErr == nil {
			previewFilename := filepath.Base(strings.ReplaceAll(previewHeader.Filename, "\\", "/"))
			data, media, checksum, e := readPreview(previewFile, previewFilename)
			_ = previewFile.Close()
			if e != nil {
				problem(w, 400, e.Error())
				return
			}
			previewName = id + ".preview"
			if e := os.WriteFile(filepath.Join(a.StorageDir, previewName), data, 0600); e != nil {
				_ = os.Remove(filepath.Join(a.StorageDir, previewName))
				problem(w, 500, "preview storage error")
				return
			}
			previewMime, previewSum, previewSize = media, checksum, len(data)
		}
	} else if ff, _, e := r.FormFile("preview"); e == nil {
		_ = ff.Close()
		problem(w, 400, "preview only available for voicepacks")
		return
	}
	previewSaved := false
	defer func() {
		if !previewSaved && previewName != "" {
			_ = os.Remove(filepath.Join(a.StorageDir, previewName))
		}
	}()
	if err := a.DB.Exec(`INSERT INTO assets(id,kind,model_slug,title,version,language,region,channel,notes,original_filename,storage_filename,size_bytes,sha256,status,author,license,series_slug,package_format,preview_storage_filename,preview_sha256,preview_size_bytes,preview_mime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'pending',?,?,?,?,?,?,?,?)`, id, kind, model, title, version, language, region, channel, notes, filename, storageName, n, sum, author, license, series, packageFormat, previewName, previewSum, previewSize, previewMime); err != nil {
		slog.Error("insert asset", "error", err)
		problem(w, 500, "database error")
		return
	}
	saved = true
	previewSaved = true
	reply(w, 201, map[string]any{"id": id, "sha256": sum, "size_bytes": n, "status": "pending"})
}
func (a *App) adminAssets(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" {
		problem(w, 400, "invalid status")
		return
	}
	rows, err := a.DB.Query(`SELECT `+firmwareColumns+` FROM assets WHERE (?='' OR status=?) ORDER BY created_at DESC LIMIT 200`, status, status)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, rows)
}
func (a *App) review(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid id")
		return
	}
	var v struct {
		Status string `json:"status"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil || v.Status != "approved" && v.Status != "rejected" {
		problem(w, 400, "status must be approved or rejected")
		return
	}
	rows, err := a.DB.Query(`SELECT storage_filename,sha256,status,kind,original_filename,preview_storage_filename,preview_sha256,preview_mime,series_slug,model_slug,language,version FROM assets WHERE id=?`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		problem(w, 404, "asset not found")
		return
	}
	if rows[0]["status"] != "pending" {
		problem(w, 409, "asset has already been reviewed")
		return
	}
	if v.Status == "approved" {
		f, e := os.Open(filepath.Join(a.StorageDir, rows[0]["storage_filename"].(string)))
		if e != nil {
			problem(w, 409, "file missing")
			return
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		_ = f.Close()
		if e != nil || hex.EncodeToString(h.Sum(nil)) != rows[0]["sha256"] {
			problem(w, 409, "file checksum mismatch")
			return
		}
		if rows[0]["kind"] == "voicepack" {
			if _, e = validateVoiceArchive(rows[0]["original_filename"].(string), filepath.Join(a.StorageDir, rows[0]["storage_filename"].(string)), voiceManifest{rows[0]["model_slug"].(string), rows[0]["series_slug"].(string), rows[0]["version"].(string), rows[0]["language"].(string)}); e != nil {
				problem(w, 409, "voicepack archive failed validation")
				return
			}
			if previewName := rows[0]["preview_storage_filename"].(string); previewName != "" {
				data, e := os.ReadFile(filepath.Join(a.StorageDir, previewName))
				if e != nil || len(data) > maxPreview || len(data) == 0 {
					problem(w, 409, "preview missing or invalid")
					return
				}
				sum := sha256.Sum256(data)
				if hex.EncodeToString(sum[:]) != rows[0]["preview_sha256"] {
					problem(w, 409, "preview checksum mismatch")
					return
				}
			}
		}
	}
	if rows[0]["kind"] == "firmware" {
		if err := a.reviewFirmware(id, v.Status); err != nil {
			problem(w, 409, err.Error())
			return
		}
		reply(w, 200, map[string]string{"id": id, "status": v.Status})
		return
	}
	if err := a.DB.Exec(`UPDATE assets SET status=? WHERE id=? AND status='pending'`, v.Status, id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			problem(w, 409, "this voicepack version is already published")
			return
		}
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, map[string]string{"id": id, "status": v.Status})
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid id")
		return
	}
	rows, err := a.DB.Query(`SELECT storage_filename,original_filename FROM assets WHERE id=? AND status='approved' AND (kind<>'firmware' OR (firmware_scan_status='clean' AND firmware_compatibility='documented'))`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		problem(w, 404, "asset unavailable")
		return
	}
	v := rows[0]
	f, err := os.Open(filepath.Join(a.StorageDir, v["storage_filename"].(string)))
	if err != nil {
		problem(w, 404, "file unavailable")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		problem(w, 500, "storage error")
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": v["original_filename"].(string)}))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=0")
	http.ServeContent(w, r, v["original_filename"].(string), st.ModTime(), f)
}

// Log request failures without logging Bearer tokens or multipart body contents.
func Run(addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 3 * time.Minute, WriteTimeout: 3 * time.Minute, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	slog.Info("vacuumhub ready", "address", addr)
	err := srv.ListenAndServe()
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}
