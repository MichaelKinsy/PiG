package surfaces

// PlatformLocale is the platform's default locale (ICU's, as `Intl.DateTimeFormat().resolvedOptions().locale` reports it): the
// first of LC_ALL, LC_MESSAGES, LANG that is set decides. The terminal launcher gives it to the sandbox after the user's own
// languages.
func PlatformLocale() string { return platformLocale() }
