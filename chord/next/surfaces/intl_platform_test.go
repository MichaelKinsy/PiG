package surfaces

import (
	"os"
	"testing"
)

// Review RevChordIntl P2-1: the platform's default locale is that of the FIRST set variable of LC_ALL, LC_MESSAGES, LANG (ICU's own
// order: a variable that is set decides, whatever it holds); `C` and `POSIX` are en-US; a set-but-empty variable is `und`, ICU's root
// locale (formats: intl_root_test.go).
func TestPlatformLocaleIsThatOfTheFirstSetVariable(t *testing.T) {
	cases := []struct {
		name                  string
		all, messages, lang   string
		setAll, setMsg, setLg bool
		want                  string
	}{
		{name: "LC_ALL first", all: "de_DE.UTF-8", setAll: true, messages: "fr_FR", setMsg: true, lang: "es_ES", setLg: true, want: "de-DE"},
		{name: "LC_MESSAGES before LANG", messages: "fr_FR", setMsg: true, lang: "es_ES", setLg: true, want: "fr-FR"},
		{name: "LANG alone", lang: "es_ES@euro", setLg: true, want: "es-ES"},
		{name: "C is en-US even with a later tag", all: "C", setAll: true, lang: "de_DE", setLg: true, want: "en-US"},
		{name: "POSIX is en-US", messages: "POSIX", setMsg: true, lang: "de_DE", setLg: true, want: "en-US"},
		{name: "a set variable that is no tag stops the search", all: "not a tag", setAll: true, lang: "de_DE", setLg: true, want: "en-US"},
		{name: "a set-but-empty variable is und", all: "", setAll: true, lang: "de_DE", setLg: true, want: "und"},
		{name: "none set", want: "en-US"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for name, v := range map[string]struct {
				value string
				set   bool
			}{"LC_ALL": {c.all, c.setAll}, "LC_MESSAGES": {c.messages, c.setMsg}, "LANG": {c.lang, c.setLg}} {
				if v.set {
					t.Setenv(name, v.value)
				} else {
					t.Setenv(name, "")
					unsetenv(t, name)
				}
			}
			if got := platformLocale(); got != c.want {
				t.Fatalf("platformLocale() = %q, want %q", got, c.want)
			}
		})
	}
}

func unsetenv(t *testing.T, name string) {
	t.Helper()
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}
