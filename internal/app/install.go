package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/SawaMEN/vacuumhub/internal/install"
)

type dryRunRequest struct {
	FirmwareID string              `json:"firmware_id"`
	VariantID  string              `json:"variant_id"`
	Device     install.Observation `json:"device"`
}

// dryRun uses manually reported details only. There is intentionally no
// flash/execute handler or device connection in this release.
func (a *App) createInstallDryRun(w http.ResponseWriter, r *http.Request) {
	var req dryRunRequest
	if err := strictJSON(w, r, 8192, &req); err != nil {
		problem(w, 400, "invalid JSON")
		return
	}
	d := req.Device
	if !idRE.MatchString(req.FirmwareID) || !slugRE.MatchString(req.VariantID) ||
		len(d.ModelSlug) > 100 || len(d.VariantID) > 100 ||
		len(d.HardwareID) > 200 || len(d.Region) > 40 ||
		len(d.HardwareRevision) > 120 || len(d.InstalledVersion) > 80 ||
		len(d.Bootloader) > 80 || d.BatteryPercent < 0 || d.BatteryPercent > 100 {
		problem(w, 400, "invalid dry-run input")
		return
	}
	firmware, err := a.DB.Query(`SELECT model_slug,version,region,status,firmware_variant_id,
		firmware_source_url,firmware_evidence_url,firmware_rights,firmware_compatibility,
		firmware_scan_status,firmware_authenticity,firmware_downgrade_policy,
		storage_filename,sha256 FROM assets WHERE id=? AND kind='firmware'`, req.FirmwareID)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(firmware) == 0 {
		problem(w, 404, "firmware not found")
		return
	}
	variants, err := a.DB.Query(`SELECT v.model_slug,v.region,v.hardware_revision,
		CASE WHEN `+variantVerified+` THEN 1 ELSE 0 END AS verified
		FROM model_variants v WHERE v.id=?`, req.VariantID)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(variants) == 0 {
		problem(w, 404, "variant not found")
		return
	}
	fw, variant := firmware[0], variants[0]
	found, err := a.DB.Query(`SELECT 1 AS found FROM variant_identifiers
		WHERE variant_id=? AND kind='hardware_id' AND verification='verified'
		AND source_url<>'' AND value=? LIMIT 1`, req.VariantID, d.HardwareID)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	matched := len(found) == 1
	validMapping := variant["verified"] == int64(1) &&
		fw["model_slug"] == variant["model_slug"] &&
		fw["region"] == variant["region"] &&
		fw["firmware_variant_id"] == req.VariantID &&
		fw["firmware_compatibility"] == "documented"
	sourcePresent := fw["firmware_source_url"] != "" &&
		fw["firmware_evidence_url"] != "" && fw["firmware_rights"] != ""

	hashOK := false
	stored := fw["storage_filename"].(string)
	if stored != "" && filepath.Base(stored) == stored {
		f, e := os.Open(filepath.Join(a.StorageDir, stored))
		if e == nil {
			h := sha256.New()
			n, readErr := io.Copy(h, io.LimitReader(f, maxUpload+1))
			_ = f.Close()
			hashOK = readErr == nil && n > 0 && n <= maxUpload &&
				hex.EncodeToString(h.Sum(nil)) == fw["sha256"]
		}
	}
	report := install.Evaluate(install.Input{
		FirmwareApproved:   fw["status"] == "approved",
		ScannedClean:       fw["firmware_scan_status"] == "clean",
		RevisionDocumented: validMapping,
		SourcePresent:      sourcePresent,
		FirmwareHashOK:     hashOK,
		VariantModel:       variant["model_slug"].(string),
		VariantID:          req.VariantID,
		VariantRegion:      variant["region"].(string),
		VariantRevision:    variant["hardware_revision"].(string),
		VerifiedHardwareID: matched,
		TargetVersion:      fw["version"].(string),
		Authenticity:       fw["firmware_authenticity"].(string),
		DowngradePolicy:    fw["firmware_downgrade_policy"].(string),
		Device:             d,
	})
	encoded, err := json.Marshal(report)
	if err != nil {
		problem(w, 500, "cannot encode report")
		return
	}
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		problem(w, 500, "random generator unavailable")
		return
	}
	id := hex.EncodeToString(idBytes[:])
	if err := a.DB.Exec(`INSERT INTO install_dry_runs
		(id,firmware_id,variant_id,mode,decision,report_json)
		VALUES(?, ?, ?, 'dry_run', 'blocked', ?)`, id, req.FirmwareID, req.VariantID, string(encoded)); err != nil {
		problem(w, 500, "audit record unavailable")
		return
	}
	reply(w, 201, map[string]any{"id": id, "firmware_id": req.FirmwareID,
		"variant_id": req.VariantID, "report": report})
}

func (a *App) listInstallDryRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT id,firmware_id,variant_id,mode,decision,created_at
		FROM install_dry_runs ORDER BY created_at DESC,id DESC LIMIT 50`)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, rows)
}

func (a *App) installDryRunDetails(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid ID")
		return
	}
	rows, err := a.DB.Query(`SELECT id,firmware_id,variant_id,mode,decision,report_json,created_at
		FROM install_dry_runs WHERE id=?`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		problem(w, 404, "dry-run not found")
		return
	}
	result := rows[0]
	var report install.Report
	if err := json.Unmarshal([]byte(result["report_json"].(string)), &report); err != nil {
		problem(w, 500, "audit report corrupted")
		return
	}
	delete(result, "report_json")
	result["report"] = report
	reply(w, 200, result)
}
