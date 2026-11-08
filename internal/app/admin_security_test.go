package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminAuthFailureLimiter(t *testing.T) {
	_, h := testApp(t)
	for i := 0; i < maxAuthFailures; i++ {
		r := httpReq(t, h, "GET", "/api/v1/admin/assets", nil, false, "")
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status %d", i, r.Code)
		}
	}
	r := httpReq(t, h, "GET", "/api/v1/admin/assets", nil, true, "")
	if r.Code != http.StatusTooManyRequests || r.Header().Get("Retry-After") == "" {
		t.Fatalf("lockout: %d, headers: %+v", r.Code, r.Header())
	}
	// A different source remains able to authenticate successfully.
	req := httptest.NewRequest("GET", "/api/v1/admin/assets", nil)
	req.RemoteAddr = "192.0.2.200:3456"
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("independent client locked out: %d", rec.Code) }
}

func TestProxyHeaderOverrideAndAppToken(t *testing.T) {
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	_, h := testApp(t)
	for i:=0; i<maxAuthFailures; i++ {
		req := httptest.NewRequest("GET", "/api/v1/admin/assets", nil)
		req.Header.Set("X-Real-IP", "198.51.100.7")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 401 { t.Fatalf("attempt %d: %d", i, rec.Code) }
	}
	req := httptest.NewRequest("GET", "/api/v1/admin/assets", nil)
	req.Header.Set("X-Real-IP", "198.51.100.8")
	req.Header.Set("Authorization", "Basic local-proxy-credentials")
	req.Header.Set("X-Vacuumhub-Token", testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 { t.Fatalf("Caddy Basic + independent app token: %d %s", rec.Code, rec.Body.String()) }
}

func TestAdminAuditAndReadiness(t *testing.T) {
	a,h := testApp(t)
	r := httpReq(t,h,"GET","/api/v1/ready",nil,false,"")
	if r.Code != 200 {t.Fatalf("ready: %d",r.Code)}
	r = httpReq(t,h,"POST","/api/v1/admin/models",
		bytes.NewBufferString(`{"slug":"test-audit-model","brand":"Roborock","name":"Audit model"}`),true,"application/json")
	if r.Code != 201 {t.Fatalf("create model: %d %s",r.Code,r.Body.String())}
	rows,err:=a.DB.Query("SELECT method,path,status_code FROM admin_audit")
	if err!=nil || len(rows)!=1 {t.Fatalf("audit rows: %+v %v",rows,err)}
	if rows[0]["method"]!="POST"||rows[0]["status_code"]!=int64(201)||
		!strings.Contains(rows[0]["path"].(string),"/api/v1/admin/models") {
		t.Fatalf("bad audit %+v",rows[0])
	}
	if err:=os.WriteFile(filepath.Join(a.WebDir, "admin.html"), []byte("<!doctype html><title>Admin</title>"), 0600); err != nil {t.Fatal(err)}
	r=httpReq(t,h,"GET","/admin.html",nil,false,"")
	if r.Header().Get("Cache-Control")!="no-store" {t.Fatalf("admin page may be cached")}
	if err:=a.DB.Close();err!=nil {t.Fatal(err)}
	r=httpReq(t,h,"GET","/api/v1/ready",nil,false,"")
	if r.Code!=503 {t.Fatalf("unavailable sqlite appears ready: %d",r.Code)}
	r=httpReq(t,h,"GET","/api/v1/health",nil,false,"")
	if r.Code!=200 {t.Fatalf("liveness should distinguish readiness: %d",r.Code)}
}
