package app

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SawaMEN/vacuumhub/internal/store"
)

// Firmware is deliberately catalogued, not installed. A documented mapping to
// a device revision does not mean the package is safe to flash to that device.
const firmwareColumns = assetColumns + `,firmware_variant_id,firmware_source_url,firmware_source_name,firmware_rights,firmware_evidence_url,firmware_downgrade_policy,firmware_signer,firmware_signature_hex,firmware_authenticity,firmware_compatibility,firmware_scan_status,firmware_inspection`

type firmwareMetadata struct {
	VariantID       string `json:"variant_id"`
	SourceURL       string `json:"source_url"`
	SourceName      string `json:"source_name"`
	License         string `json:"license"`
	Rights          string `json:"rights_statement"`
	EvidenceURL     string `json:"evidence_url"`
	DowngradePolicy string `json:"downgrade_policy"`
	Signer          string `json:"signer"`
	SignatureHex    string `json:"signature_hex"`
}

func (a *App) firmwareMetadata(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid asset ID")
		return
	}
	var v firmwareMetadata
	if err := strictJSON(w, r, 8192, &v); err != nil {
		problem(w, 400, "invalid JSON")
		return
	}
	v.SourceURL = clean(v.SourceURL, 500)
	v.SourceName = clean(v.SourceName, 120)
	v.License = clean(v.License, 120)
	v.Rights = clean(v.Rights, 1000)
	v.EvidenceURL = clean(v.EvidenceURL, 500)
	v.Signer = clean(v.Signer, 100)
	v.SignatureHex = strings.ToLower(clean(v.SignatureHex, 128))
	if !slugRE.MatchString(v.VariantID) || !validSource(v.SourceURL) || v.SourceURL == "" ||
		!validSource(v.EvidenceURL) || v.EvidenceURL == "" ||
		v.SourceName == "" || v.License == "" || v.Rights == "" {
		problem(w, 400, "source, verified variant, evidence, rights and license required")
		return
	}
	if v.DowngradePolicy == "" {
		v.DowngradePolicy = "unknown"
	}
	switch v.DowngradePolicy {
	case "unknown", "forbidden", "documented":
	default:
		problem(w, 400, "invalid downgrade policy")
		return
	}
	if (v.Signer == "") != (v.SignatureHex == "") {
		problem(w, 400, "signer and signature must be supplied together")
		return
	}
	if v.Signer != "" {
		sig, err := hex.DecodeString(v.SignatureHex)
		if !slugRE.MatchString(v.Signer) || err != nil || len(sig) != ed25519.SignatureSize {
			problem(w, 400, "invalid Ed25519 signer or signature")
			return
		}
	}
	rows, err := a.DB.Query(`SELECT model_slug,region,status FROM assets WHERE id=? AND kind='firmware'`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		problem(w, 404, "firmware not found")
		return
	}
	if rows[0]["status"] != "pending" {
		problem(w, 409, "published or rejected firmware is immutable")
		return
	}
	variants, err := a.DB.Query(`SELECT v.model_slug,v.region,v.hardware_revision,
		CASE WHEN `+variantVerified+` THEN 1 ELSE 0 END AS documented
		FROM model_variants v WHERE v.id=?`, v.VariantID)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(variants) != 1 || variants[0]["model_slug"] != rows[0]["model_slug"] ||
		!strings.EqualFold(variants[0]["region"].(string), rows[0]["region"].(string)) ||
		variants[0]["documented"] != int64(1) {
		problem(w, 409, "variant must match the exact model/region and have a sourced hardware ID and revision")
		return
	}
	if err := a.DB.Batch([]store.Statement{
		{SQL: `UPDATE assets SET firmware_variant_id=?,firmware_source_url=?,firmware_source_name=?,
			license=?,firmware_rights=?,firmware_evidence_url=?,firmware_downgrade_policy=?,
			firmware_signer=?,firmware_signature_hex=?,firmware_authenticity='unverified',
			firmware_compatibility='unverified',firmware_scan_status='not_scanned'
			WHERE id=? AND status='pending' AND kind='firmware'`,
			Args: []any{v.VariantID, v.SourceURL, v.SourceName, v.License, v.Rights,
				v.EvidenceURL, v.DowngradePolicy, v.Signer, v.SignatureHex, id}},
		{SQL: `INSERT INTO firmware_events(asset_id,event,detail) VALUES(?,?,?)`,
			Args: []any{id, "metadata", "source, revision and rights recorded"}},
	}); err != nil {
		problem(w, 409, "could not save metadata")
		return
	}
	reply(w, 200, map[string]string{"id": id, "status": "pending", "metadata": "saved"})
}

