package main

import (
 "fmt"
 "log/slog"
 "os"
 "path/filepath"

 "github.com/SawaMEN/vacuumhub/internal/app"
 "github.com/SawaMEN/vacuumhub/internal/store"
)

func env(name, def string) string {
 if v:=os.Getenv(name);v!=""{return v}
 return def
}
func main(){
 if len(os.Args)==2 && os.Args[1]=="--verify-data"{
  if err:=verifyOfflineData(env("DATA_DIR","/var/lib/vacuumhub"));err!=nil{
   slog.Error("offline verification failed","error",err);os.Exit(1)
  }
  fmt.Println("SQLite and all referenced files verified")
  return
 }
 if len(os.Args)>2 || (len(os.Args)==2 && os.Args[1]!="--check-config"){
  fmt.Fprintln(os.Stderr,"usage: vacuumhub [--check-config|--verify-data]")
  os.Exit(2)
 }
 config,err:=app.NativeConfigFromEnv()
 if err!=nil{slog.Error("server configuration","error",err);os.Exit(1)}
 if len(os.Args)==2 {
  fmt.Println("Configuration OK:",config.PublicURL())
  return
 }
 dataDir:=env("DATA_DIR","/var/lib/vacuumhub")
 if err:=os.MkdirAll(dataDir,0700);err!=nil{slog.Error("data dir","error",err);os.Exit(1)}
 db,err:=store.Open(filepath.Join(dataDir,"vacuumhub.sqlite"))
 if err!=nil{slog.Error("database","error",err);os.Exit(1)}
 defer db.Close()
 if err:=db.Migrate();err!=nil{slog.Error("migrate","error",err);os.Exit(1)}
 if err:=db.Seed();err!=nil{slog.Error("seed","error",err);os.Exit(1)}
 // All frontend files are embedded in the binary. No separate local web root.
 server,err:=app.New(db,filepath.Join(dataDir,"assets"),os.Getenv("ADMIN_API_TOKEN"),"")
 if err!=nil{slog.Error("security configuration","error",err);os.Exit(1)}
 slog.Info("VacuumHub HTTPS ready","url",config.PublicURL())
 if err:=app.RunNative(config,server.Handler());err!=nil{slog.Error("server","error",err);os.Exit(1)}
}
func verifyOfflineData(dir string) error {
 path:=filepath.Join(dir,"vacuumhub.sqlite")
 stat,err:=os.Stat(path)
 if err!=nil{return err}
 if !stat.Mode().IsRegular()||stat.Size()==0{return fmt.Errorf("invalid SQLite backup at %s",path)}
 db,err:=store.Open(path)
 if err!=nil{return err}
 defer db.Close()
 return store.VerifyData(db,filepath.Join(dir,"assets"))
}
