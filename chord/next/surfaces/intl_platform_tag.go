package surfaces

import "strings"

// localeNameMaxLength is Windows' LOCALE_NAME_MAX_LENGTH, the size of the buffer ICU reads the name into.
const localeNameMaxLength = 85

// platformLocale is the platform's default locale: the user's (userLocale). A variable so a test of a format that follows the
// environment can say so on every system (useEnvironmentLocale): Windows reads no environment variable.
var platformLocale = userLocale

// windowsLocaleTag is the default locale ICU makes of the name Windows gives for the user's locale
// (`GetLocaleInfoEx(LOCALE_NAME_USER_DEFAULT, LOCALE_SNAME)`), as `uprv_getDefaultLocaleID` does on Windows (ICU 78
// icu4c/source/common/putil.cpp, which Node v24.22.2's deps/icu-small carries unchanged): the name's `_` become `-` (the loop at
// :1816-1829; a sort name `de-DE_phoneb` is `de-DE-phoneb`, a variant the locale resolution leaves out), cut to the buffer's 84
// characters (:1833-1838), canonicalized (`uloc_canonicalize`, :1850; PiG's resolution applies the same alias rules to the tag
// it is given), and `en_US` when the name is empty (the call failed) or cannot be made a locale (:1861-1863).
// It is here and not in the Windows file so a test on every system holds it.
func windowsLocaleTag(name string) string {
	name = strings.ReplaceAll(name, "_", "-")
	if len(name) > localeNameMaxLength-1 {
		name = name[:localeNameMaxLength-1]
	}
	if name == "" {
		return "en-US"
	}
	if _, err := parseTag(name); err != nil {
		return "en-US"
	}
	return name
}