func (a *App) firmwareCatalog(w http.ResponseWriter, r *http.Request) {
	model := clean(r.URL.Query().Get("model"), 100)
	q := clean(r.URL.Query().Get("q"), 100)
	region := clean(r.URL.Query().Get("region"), 40)
	variant := clean(r.URL.Query().Get("variant"), 100)
	rows, err := a.DB.Query(`SELECT `+firmwareColumns+` FROM assets
		WHERE kind='firmware' AND status='approved' AND firmware_scan_status='clean'
		AND firmware_compatibility='documented'
		AND (?='' OR model_slug=?) AND (?='' OR title LIKE ?)
		AND (?='' OR region=?) AND (?='' OR firmware_variant_id=?)
		ORDER BY created_at DESC,id DESC LIMIT 100`,
		model, model, q, "%"+q+"%", region, region, variant, variant)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, rows)
}

func (a *App) firmwareDetails(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid ID")
		return
	}
	rows, err := a.DB.Query(`SELECT `+firmwareColumns+` FROM assets WHERE
		id=? AND kind='firmware' AND status='approved'
		AND firmware_scan_status='clean' AND firmware_compatibility='documented'`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) != 1 {
		problem(w, 404, "firmware unavailable")
		return
	}
	reply(w, 200, rows[0])
}

func (a *App) firmwareVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		problem(w, 400, "invalid ID")
		return
	}
	rows, err := a.DB.Query(`SELECT firmware_variant_id,channel FROM assets
		WHERE id=? AND kind='firmware' AND status='approved'
		AND firmware_scan_status='clean' AND firmware_compatibility='documented'`, id)
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		problem(w, 404, "firmware unavailable")
		return
	}
	v := rows[0]
	list, err := a.DB.Query(`SELECT `+firmwareColumns+` FROM assets
		WHERE kind='firmware' AND status='approved' AND firmware_scan_status='clean'
		AND firmware_compatibility='documented' AND firmware_variant_id=? AND channel=?
		ORDER BY created_at DESC,id DESC LIMIT 100`, v["firmware_variant_id"], v["channel"])
	if err != nil {
		problem(w, 500, "database error")
		return
	}
	reply(w, 200, list)
}

