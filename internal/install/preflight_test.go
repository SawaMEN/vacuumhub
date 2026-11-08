package install

import (
 "encoding/json"
 "strings"
 "testing"
)

func goodInput() Input {
 return Input{
   FirmwareApproved:true, ScannedClean:true, RevisionDocumented:true, SourcePresent:true,
   FirmwareHashOK:true, VariantModel:"roborock-s5", VariantID:"s5-global-r1",
   VariantRegion:"global", VariantRevision:"r1", VerifiedHardwareID:true,
   TargetVersion:"2.0", Authenticity:"trusted_key_signature", DowngradePolicy:"documented",
   LiveProbeAvailable:true, RecoveryTested:true,
   Device:Observation{ModelSlug:"roborock-s5",VariantID:"s5-global-r1",
     HardwareID:"test-unique-secret-hardware-id",Region:"global",HardwareRevision:"r1",
     BatteryPercent:100,Charging:true,InstalledVersion:"1.0",Bootloader:"verified_supported"},
 }
}
func find(report Report,id string) string {
 for _,c:=range report.Checks { if c.ID==id {return c.Status} }
 return ""
}
func TestFailClosedEvenIfAllFieldsLookGood(t *testing.T) {
 r:=Evaluate(goodInput())
 if r.ExecutionAllowed || r.Decision!="blocked" || r.Mode!="dry_run" {t.Fatalf("unsafe result: %+v",r)}
 if find(r,"live_probe")!="unverified" || find(r,"recovery")!="unverified" || find(r,"flashing")!="fail" {
  t.Fatalf("unverified hardware evidence was trusted: %+v",r)
 }
}
func TestPreflightRejectsMismatches(t *testing.T) {
 cases:=[]struct{name string;change func(*Input);check string}{
  {"wrong model",func(i *Input){i.Device.ModelSlug="different"},"model"},
  {"wrong variant",func(i *Input){i.Device.VariantID="other"},"variant"},
  {"wrong region",func(i *Input){i.Device.Region="cn"},"region"},
  {"wrong revision",func(i *Input){i.Device.HardwareRevision="r2"},"revision"},
  {"unverified hardware",func(i *Input){i.VerifiedHardwareID=false},"hardware_id"},
  {"low battery",func(i *Input){i.Device.BatteryPercent=30},"power"},
  {"no charging",func(i *Input){i.Device.Charging=false},"power"},
  {"same version",func(i *Input){i.Device.InstalledVersion="2.0"},"current_version"},
  {"unknown downgrade",func(i *Input){i.DowngradePolicy="unknown"},"downgrade"},
  {"no scanner",func(i *Input){i.ScannedClean=false},"malware_scan"},
  {"changed file",func(i *Input){i.FirmwareHashOK=false},"file_integrity"},
  {"no provenance",func(i *Input){i.SourcePresent=false},"revision_mapping"},
 }
 for _,tc:=range cases {
  t.Run(tc.name,func(t *testing.T){i:=goodInput();tc.change(&i);r:=Evaluate(i)
   if find(r,tc.check)!="fail" || r.ExecutionAllowed {t.Fatalf("%s: %+v",tc.name,r)}
  })
 }
}
func TestReportNeverLeaksDeviceIdentifier(t *testing.T) {
 in:=goodInput()
 raw,e:=json.Marshal(Evaluate(in)); if e!=nil {t.Fatal(e)}
 if strings.Contains(string(raw),in.Device.HardwareID) {t.Fatalf("sensitive ID leaked in report: %s",raw)}
}
