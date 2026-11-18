//go:build windows

package surfaces

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// localeSName is LOCALE_SNAME: the locale's name (`de-DE`, `zh-Hans-CN`, `de-DE_phoneb`).
const localeSName = 0x5c

var procGetLocaleInfoEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetLocaleInfoEx")

// userLocale is the user's default locale as ICU's `uprv_getDefaultLocaleID` reads it on Windows (putil.cpp:1796-1869, the
// `U_PLATFORM_USES_ONLY_WIN32_API` branch): `GetLocaleInfoEx(LOCALE_NAME_USER_DEFAULT, LOCALE_SNAME)`, no environment variable.
func userLocale() string {
	var name [localeNameMaxLength]uint16
	// LOCALE_NAME_USER_DEFAULT is a null name.
	length, _, _ := procGetLocaleInfoEx.Call(0, localeSName, uintptr(unsafe.Pointer(&name[0])), localeNameMaxLength)
	if length == 0 {
		// putil.cpp:1808: "If length is 0, then the GetLocaleInfoEx failed": no locale ICU agrees on is en_US (:1861-1863).
		return windowsLocaleTag("")
	}
	return windowsLocaleTag(windows.UTF16ToString(name[:]))
}