func (a *App) reviewFirmware(id, status string) error {
	rows, err := a.DB.Query(`SELECT model_slug,version,region,channel,storage_filename,
		original_filename,sha256,license,firmware_variant_id,firmware_source_url,
		firmware_source_name,firmware_rights,firmware_evidence_url,
		firmware_signer,firmware_signature_hex,status
		FROM assets WHERE id=? AND kind='firmware'`, id)
	if err != nil || len(rows) != 1 || rows[0]["status"] != "pending" {
		return errors.New("firmware unavailable or already moderated")
	}
	if status == "rejected" {
		return a.DB.Batch([]store.Statement{
			{SQL: `UPDATE assets SET status='rejected' WHERE id=? AND status='pending'`, Args: []any{id}},
			{SQL: `INSERT INTO firmware_events(asset_id,event,detail) VALUES(?,?,?)`, Args: []any{id, "rejected", "moderator decision"}},
		})
	}
	v := rows[0]
	if v["license"] == "" || v["firmware_source_url"] == "" ||
		v["firmware_source_name"] == "" || v["firmware_rights"] == "" ||
		v["firmware_evidence_url"] == "" || v["firmware_variant_id"] == "" {
		return errors.New("firmware provenance, rights and variant metadata required")
	}
	variants, err := a.DB.Query(`SELECT v.model_slug,v.region,
		CASE WHEN `+variantVerified+` THEN 1 ELSE 0 END AS documented
		FROM model_variants v WHERE v.id=?`, v["firmware_variant_id"])
	if err != nil || len(variants) != 1 || variants[0]["documented"] != int64(1) ||
		variants[0]["model_slug"] != v["model_slug"] ||
		!strings.EqualFold(variants[0]["region"].(string), v["region"].(string)) {
		return errors.New("documented variant/revision mapping is missing or has changed")
	}
	p := filepath.Join(a.StorageDir, v["storage_filename"].(string))
	inspection, err := inspectFirmware(p, v["original_filename"].(string))
	if err != nil {
		return fmt.Errorf("firmware package inspection: %w", err)
	}
	authenticity, err := verifyPinnedSignature(v["sha256"].(string), v["firmware_signer"].(string),
		v["firmware_signature_hex"].(string))
	if err != nil {
		return fmt.Errorf("firmware signature: %w", err)
	}
	if err := scanFirmwareClamd(p); err != nil {
		return fmt.Errorf("malware scan: %w", err)
	}
	if err := a.DB.Batch([]store.Statement{
		{SQL: `UPDATE assets SET status='approved', firmware_authenticity=?,
			firmware_compatibility='documented',firmware_scan_status='clean',
			firmware_inspection=? WHERE id=? AND kind='firmware' AND status='pending'`,
			Args: []any{authenticity, inspection, id}},
		{SQL: `INSERT INTO firmware_events(asset_id,event,detail) VALUES(?,?,?)`,
			Args: []any{id, "approved", "archive and ClamAV checked; compatibility is documented only"}},
	}); err != nil {
		return fmt.Errorf("publication refused (possibly duplicate version): %w", err)
	}
	return nil
}

func verifyPinnedSignature(sum, signer, signatureHex string) (string, error) {
	if signer == "" && signatureHex == "" {
		return "unverified", nil
	}
	if signer == "" || signatureHex == "" {
		return "", errors.New("missing signature or signer")
	}
	var key []byte
	for _, pair := range strings.Split(os.Getenv("FIRMWARE_TRUSTED_KEYS"), ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(parts) == 2 && parts[0] == signer {
			k, err := hex.DecodeString(parts[1])
			if err == nil && len(k) == ed25519.PublicKeySize {
				key = k
			}
			break
		}
	}
	if len(key) != ed25519.PublicKeySize {
		return "", errors.New("signer has no pinned Ed25519 public key")
	}
	signature, err := hex.DecodeString(signatureHex)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return "", errors.New("invalid detached signature")
	}
	digest, err := hex.DecodeString(sum)
	if err != nil || len(digest) != sha256.Size ||
		!ed25519.Verify(ed25519.PublicKey(key), digest, signature) {
		return "", errors.New("signature verification failed")
	}
	// Pinned-key verification does not prove the key belongs to the manufacturer.
	return "trusted_key_signature", nil
}

