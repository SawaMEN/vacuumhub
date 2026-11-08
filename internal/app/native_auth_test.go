package app

import (
 "bytes"
 "encoding/base64"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"

 "github.com/SawaMEN/vacuumhub/internal/store"
)

func TestNativeBasicProtectsPageAndAPI(t *testing.T){
 t.Setenv("ADMIN_BASIC_USER","vacadmin")
 t.Setenv("ADMIN_BASIC_PASSWORD_B64",base64.StdEncoding.EncodeToString([]byte("long-test-password-unique")))
 db,err:=store.Open(filepath.Join(t.TempDir(),"native.sqlite"))
 if err!=nil{t.Fatal(err)}
 defer db.Close()
 if err:=db.Migrate();err!=nil{t.Fatal(err)}
 if err:=db.Seed();err!=nil{t.Fatal(err)}
 a,err:=New(db,filepath.Join(t.TempDir(),"data"),testToken,"")
 if err!=nil{t.Fatal(err)}
 h:=a.Handler()
 makeRequest:=func(method,path,body string,auth,csrf bool,origin string)*httptest.ResponseRecorder{
  req:=httptest.NewRequest(method,"https://example.com"+path,strings.NewReader(body))
  if auth{req.SetBasicAuth("vacadmin","long-test-password-unique")}
  if csrf{req.Header.Set("X-Vacuumhub-Request","same-origin")}
  if origin!=""{req.Header.Set("Origin",origin)}
  if body!=""{req.Header.Set("Content-Type","application/json")}
  rec:=httptest.NewRecorder();h.ServeHTTP(rec,req);return rec
 }
 for _,path:=range []string{"/admin.html","/api/v1/admin/assets"} {
  r:=makeRequest("GET",path,"",false,false,"")
  if r.Code!=401||r.Header().Get("WWW-Authenticate")=="" {t.Fatalf("unprotected %s: %d",path,r.Code)}
  r=makeRequest("GET",path,"",true,false,"")
  if r.Code!=200{t.Fatalf("authorized %s: %d %s",path,r.Code,r.Body.String())}
 }
 req:=makeRequest("POST","/api/v1/admin/models",`{"slug":"native-test-model","brand":"Roborock","name":"Native"}`,true,false,"")
 if req.Code!=403{t.Fatalf("CSRF form accepted %d",req.Code)}
 req=makeRequest("POST","/api/v1/admin/models",`{"slug":"native-test-model","brand":"Roborock","name":"Native"}`,true,true,"https://attacker.example")
 if req.Code!=403{t.Fatalf("cross-origin accepted %d",req.Code)}
 req=makeRequest("POST","/api/v1/admin/models",`{"slug":"native-test-model","brand":"Roborock","name":"Native"}`,true,true,"https://example.com")
 if req.Code!=201{t.Fatalf("native admin write failed %d %s",req.Code,req.Body.String())}
 req=makeRequest("GET","/api/v1/auth-mode","",false,false,"")
 if req.Code!=200||!bytes.Contains(req.Body.Bytes(),[]byte("basic")){t.Fatal(req.Body.String())}
 req=makeRequest("GET","/","",false,false,"")
 if req.Code!=200||!bytes.Contains(req.Body.Bytes(),[]byte("VacuumHub")){t.Fatal("embedded frontend unavailable")}
 req=makeRequest("GET","/admin.html","",true,false,"")
 if req.Header().Get("Cache-Control")!="no-store"{t.Fatal("admin HTML cacheable")}
}
func TestNativeCredentialsRequiredAndMalformedRejected(t *testing.T){
 t.Setenv("ADMIN_BASIC_USER","test")
 t.Setenv("ADMIN_BASIC_PASSWORD_B64","invalid")
 _,_,err:=loadBasicCredentials()
 if err==nil{t.Fatal("invalid password accepted")}
 t.Setenv("ADMIN_BASIC_USER","")
 t.Setenv("ADMIN_BASIC_PASSWORD_B64","")
 u,p,err:=loadBasicCredentials()
 if err!=nil||u!=""||p!=""{t.Fatal("token-only test auth should remain available")}
}
func TestNativeFileServerDoesNotNeedExternalAssets(t *testing.T){
 t.Setenv("ADMIN_BASIC_USER","")
 t.Setenv("ADMIN_BASIC_PASSWORD_B64","")
 db,err:=store.Open(filepath.Join(t.TempDir(),"embedded.sqlite"))
 if err!=nil{t.Fatal(err)}
 defer db.Close()
 if err:=db.Migrate();err!=nil{t.Fatal(err)}
 a,err:=New(db,filepath.Join(t.TempDir(),"uploads"),testToken,"")
 if err!=nil{t.Fatal(err)}
 r:=httptest.NewRecorder()
 a.Handler().ServeHTTP(r,httptest.NewRequest("GET","https://example.org/style.css",nil))
 if r.Code!=200||!strings.Contains(r.Body.String(),"container"){t.Fatalf("asset not embedded: %d",r.Code)}
}
