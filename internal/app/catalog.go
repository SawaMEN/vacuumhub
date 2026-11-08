package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/SawaMEN/vacuumhub/internal/store"
)

// Verification means a moderator recorded a source for an exact hardware ID;
// it is NOT cryptographic authenticity or permission to flash firmware.
const variantVerified = `v.hardware_revision<>'' AND v.region<>'' AND EXISTS (
 SELECT 1 FROM variant_identifiers vi WHERE vi.variant_id=v.id AND vi.kind='hardware_id'
 AND vi.verification='verified' AND vi.source_url<>'')`

const variantColumns = `v.id,v.model_slug,v.sku,v.region,v.hardware_revision,v.notes,
 CASE WHEN ` + variantVerified + ` THEN 1 ELSE 0 END AS verified`

func (a *App) manufacturers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT f.slug,f.name,f.website,COUNT(m.slug) AS model_count
 FROM manufacturers f LEFT JOIN models m ON m.brand=f.name COLLATE NOCASE
 GROUP BY f.slug,f.name,f.website ORDER BY f.name`)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, rows)
}

func (a *App) createManufacturer(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Slug    string `json:"slug"`
		Name    string `json:"name"`
		Website string `json:"website"`
	}
	if err := strictJSON(w, r, 8192, &v); err != nil {
		problem(w, 400, "invalid JSON")
		return
	}
	v.Name = clean(v.Name, 80)
	if !slugRE.MatchString(v.Slug) || v.Name == "" || !validSource(v.Website) {
		problem(w, 400, "invalid manufacturer")
		return
	}
	if err := a.DB.Exec(`INSERT INTO manufacturers(slug,name,website) VALUES(?,?,?)`, v.Slug, v.Name, v.Website); err != nil {
		problem(w, 409, "manufacturer exists or invalid")
		return
	}
	reply(w, 201, map[string]string{"slug": v.Slug})
}

func validSource(raw string) bool {
	if raw == "" {
		return true
	}
	if len(raw) > 500 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func strictJSON(w http.ResponseWriter, r *http.Request, max int64, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data")
	}
	return nil
}

type importedIdentifier struct {
	Kind         string `json:"kind"`
	Value        string `json:"value"`
	SourceURL    string `json:"source_url"`
	Verification string `json:"verification"`
}
type importedVariant struct {
	ID               string               `json:"id"`
	ModelSlug        string               `json:"model_slug"`
	SKU              string               `json:"sku"`
	Region           string               `json:"region"`
	HardwareRevision string               `json:"hardware_revision"`
	Notes            string               `json:"notes"`
	Identifiers      []importedIdentifier `json:"identifiers"`
}

func normalizeVariant(v *importedVariant) error {
	if !slugRE.MatchString(v.ID) || !slugRE.MatchString(v.ModelSlug) {
		return errors.New("invalid variant or model slug")
	}
	for _, p := range []struct {
		field *string
		max   int
	}{{&v.SKU, 120}, {&v.Region, 40}, {&v.HardwareRevision, 120}, {&v.Notes, 1000}} {
		if strings.TrimSpace(*p.field) != "" && clean(*p.field, p.max) == "" {
			return errors.New("field too long")
		}
		*p.field = clean(*p.field, p.max)
	}
	if len(v.Identifiers) > 20 {
		return errors.New("too many identifiers")
	}
	for i := range v.Identifiers {
		id := &v.Identifiers[i]
		switch id.Kind {
		case "hardware_id", "miot_model", "sku", "model_number", "vendor":
		default:
			return errors.New("unsupported identifier kind")
		}
		if len(id.Value) > 200 || len(id.Value) == 0 || clean(id.Value, 200) == "" {
			return errors.New("invalid identifier value")
		}
		id.Value = clean(id.Value, 200)
		if !validSource(id.SourceURL) {
			return errors.New("invalid source URL")
		}
		if id.Verification == "" {
			id.Verification = "unverified"
		}
		if id.Verification != "verified" && id.Verification != "unverified" {
			return errors.New("invalid verification")
		}
		if id.Verification == "verified" && (id.SourceURL == "" || id.Kind == "hardware_id" && (v.Region == "" || v.HardwareRevision == "")) {
			return errors.New("verified identifier requires source and exact region/revision for hardware IDs")
		}
	}
	return nil
}

// importCatalog accepts at most 100 variants. All inserts/upserts commit together.
// Importing never marks a firmware asset as compatible.
func (a *App) importCatalog(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Variants []importedVariant `json:"variants"`
	}
	if err := strictJSON(w, r, 1<<20, &payload); err != nil {
		problem(w, 400, "invalid JSON")
		return
	}
	if len(payload.Variants) == 0 || len(payload.Variants) > 100 {
		problem(w, 400, "expected 1-100 variants")
		return
	}
	statements := make([]store.Statement, 0, 300)
	for i := range payload.Variants {
		v := &payload.Variants[i]
		if err := normalizeVariant(v); err != nil {
			problem(w, 400, err.Error())
			return
		}
		statements = append(statements, store.Statement{
			SQL: `INSERT INTO model_variants(id,model_slug,sku,region,hardware_revision,notes) VALUES(?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET sku=excluded.sku,region=excluded.region,hardware_revision=excluded.hardware_revision,notes=excluded.notes, model_slug=CASE WHEN model_variants.model_slug=excluded.model_slug THEN model_variants.model_slug ELSE NULL END`,
			Args: []any{v.ID, v.ModelSlug, v.SKU, v.Region, v.HardwareRevision, v.Notes},
		})
		for _, id := range v.Identifiers {
			statements = append(statements, store.Statement{
				SQL: `INSERT INTO variant_identifiers(variant_id,kind,value,source_url,verification) VALUES(?,?,?,?,?)
 ON CONFLICT(variant_id,kind,value) DO UPDATE SET source_url=excluded.source_url,verification=excluded.verification`,
				Args: []any{v.ID, id.Kind, id.Value, id.SourceURL, id.Verification},
			})
		}
	}
	if err := a.DB.Batch(statements); err != nil {
		problem(w, 409, "catalog import rejected: check model references and variant ownership")
		return
	}
	reply(w, 200, map[string]any{"imported_variants": len(payload.Variants), "status": "saved"})
}

func (a *App) variants(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRE.MatchString(slug) {
		problem(w, 400, "invalid model slug")
		return
	}
	rows, err := a.DB.Query(`SELECT `+variantColumns+` FROM model_variants v WHERE v.model_slug=? ORDER BY v.region,v.hardware_revision,v.id LIMIT 200`, slug)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	for _, v := range rows {
		identifiers, err := a.DB.Query(`SELECT kind,value,source_url,verification FROM variant_identifiers WHERE variant_id=? ORDER BY kind,value LIMIT 100`, v["id"])
		if err != nil {
			problem(w, 500, "database error")
			return
		}
		v["identifiers"] = identifiers
	}
	reply(w, 200, rows)
}

func (a *App) catalogModels(w http.ResponseWriter, r *http.Request) {
	q := clean(r.URL.Query().Get("q"), 100)
	brand := clean(r.URL.Query().Get("brand"), 80)
	region := clean(r.URL.Query().Get("region"), 40)
	revision := clean(r.URL.Query().Get("revision"), 120)
	identifier := clean(r.URL.Query().Get("identifier"), 200)
	verified := r.URL.Query().Get("verified")
	if verified != "" && verified != "1" && verified != "0" {
		problem(w, 400, "verified must be 1 or 0")
		return
	}
	query := `SELECT m.slug,m.brand,m.name,m.device_id,m.description,
 (SELECT COUNT(*) FROM model_variants v WHERE v.model_slug=m.slug) AS variant_count,
 (SELECT COUNT(*) FROM model_variants v WHERE v.model_slug=m.slug AND ` + variantVerified + `) AS verified_variant_count
 FROM models m WHERE 1=1`
	args := make([]any, 0, 16)
	if brand != "" {
		query += ` AND m.brand=? COLLATE NOCASE`
		args = append(args, brand)
	}
	if q != "" {
		query += ` AND (m.name LIKE ? OR m.brand LIKE ? OR m.device_id LIKE ? OR EXISTS (
 SELECT 1 FROM model_variants v LEFT JOIN variant_identifiers vi ON vi.variant_id=v.id
 WHERE v.model_slug=m.slug AND (v.sku LIKE ? OR v.id LIKE ? OR vi.value LIKE ?)))`
		like := "%" + q + "%"
		for i := 0; i < 6; i++ {
			args = append(args, like)
		}
	}
	if region != "" || revision != "" || identifier != "" || verified != "" {
		query += ` AND EXISTS(SELECT 1 FROM model_variants v WHERE v.model_slug=m.slug`
		if region != "" {
			query += ` AND v.region=?`
			args = append(args, region)
		}
		if revision != "" {
			query += ` AND v.hardware_revision=?`
			args = append(args, revision)
		}
		if identifier != "" {
			query += ` AND EXISTS(SELECT 1 FROM variant_identifiers vi WHERE vi.variant_id=v.id AND vi.value LIKE ?)`
			args = append(args, "%"+identifier+"%")
		}
		if verified == "1" {
			query += ` AND (` + variantVerified + `)`
		}
		if verified == "0" {
			query += ` AND NOT (` + variantVerified + `)`
		}
		query += `)`
	}
	query += ` ORDER BY m.brand,m.name LIMIT 200`
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, rows)
}
