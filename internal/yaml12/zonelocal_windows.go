//go:build windows

package yaml12

import (
	"os"
	"syscall"
	"time"
	_ "time/tzdata" // Windows has no zoneinfo directory for a TZ value to name
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// detectZoneID reads the zone the way Node's ICU does on Windows: TZ when it is set and not empty, otherwise the registry's TimeZoneKeyName mapped to an IANA zone through CLDR with the user's region.
func detectZoneID() string {
	tz, tzSet := os.LookupEnv("TZ")
	if tzSet && tz != "" {
		return windowsZoneIDFrom(tz, true, "", "")
	}
	return windowsZoneIDFrom("", false, timeZoneKeyName(), userRegion())
}

// hostLocation is the zone the process reads times in. Go ignores TZ on Windows, so a TZ value Node would honour is loaded here.
func hostLocation() *time.Location {
	if tz, set := os.LookupEnv("TZ"); set && tz != "" {
		if loc, ok := locationForTZ(tz); ok {
			return loc
		}
	}
	return time.Local
}

func timeZoneKeyName() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = key.Close() }()
	name, _, err := key.GetStringValue("TimeZoneKeyName")
	if err != nil {
		return ""
	}
	return name
}

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGetUserGeoID      = kernel32.NewProc("GetUserGeoID")
	procGetGeoInfoW       = kernel32.NewProc("GetGeoInfoW")
	procGetUserDefaultGeo = kernel32.NewProc("GetUserDefaultGeoName")
)

const (
	geoClassNation = 16
	geoISO2        = 4
)

// userRegion is the ISO 3166 code of the user's region (the "Country or region" setting), as ICU reads it, or "" when Windows does not say.
func userRegion() string {
	var buf [16]uint16
	// Windows 10 1709 and later name the region directly.
	if procGetUserDefaultGeo.Find() == nil {
		if n, _, _ := procGetUserDefaultGeo.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); n > 1 { //nolint:gosec // G103: GetUserDefaultGeoName writes into the caller's UTF-16 buffer, which the syscall takes as a uintptr.
			return syscall.UTF16ToString(buf[:n])
		}
	}
	if procGetUserGeoID.Find() != nil || procGetGeoInfoW.Find() != nil {
		return ""
	}
	id, _, _ := procGetUserGeoID.Call(geoClassNation)
	if id == 0 || id == 0xFFFFFFFF {
		return ""
	}
	n, _, _ := procGetGeoInfoW.Call(id, geoISO2, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0) //nolint:gosec // G103: GetGeoInfoW writes into the caller's UTF-16 buffer, which the syscall takes as a uintptr.
	if n < 2 {
		return ""
	}
	return syscall.UTF16ToString(buf[:])
}
