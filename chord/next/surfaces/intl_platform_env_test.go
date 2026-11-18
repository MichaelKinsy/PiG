package surfaces

import "testing"

// useEnvironmentLocale makes the platform's default locale the one LC_ALL, LC_MESSAGES and LANG name for the test, on every system:
// Windows reads the user's locale instead (userLocale; the live comparison with Node is intl_platform_windows_test.go).
func useEnvironmentLocale(t *testing.T) {
	t.Helper()
	saved := platformLocale
	platformLocale = posixLocale
	t.Cleanup(func() { platformLocale = saved })
}
