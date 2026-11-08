package app

import (
 "bytes"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func testInstallRecord(t *testing.T, a *App) string {
 t.Helper()
 if err:=a.DB.Exec(`INSERT INTO model_variants(id,model_slug,region,hardware_revision)
   VALUES('s5-global-r1','roborock-s5','global','revision-one')`);err!=nil {t.Fatal(err)}
 if err:=a.DB.Exec(`INSERT INTO variant_identifiers
  (variant_id,kind,value,source_url,verification) VALUES
  ('s5-global-r1','hardware_id','test-unique-device-secret','https://example.org/device','verified')`);err!=nil{t.Fatal(err)}
 data:=[]byte("not actual firmware bytes")
 id:=strings.Repeat("a",32)
 storageName:=id+".blob"
 if err:=os.WriteFile(filepath.Join(a.StorageDir,storageName),data,0600);err!=nil{t.Fatal(err)}
 sum:=sha256.Sum256(data)
 if err:=a.DB.Exec(`INSERT INTO assets(id,kind,model_slug,title,version,region,channel,
   original_filename,storage_filename,size_bytes,sha256,status,firmware_variant_id,
   firmware_source_url,firmware_evidence_url,firmware_rights,firmware_compatibility,
   firmware_scan_status,firmware_downgrade_policy)
   VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
   id,"firmware","roborock-s5","Test Firmware","2.0","global","stable",
   "test.bin",storageName,len(data),hex.EncodeToString(sum[:]),"approved","s5-global-r1",
   "https://example.org/fw","https://example.org/evidence","permission","documented",
   "clean","documented");err!=nil {t.Fatal(err)}
 return id
}

func TestInstallPreflightAuditedButNeverExecutes(t *testing.T){
 a,h:=testApp(t)
 id:=testInstallRecord(t,a)
 body:=map[string]any{
  "firmware_id":id,"variant_id":"s5-global-r1",
  "device":map[string]any{"model_slug":"roborock-s5","variant_id":"s5-global-r1",
   "hardware_id":"test-unique-device-secret","region":"global",
   "hardware_revision":"revision-one","installed_version":"1.0",
   "battery_percent":100,"charging":true,"bootloader":"verified_supported"},
 }
 raw,e:=json.Marshal(body);if e!=nil{t.Fatal(e)}
 r:=httpReq(t,h,"POST","/api/v1/admin/install/dry-runs",bytes.NewReader(raw),false,"application/json")
 if r.Code!=http.StatusUnauthorized{t.Fatalf("unprotected preflight: %d",r.Code)}
 r=httpReq(t,h,"POST","/api/v1/admin/install/dry-runs",bytes.NewReader(raw),true,"application/json")
 if r.Code!=http.StatusCreated{t.Fatalf("preflight: %d %s",r.Code,r.Body.String())}
 var result struct{ID string `json:"id"`;Report struct{
  Decision string `json:"decision"`
  ExecutionAllowed bool `json:"execution_allowed"`
 } `json:"report"`}
 if err:=json.Unmarshal(r.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if result.Report.Decision!="blocked"||result.Report.ExecutionAllowed||len(result.ID)!=32{
  t.Fatalf("unexpected report: %+v",result)
 }
 if strings.Contains(r.Body.String(),"test-unique-device-secret"){t.Fatal("hardware ID leaked")}
 history:=httpReq(t,h,"GET","/api/v1/admin/install/dry-runs",nil,true,"")
 if history.Code!=200 || !strings.Contains(history.Body.String(),result.ID) {
  t.Fatalf("audit history %d %s",history.Code,history.Body.String())
 }
 detail:=httpReq(t,h,"GET","/api/v1/admin/install/dry-runs/"+result.ID,nil,true,"")
 if detail.Code!=200 || strings.Contains(detail.Body.String(),"test-unique-device-secret") {
  t.Fatalf("audit leak %d %s",detail.Code,detail.Body.String())
 }
 if rows,e:=a.DB.Query(`SELECT mode,decision,report_json FROM install_dry_runs WHERE id=?`,result.ID);e!=nil||len(rows)!=1||rows[0]["mode"]!="dry_run"||rows[0]["decision"]!="blocked" {
  t.Fatalf("audit persist: %+v %v",rows,e)
 }
 r=httpReq(t,h,"POST","/api/v1/admin/install/execute",bytes.NewReader(raw),true,"application/json")
 if r.Code==200||r.Code==201||r.Code==202 {t.Fatal("unsafe execute route exposed")}
}

func TestInstallPreflightRejectsInvalidInputAndTampering(t *testing.T){
 a,h:=testApp(t);id:=testInstallRecord(t,a)
 bad:=`{"firmware_id":"../oops","variant_id":"s5-global-r1","device":{}}`
 r:=httpReq(t,h,"POST","/api/v1/admin/install/dry-runs",strings.NewReader(bad),true,"application/json")
 if r.Code!=400{t.Fatalf("unsafe ID: %d",r.Code)}
 // After a file changes its hash, preflight must report file_integrity=fail.
 if err:=os.WriteFile(filepath.Join(a.StorageDir,id+".blob"),[]byte("modified"),0600);err!=nil{t.Fatal(err)}
 request:=`{"firmware_id":"`+id+`","variant_id":"s5-global-r1","device":{"model_slug":"roborock-s5","variant_id":"s5-global-r1","hardware_id":"wrong","region":"cn","hardware_revision":"not-the-same","installed_version":"2.0","battery_percent":5,"charging":false}}`
 r=httpReq(t,h,"POST","/api/v1/admin/install/dry-runs",strings.NewReader(request),true,"application/json")
 if r.Code!=201||!bytes.Contains(r.Body.Bytes(),[]byte(`"id":"file_integrity","status":"fail"`)) {
  t.Fatalf("tamper should fail: %d %s",r.Code,r.Body.String())
 }
}
