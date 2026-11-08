package app

import (
 "crypto/subtle"
 "encoding/base64"
 "errors"
 "net/http"
 "net/url"
 "os"
 "regexp"
 "strings"
)

var basicUserRE=regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{1,47}$`)

func loadBasicCredentials()(string,string,error){
 user:=os.Getenv("ADMIN_BASIC_USER")
 encoded:=os.Getenv("ADMIN_BASIC_PASSWORD_B64")
 if user=="" && encoded=="" {return "","",nil}
 if !basicUserRE.MatchString(user)||encoded=="" {return "","",errors.New("invalid ADMIN_BASIC_USER or missing password")}
 raw,err:=base64.StdEncoding.DecodeString(encoded)
 if err!=nil||len(raw)<12||len(raw)>256 {return "","",errors.New("ADMIN_BASIC_PASSWORD_B64 must decode to 12-256 bytes")}
 return user,string(raw),nil
}
func (a *App) basicAuthenticated(r *http.Request)bool{
 if a.AdminUsername=="" || a.AdminPassword=="" {return false}
 username,password,ok:=r.BasicAuth()
 if !ok {return false}
 return len(username)==len(a.AdminUsername) && len(password)==len(a.AdminPassword) &&
 subtle.ConstantTimeCompare([]byte(username),[]byte(a.AdminUsername))==1 &&
 subtle.ConstantTimeCompare([]byte(password),[]byte(a.AdminPassword))==1
}
func(a *App)basicChallenge(w http.ResponseWriter){
 w.Header().Set("WWW-Authenticate",`Basic realm="VacuumHub administration", charset="UTF-8"`)
 w.Header().Set("Cache-Control","no-store")
 problem(w,http.StatusUnauthorized,"administrator authentication required")
}
// Native browser mutations use a non-simple header, which cross-origin HTML
// forms cannot set. Cross-origin fetch() with this header triggers a CORS
// preflight that this server never authorizes. Reject a contradictory Origin.
func nativeRequestAllowed(r *http.Request)bool{
 if r.Method==http.MethodGet||r.Method==http.MethodHead {return true}
 if r.Header.Get("X-Vacuumhub-Request")!="same-origin" {return false}
 if origin:=r.Header.Get("Origin"); origin!="" {
  u,err:=url.Parse(origin)
  if err!=nil || u.Scheme!="https" || !strings.EqualFold(u.Host,r.Host) {return false}
 }
 return true
}
