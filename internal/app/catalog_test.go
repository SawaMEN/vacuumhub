package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func decodeArray(t *testing.T, rbody []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(rbody, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
func importRequest(t *testing.T, h http.Handler, payload string, auth bool) int {
	t.Helper()
	r := httpReq(t, h, "POST", "/api/v1/admin/catalog/import", bytes.NewBufferString(payload), auth, "application/json")
	if r.Code != 200 && auth {
		t.Logf("import failed: %d %s", r.Code, r.Body.String())
	}
	return r.Code
}
func TestCatalogVariantsAndFilters(t *testing.T) {
	_, h := testApp(t)
	data := `{"variants":[{"id":"roborock-s5-test-rev-a","model_slug":"roborock-s5","sku":"S5TEST","region":"global","hardware_revision":"rev-a","notes":"test-only","identifiers":[{"kind":"miot_model","value":"roborock.vacuum.s5","verification":"verified","source_url":"https://example.org/miot"}]}]}`
	if code := importRequest(t, h, data, false); code != 401 {
		t.Fatalf("unauthorized import: %d", code)
	}
	if code := importRequest(t, h, data, true); code != 200 {
		t.Fatalf("import: %d", code)
	}
	if code := importRequest(t, h, data, true); code != 200 {
		t.Fatalf("idempotent import: %d", code)
	}
	r := httpReq(t, h, "GET", "/api/v1/models/roborock-s5/variants", nil, false, "")
	if r.Code != 200 {
		t.Fatalf("variants %d %s", r.Code, r.Body.String())
	}
	rows := decodeArray(t, r.Body.Bytes())
	if len(rows) != 1 || rows[0]["verified"].(float64) != 0 || len(rows[0]["identifiers"].([]any)) != 1 {
		t.Fatalf("unverified variant expected: %v", rows)
	}
	for _, u := range []string{"/api/v1/models?region=global&revision=rev-a", "/api/v1/models?identifier=roborock.vacuum.s5", "/api/v1/models?q=S5TEST", "/api/v1/models?brand=roborock"} {
		r = httpReq(t, h, "GET", u, nil, false, "")
		if r.Code != 200 || len(decodeArray(t, r.Body.Bytes())) != 1 {
			t.Fatalf("filtered catalog %s: %d %s", u, r.Code, r.Body.String())
		}
	}
	r = httpReq(t, h, "GET", "/api/v1/models?verified=1", nil, false, "")
	if r.Code != 200 || len(decodeArray(t, r.Body.Bytes())) != 0 {
		t.Fatal("miot_model alone must not verify hardware")
	}
	verified := `{"variants":[{"id":"roborock-s5-test-rev-a","model_slug":"roborock-s5","sku":"S5TEST","region":"global","hardware_revision":"rev-a","identifiers":[{"kind":"hardware_id","value":"TEST-HARDWARE-A","verification":"verified","source_url":"https://example.org/hardware"}]}]}`
	if code := importRequest(t, h, verified, true); code != 200 {
		t.Fatalf("verified import: %d", code)
	}
	r = httpReq(t, h, "GET", "/api/v1/models?verified=1", nil, false, "")
	rows = decodeArray(t, r.Body.Bytes())
	if r.Code != 200 || len(rows) != 1 || rows[0]["verified_variant_count"].(float64) != 1 {
		t.Fatalf("expected one verified variant: %s", r.Body.String())
	}
	r = httpReq(t, h, "GET", "/api/v1/models?region=cn&verified=1", nil, false, "")
	if len(decodeArray(t, r.Body.Bytes())) != 0 {
		t.Fatal("region mismatch must not match")
	}
	r = httpReq(t, h, "GET", "/api/v1/models?identifier=%27%20OR%201%3D1--", nil, false, "")
	if len(decodeArray(t, r.Body.Bytes())) != 0 {
		t.Fatal("SQL injection changed search result")
	}
	r = httpReq(t, h, "GET", "/api/v1/models?verified=unexpected", nil, false, "")
	if r.Code != 400 {
		t.Fatal("invalid verification filter accepted")
	}
}

func TestCatalogImportAtomicityAndOwnership(t *testing.T) {
	_, h := testApp(t)
	batch := `{"variants":[{"id":"valid-first","model_slug":"roborock-s5","region":"global"},{"id":"bad-second","model_slug":"missing-model"}]}`
	if code := importRequest(t, h, batch, true); code != 409 {
		t.Fatalf("bad foreign key should reject transaction: %d", code)
	}
	r := httpReq(t, h, "GET", "/api/v1/models/roborock-s5/variants", nil, false, "")
	if len(decodeArray(t, r.Body.Bytes())) != 0 {
		t.Fatal("transaction partially committed")
	}
	good := `{"variants":[{"id":"owned-variant","model_slug":"roborock-s5","hardware_revision":"r1","region":"global","identifiers":[{"kind":"hardware_id","value":"HW1","verification":"verified","source_url":"https://example.org/hw"}]}]}`
	if code := importRequest(t, h, good, true); code != 200 {
		t.Fatalf("create: %d", code)
	}
	wrong := `{"variants":[{"id":"owned-variant","model_slug":"xiaomi-mi-robot","hardware_revision":"r2","region":"cn","identifiers":[{"kind":"hardware_id","value":"WRONG","source_url":"https://example.org/hw"}]}]}`
	if code := importRequest(t, h, wrong, true); code != 409 {
		t.Fatalf("different model should not claim variant: %d", code)
	}
	r = httpReq(t, h, "GET", "/api/v1/models/roborock-s5/variants", nil, false, "")
	rows := decodeArray(t, r.Body.Bytes())
	if len(rows) != 1 || rows[0]["hardware_revision"] != "r1" || len(rows[0]["identifiers"].([]any)) != 1 {
		t.Fatalf("failed import modified data: %v", rows)
	}
}

func TestCatalogRejectsUnfoundedVerification(t *testing.T) {
	_, h := testApp(t)
	cases := []string{
		`{"variants":[{"id":"unfounded","model_slug":"roborock-s5","identifiers":[{"kind":"hardware_id","value":"HW","verification":"verified","source_url":"https://example.org/id"}]}]}`,
		`{"variants":[{"id":"unfounded","model_slug":"roborock-s5","region":"eu","hardware_revision":"r1","identifiers":[{"kind":"hardware_id","value":"HW","verification":"verified"}]}]}`,
		`{"variants":[{"id":"unfounded","model_slug":"roborock-s5","region":"eu","hardware_revision":"r1","identifiers":[{"kind":"hardware_id","value":"HW","verification":"verified","source_url":"javascript:alert(1)"}]}]}`,
	}
	for _, c := range cases {
		if code := importRequest(t, h, c, true); code != 400 {
			t.Fatalf("unsafe verification accepted: %d", code)
		}
	}
	r := httpReq(t, h, "GET", "/api/v1/manufacturers", nil, false, "")
	if r.Code != 200 || len(decodeArray(t, r.Body.Bytes())) < 5 {
		t.Fatalf("manufacturers failed: %d %s", r.Code, r.Body.String())
	}
	r = httpReq(t, h, "POST", "/api/v1/admin/manufacturers", bytes.NewBufferString(`{"slug":"test-brand","name":"Test Brand","website":"https://example.org"}`), true, "application/json")
	if r.Code != 201 {
		t.Fatalf("add manufacturer: %d %s", r.Code, r.Body.String())
	}
}

func TestChangingRevisionInvalidatesVerification(t *testing.T) {
	a, h := testApp(t)
	payload := `{"variants":[{"id":"test-revision-invalidation","model_slug":"roborock-s5","region":"global","hardware_revision":"rev1","identifiers":[{"kind":"hardware_id","value":"verified-hw","verification":"verified","source_url":"https://example.org/verified"}]}]}`
	if code := importRequest(t, h, payload, true); code != 200 {
		t.Fatalf("first import: %d", code)
	}
	if err := a.DB.Exec(`UPDATE model_variants SET hardware_revision=? WHERE id=?`, "rev2", "test-revision-invalidation"); err != nil {
		t.Fatal(err)
	}
	r := httpReq(t, h, "GET", "/api/v1/models?verified=1", nil, false, "")
	if r.Code != 200 || len(decodeArray(t, r.Body.Bytes())) != 0 {
		t.Fatalf("stale revision remained verified: %d %s", r.Code, r.Body.String())
	}
	r = httpReq(t, h, "GET", "/api/v1/models/roborock-s5/variants", nil, false, "")
	variants := decodeArray(t, r.Body.Bytes())
	if variants[0]["identifiers"].([]any)[0].(map[string]any)["verification"] != "unverified" {
		t.Fatal("identifier verification not invalidated")
	}
}
func TestVerifiedHardwareIDCannotBelongToTwoVariants(t *testing.T) {
	_, h := testApp(t)
	first := `{"variants":[{"id":"first-verified-hw","model_slug":"roborock-s5","region":"global","hardware_revision":"r1","identifiers":[{"kind":"hardware_id","value":"SAME-DEVICE-ID","verification":"verified","source_url":"https://example.org/s1"}]}]}`
	if code := importRequest(t, h, first, true); code != 200 {
		t.Fatalf("first %d", code)
	}
	second := `{"variants":[{"id":"other-verified-hw","model_slug":"xiaomi-mi-robot","region":"global","hardware_revision":"r9","identifiers":[{"kind":"hardware_id","value":"same-device-id","verification":"verified","source_url":"https://example.org/s2"}]}]}`
	if code := importRequest(t, h, second, true); code != 409 {
		t.Fatalf("duplicate verified hardware ID: %d", code)
	}
	r := httpReq(t, h, "GET", "/api/v1/models/xiaomi-mi-robot/variants", nil, false, "")
	if len(decodeArray(t, r.Body.Bytes())) != 0 {
		t.Fatal("partial import after duplicate hardware ID")
	}
}
