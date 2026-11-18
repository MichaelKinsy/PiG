//go:build !windows

package surfaces

// userLocale is the default locale of every system but Windows: the environment's (posixLocale).
func userLocale() string { return posixLocale() }