// ClamAV INSTREAM protocol; fail closed. The service operator must configure
// FIRMWARE_CLAMD_ADDR and provision a scanner separately from the 512MiB app.
func scanFirmwareClamd(path string) error {
	addr := strings.TrimSpace(os.Getenv("FIRMWARE_CLAMD_ADDR"))
	if addr == "" {
		return errors.New("FIRMWARE_CLAMD_ADDR not configured; publication blocked")
	}
	network := "tcp"
	if strings.HasPrefix(addr, "unix:") {
		network, addr = "unix", strings.TrimPrefix(addr, "unix:")
	}
	conn, err := net.DialTimeout(network, addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Minute)); err != nil {
		return err
	}
	if _, err := io.WriteString(conn, "zINSTREAM\x00"); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	chunk := make([]byte, 32<<10)
	for {
		n, readErr := f.Read(chunk)
		if n > 0 {
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(n))
			if _, err := conn.Write(size[:]); err != nil {
				return err
			}
			if _, err := conn.Write(chunk[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return err
	}
	answer, err := bufio.NewReader(io.LimitReader(conn, 512)).ReadString(0)
	if err != nil && err != io.EOF {
		return err
	}
	answer = strings.TrimSpace(strings.TrimSuffix(answer, "\x00"))
	if !strings.HasSuffix(answer, ": OK") {
		return fmt.Errorf("ClamAV did not return OK: %q", answer)
	}
	return nil
}

func inspectFirmware(storagePath, filename string) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip_checked", inspectFirmwareZip(storagePath)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar_checked", inspectFirmwareTar(storagePath)
	case strings.HasSuffix(lower, ".bin"), strings.HasSuffix(lower, ".img"), strings.HasSuffix(lower, ".pkg"):
		// Proprietary binary formats require vendor-specific parsers. No claims
		// about their internal structure are made at this stage.
		f, err := os.Open(storagePath)
		if err != nil { return "", err }
		defer f.Close()
		sample := make([]byte, 512)
		n, err := f.Read(sample)
		if err != nil && err != io.EOF { return "", err }
		trimmed := strings.ToLower(strings.TrimSpace(string(sample[:n])))
		if n == 0 || strings.HasPrefix(trimmed, "<html") || strings.HasPrefix(trimmed, "<!doctype html") {
			return "", errors.New("empty or HTML content masquerading as firmware")
		}
		return "opaque_unparsed", nil
	default:
		return "", errors.New("unsupported firmware extension")
	}
}

func inspectFirmwareZip(filename string) error {
	f, err := os.Open(filename)
	if err != nil { return err }
	defer f.Close()
	stat, err := f.Stat()
	if err != nil { return err }
	z, err := zip.NewReader(f, stat.Size())
	if err != nil { return err }
	if len(z.File) == 0 || len(z.File) > maxArchiveEntries {
		return errors.New("ZIP entry count invalid")
	}
	var total uint64
	seen := make(map[string]bool, len(z.File))
	regular := 0
	for _, item := range z.File {
		if !safeEntryName(item.Name) || seen[item.Name] {
			return errors.New("unsafe or duplicate ZIP entry")
		}
		seen[item.Name] = true
		if !item.Mode().IsRegular() && !item.Mode().IsDir() {
			return errors.New("ZIP symlink or special file")
		}
		if item.Flags&1 != 0 || (item.Method != zip.Store && item.Method != zip.Deflate) {
			return errors.New("encrypted or unsupported ZIP entry")
		}
		if item.Mode().IsDir() { continue }
		regular++
		if item.UncompressedSize64 > maxExtracted-total ||
			item.UncompressedSize64 > maxUpload ||
			item.UncompressedSize64 > (item.CompressedSize64+1)*200 {
			return errors.New("excessive extracted size or compression ratio")
		}
		total += item.UncompressedSize64
		reader, err := item.Open()
		if err != nil { return err }
		n, readErr := io.Copy(io.Discard, io.LimitReader(reader, int64(item.UncompressedSize64)+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || uint64(n) != item.UncompressedSize64 {
			return errors.New("corrupted ZIP entry or checksum")
		}
	}
	if regular == 0 { return errors.New("empty ZIP") }
	return nil
}

func inspectFirmwareTar(filename string) error {
	f, err := os.Open(filename)
	if err != nil { return err }
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil { return err }
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	var total int64
	entries, regular := 0, 0
	seen := make(map[string]bool)
	for {
		h, err := tarReader.Next()
		if err == io.EOF { break }
		if err != nil { return err }
		entries++
		if entries > maxArchiveEntries || !safeEntryName(h.Name) || seen[h.Name] {
			return errors.New("too many, unsafe or duplicate TAR entries")
		}
		seen[h.Name] = true
		switch h.Typeflag {
		case tar.TypeDir: continue
		case tar.TypeReg, tar.TypeRegA:
		default: return errors.New("TAR links or special files forbidden")
		}
		regular++
		if h.Size < 0 || h.Size > maxUpload || h.Size > maxExtracted-total {
			return errors.New("excessive TAR extracted size")
		}
		n, err := io.Copy(io.Discard, io.LimitReader(tarReader, h.Size+1))
		if err != nil || n != h.Size { return errors.New("corrupted TAR entry") }
		total += n
	}
	if regular == 0 { return errors.New("empty TAR") }
	return nil
}
