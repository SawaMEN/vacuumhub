package app

import (
 "crypto/ecdsa"
 "crypto/elliptic"
 "crypto/rand"
 "crypto/x509"
 "crypto/x509/pkix"
 "encoding/base64"
 "encoding/pem"
 "math/big"
 "net"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

func writeNativeCert(t *testing.T, dns string, ip net.IP)(string,string){
 t.Helper()
 key,e:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader)
 if e!=nil{t.Fatal(e)}
 cert:=&x509.Certificate{
  SerialNumber:big.NewInt(1), Subject:pkix.Name{CommonName:"VacuumHub Test"},
  NotBefore:time.Now().Add(-time.Hour),NotAfter:time.Now().Add(24*time.Hour),
  KeyUsage:x509.KeyUsageDigitalSignature,ExtKeyUsage:[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
 }
 if dns!=""{cert.DNSNames=[]string{dns}}
 if ip!=nil{cert.IPAddresses=[]net.IP{ip}}
 der,e:=x509.CreateCertificate(rand.Reader,cert,cert,&key.PublicKey,key)
 if e!=nil{t.Fatal(e)}
 pk,e:=x509.MarshalECPrivateKey(key)
 if e!=nil{t.Fatal(e)}
 dir:=t.TempDir()
 cp,kp:=filepath.Join(dir,"cert.pem"),filepath.Join(dir,"key.pem")
 if e=os.WriteFile(cp,pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:der}),0600);e!=nil{t.Fatal(e)}
 if e=os.WriteFile(kp,pem.EncodeToMemory(&pem.Block{Type:"EC PRIVATE KEY",Bytes:pk}),0600);e!=nil{t.Fatal(e)}
 return cp,kp
}
func TestNativeAccessModesAndCertificateSAN(t *testing.T){
 t.Setenv("ADMIN_BASIC_USER","admin")
 t.Setenv("ADMIN_BASIC_PASSWORD_B64",base64.StdEncoding.EncodeToString([]byte("secure-password-18chars")))
 t.Setenv("ADMIN_API_TOKEN",testToken)
 t.Setenv("LISTEN_HOST","0.0.0.0")
 t.Setenv("LISTEN_PORT","8443")
 cp,kp:=writeNativeCert(t,"example.org",nil)
 t.Setenv("TLS_CERT_FILE",cp)
 t.Setenv("TLS_KEY_FILE",kp)
 t.Setenv("ACCESS_MODE","domain")
 t.Setenv("PUBLIC_HOST","example.org")
 cfg,err:=NativeConfigFromEnv()
 if err!=nil||cfg.PublicURL()!="https://example.org:8443"{t.Fatalf("domain: %+v %v",cfg,err)}
 for _,tc:=range []struct{mode,host string}{
  {"local","example.org"},{"ip","127.0.0.1"},{"ip","192.168.1.2"},
  {"domain","localhost"},{"domain","example.local"},{"domain","127.0.0.1"},
  {"ip","203.0.113.3"}, // SAN mismatches domain certificate
 }{
  t.Setenv("ACCESS_MODE",tc.mode)
  t.Setenv("PUBLIC_HOST",tc.host)
  if _,err:=NativeConfigFromEnv();err==nil{t.Fatalf("invalid %s/%s allowed",tc.mode,tc.host)}
 }
 ip:=net.ParseIP("203.0.113.15")
 cp,kp=writeNativeCert(t,"",ip)
 t.Setenv("TLS_CERT_FILE",cp)
 t.Setenv("TLS_KEY_FILE",kp)
 t.Setenv("ACCESS_MODE","ip")
 t.Setenv("PUBLIC_HOST","203.0.113.15")
 cfg,err=NativeConfigFromEnv()
 if err!=nil||cfg.Mode!="ip" {t.Fatalf("IP SAN: %+v %v",cfg,err)}
 t.Setenv("LISTEN_HOST","127.0.0.1")
 if _,err:=NativeConfigFromEnv();err==nil{t.Fatal("local-only listener allowed")}
 t.Setenv("LISTEN_HOST","::")
 if _,err:=NativeConfigFromEnv();err!=nil{t.Fatal("IPv6-wide binding rejected",err)}
 t.Setenv("PUBLIC_HOST","example.org")
 if _,err:=NativeConfigFromEnv();err==nil{t.Fatal("domain accepted in IP mode")}
}
func TestNativeNoPlainHTTPFallback(t *testing.T) {
 t.Setenv("ADMIN_API_TOKEN",testToken)
 t.Setenv("ACCESS_MODE","")
 t.Setenv("PUBLIC_HOST","")
 t.Setenv("ADMIN_BASIC_USER","admin")
 t.Setenv("ADMIN_BASIC_PASSWORD_B64",base64.StdEncoding.EncodeToString([]byte("secure-password-18chars")))
 if _,err:=NativeConfigFromEnv();err==nil||!strings.Contains(err.Error(),"ACCESS_MODE"){t.Fatalf("missing HTTPS mode accepted: %v",err)}
}
