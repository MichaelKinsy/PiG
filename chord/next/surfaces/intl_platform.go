package surfaces

// PlatformLocale is the platform's default locale (ICU's, as `Intl.DateTimeFormat().resolvedOptions().locale` reports it).
// On Windows it is the user's default locale name (`GetLocaleInfoEx` with `LOCALE_NAME_USER_DEFAULT`; `en-US` when that fails) and
// no environment variable counts. On every other system, macOS included, the first of LC_ALL, LC_MESSAGES, LANG that is set
// decides (`en-US` when none is, or when it is `C` or `POSIX`). The terminal launcher gives it to the sandbox after the user's own
// languages.
func PlatformLocale() string { return platformLocale() }
