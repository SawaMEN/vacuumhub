// Package install defines a fail-closed, non-executing preflight workflow.
//
// No production device adapter, firmware transport, or flash operation is
// registered in this release. An operator-supplied observation is never
// treated as trusted live device telemetry.
package install

import "context"

// Probe is the boundary future *model-specific* adapters must implement.
// A probe must retrieve data from an actual authenticated local device.
type Probe interface {
	ID() string
	Observe(context.Context, string) (Observation, error)
}

// Observation is UNTRUSTED when received from the current HTTP API.
type Observation struct {
	ModelSlug       string `json:"model_slug"`
	VariantID       string `json:"variant_id"`
	HardwareID      string `json:"hardware_id"`
	Region          string `json:"region"`
	HardwareRevision string `json:"hardware_revision"`
	InstalledVersion string `json:"installed_version"`
	BatteryPercent   int    `json:"battery_percent"`
	Charging         bool   `json:"charging"`
	Bootloader       string `json:"bootloader"`
}

// Input is assembled exclusively on the server. Firmware and catalog
// properties are read from SQLite; clients cannot set those values.
type Input struct {
	FirmwareApproved   bool
	ScannedClean       bool
	RevisionDocumented bool
	SourcePresent      bool
	FirmwareHashOK     bool
	VariantModel       string
	VariantID          string
	VariantRegion      string
	VariantRevision    string
	VerifiedHardwareID bool
	TargetVersion      string
	Authenticity       string
	DowngradePolicy    string
	Device             Observation
	LiveProbeAvailable bool
	RecoveryTested     bool
}

type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"` // pass, fail, reported_match or unverified
	Detail string `json:"detail"`
}

type Report struct {
	Mode             string  `json:"mode"`
	Decision         string  `json:"decision"`
	ExecutionAllowed bool    `json:"execution_allowed"`
	Checks           []Check `json:"checks"`
	Notice           string  `json:"notice"`
}

// Evaluate never produces an "allowed" decision. The next prerequisite is
// a separately reviewed physical-device adapter AND verified recovery testing.
// Do not add a flash/execute endpoint until that work has been completed.
func Evaluate(in Input) Report {
	out := Report{
		Mode: "dry_run",
		Decision: "blocked",
		ExecutionAllowed: false,
		Notice: "Только предварительная оценка. Данные об устройстве введены вручную, физическая установка отключена.",
	}
	add := func(id string, ok bool, detail string) {
		status := "fail"
		if ok {
			status = "pass"
		}
		out.Checks = append(out.Checks, Check{ID: id, Status: status, Detail: detail})
	}
	claimed := func(id string, matches bool, detail string) {
		status := "fail"
		if matches {
			status = "reported_match"
		}
		out.Checks = append(out.Checks, Check{ID: id, Status: status, Detail: detail})
	}
	add("firmware_published", in.FirmwareApproved, "Прошивка должна быть опубликована после модерации")
	add("malware_scan", in.ScannedClean, "Для публикации обязательна проверка ClamAV")
	add("revision_mapping", in.RevisionDocumented && in.SourcePresent,
		"Требуются документированная ревизия и подтверждаемый источник")
	add("file_integrity", in.FirmwareHashOK, "SHA-256 локального файла должен соответствовать каталогу")
	claimed("model", in.VariantModel != "" && in.Device.ModelSlug == in.VariantModel,
		"Модель устройства должна точно совпадать с записью прошивки")
	claimed("variant", in.VariantID != "" && in.Device.VariantID == in.VariantID,
		"Аппаратный вариант должен точно совпадать")
	claimed("region", in.VariantRegion != "" && in.Device.Region == in.VariantRegion,
		"Регион должен совпадать без автоматического преобразования")
	claimed("revision", in.VariantRevision != "" && in.Device.HardwareRevision == in.VariantRevision,
		"Требуется точная аппаратная ревизия")
	claimed("hardware_id", in.VerifiedHardwareID && in.Device.HardwareID != "",
		"Аппаратный ID должен совпадать с проверенной записью каталога")
	claimed("power", in.Device.BatteryPercent >= 70 && in.Device.BatteryPercent <= 100 && in.Device.Charging,
		"Консервативная проверка: заряд не менее 70% и питание подключено")
	claimed("current_version", in.Device.InstalledVersion != "" && in.Device.InstalledVersion != in.TargetVersion,
		"Нужно определить текущую версию; повторная запись той же версии не разрешена")
	claimed("bootloader", in.Device.Bootloader == "verified_supported",
		"Требуется подтверждение поддерживаемого загрузчика")
	add("downgrade", in.DowngradePolicy != "unknown" && in.DowngradePolicy != "",
		"Ограничения отката должны быть исследованы; автоматическое сравнение версий отсутствует")
	out.Checks = append(out.Checks,
		Check{ID: "live_probe", Status: "unverified", Detail: "Нет аттестованного адаптера для получения данных непосредственно с устройства"},
		Check{ID: "recovery", Status: "unverified", Detail: "Нет подтверждённых тестов восстановления на физическом устройстве"},
		Check{ID: "authenticity", Status: "unverified", Detail: "Ключ оператора и антивирус не доказывают официальное происхождение прошивки"},
		Check{ID: "flashing", Status: "fail", Detail: "Запись прошивки отключена; endpoint установки отсутствует"},
	)
	// No caller can turn these off by setting its own booleans.
	_ = in.LiveProbeAvailable
	_ = in.RecoveryTested
	return out
}
