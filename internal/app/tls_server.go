package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type NativeConfig struct {
	Mode, PublicHost, ListenHost string
	Port int
	CertFile, KeyFile string
}
var nativeDomain = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func NativeConfigFromEnv() (NativeConfig,error) {
	c:=NativeConfig{
		Mode:os.Getenv("ACCESS_MODE"), PublicHost:strings.TrimSpace(os.Getenv("PUBLIC_HOST")),
		ListenHost:os.Getenv("LISTEN_HOST"), CertFile:os.Getenv("TLS_CERT_FILE"),
		KeyFile:os.Getenv("TLS_KEY_FILE"),
	}
	username,_,basicErr:=loadBasicCredentials()
	if basicErr!=nil{return c,basicErr}
	if username==""{return c,fmt.Errorf("native HTTPS requires ADMIN_BASIC_USER and ADMIN_BASIC_PASSWORD_B64")}
	if len(os.Getenv("ADMIN_API_TOKEN"))<24{return c,fmt.Errorf("ADMIN_API_TOKEN must have at least 24 characters")}
	if c.ListenHost=="" {c.ListenHost="0.0.0.0"}
	port:=os.Getenv("LISTEN_PORT")
	if port=="" {port="8443"}
	n,err:=strconv.Atoi(port)
	if err!=nil||n<1||n>65535{return c,fmt.Errorf("invalid LISTEN_PORT")}
	c.Port=n
	if c.Mode!="domain"&&c.Mode!="ip" {return c,fmt.Errorf("ACCESS_MODE must be domain or ip; local mode is disabled")}
	if c.Mode=="ip"{
		ip:=net.ParseIP(c.PublicHost)
		if ip==nil||!ip.IsGlobalUnicast()||ip.IsPrivate() {
			return c,fmt.Errorf("PUBLIC_HOST must be a public IPv4 or IPv6 address")
		}
	} else {
		if len(c.PublicHost)>253||net.ParseIP(c.PublicHost)!=nil||
			!nativeDomain.MatchString(c.PublicHost) {
			return c,fmt.Errorf("PUBLIC_HOST must be a valid public DNS hostname")
		}
		for _, suffix:=range []string{".localhost",".local",".internal",".test",".invalid",".example"}{
			if strings.HasSuffix(strings.ToLower(c.PublicHost),suffix){return c,fmt.Errorf("PUBLIC_HOST cannot be a local or reserved domain")}
		}
	}
	if c.ListenHost!="0.0.0.0"&&c.ListenHost!="::"{
		return c,fmt.Errorf("LISTEN_HOST may only bind public interfaces: 0.0.0.0 or ::")
	}
	if c.CertFile==""||c.KeyFile==""||!strings.HasPrefix(c.CertFile,"/")||!strings.HasPrefix(c.KeyFile,"/"){
		return c,fmt.Errorf("TLS_CERT_FILE and TLS_KEY_FILE must use absolute paths")
	}
	if _,err:=c.LoadCertificate();err!=nil{return c,err}
	return c,nil
}
func(c NativeConfig)LoadCertificate()(tls.Certificate,error){
	cert,err:=tls.LoadX509KeyPair(c.CertFile,c.KeyFile)
	if err!=nil{return cert,fmt.Errorf("invalid TLS certificate/key: %w",err)}
	if len(cert.Certificate)==0{return cert,fmt.Errorf("empty TLS certificate")}
	leaf,err:=x509.ParseCertificate(cert.Certificate[0])
	if err!=nil{return cert,fmt.Errorf("parse TLS certificate: %w",err)}
	if err:=leaf.VerifyHostname(c.PublicHost);err!=nil{
		return cert,fmt.Errorf("certificate does not cover %s: %w",c.PublicHost,err)
	}
	now:=time.Now()
	if now.Before(leaf.NotBefore)||now.After(leaf.NotAfter){
		return cert,fmt.Errorf("certificate not currently valid (expires %s)",leaf.NotAfter.UTC())
	}
	return cert,nil
}
func(c NativeConfig)Addr()string{return net.JoinHostPort(c.ListenHost,strconv.Itoa(c.Port))}
func(c NativeConfig)PublicURL()string{
	host:=c.PublicHost
	if strings.Contains(host,":"){host="["+host+"]"}
	if c.Port==443{return "https://"+host}
	return "https://"+host+":"+strconv.Itoa(c.Port)
}
func RunNative(c NativeConfig, handler http.Handler)error{
	cert,err:=c.LoadCertificate()
	if err!=nil{return err}
	srv:=&http.Server{
		Addr:c.Addr(),Handler:handler,ReadHeaderTimeout:10*time.Second,
		ReadTimeout:3*time.Minute,WriteTimeout:3*time.Minute,IdleTimeout:90*time.Second,
		MaxHeaderBytes:1<<20,
		TLSConfig:&tls.Config{MinVersion:tls.VersionTLS12,Certificates:[]tls.Certificate{cert}},
	}
	err=srv.ListenAndServeTLS("","")
	if err!=nil{return fmt.Errorf("serve HTTPS: %w",err)}
	return nil
}
