package surfaces

import (
	"reflect"
	"testing"
)

// pi: packages/chord/test/surfaces/language.test.ts

func newLanguage(t testing.TB, locale string) *Language {
	t.Helper()
	lang, err := CreateLanguage(locale, NoTranslations, func(MessagesReport) {})
	if err != nil {
		t.Fatal(err)
	}
	return lang
}

func TestLanguageHasItsLocalesDirectionAndChainOfBases(t *testing.T) {
	for locale, dir := range map[string]string{"de": "ltr", "ar": "rtl", "he-IL": "rtl"} {
		if got := newLanguage(t, locale).Dir; got != dir {
			t.Errorf("%s dir = %s, want %s", locale, got, dir)
		}
	}
	for locale, want := range map[string][]string{
		"pt-BR":      {"pt-BR", "pt"},
		"zh-Hant-TW": {"zh-Hant-TW", "zh-Hant", "zh"},
	} {
		if got := newLanguage(t, locale).Chain; !reflect.DeepEqual(got, want) {
			t.Errorf("%s chain = %v, want %v", locale, got, want)
		}
	}
}

func TestMatchLocaleTakesTheFirstPreferenceOffered(t *testing.T) {
	for _, c := range []struct {
		preferred, offered []string
		want               string
		ok                 bool
	}{
		{[]string{"fr", "de"}, []string{"de", "en"}, "de", true},
		{[]string{"de-AT"}, []string{"de", "en"}, "de", true},
		{[]string{"de-AT"}, []string{"de-DE", "en"}, "de-DE", true},
		{[]string{"pt-br"}, []string{"pt", "pt-BR"}, "pt-BR", true},
		{[]string{"ja"}, []string{"de", "en"}, "", false},
		// A device language that is no tag is skipped.
		{[]string{"not a tag", "de"}, []string{"de"}, "de", true},
	} {
		got, ok := MatchLocale(c.preferred, c.offered)
		if got != c.want || ok != c.ok {
			t.Errorf("MatchLocale(%v, %v) = %q, %v; want %q, %v", c.preferred, c.offered, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveLocalePicksTheUsersFirstThenTheDevicesThenEnglishThenTheFirstOffered(t *testing.T) {
	fr, it := "fr", "it"
	for _, c := range []struct {
		pick               *string
		preferred, offered []string
		want               string
	}{
		{&fr, []string{"de"}, []string{"de", "en", "fr"}, "fr"},
		{&it, []string{"de"}, []string{"de", "en", "fr"}, "de"},
		{nil, []string{"ja"}, []string{"de", "en"}, "en"},
		{nil, []string{"ja"}, []string{"de", "fr"}, "de"},
		{nil, nil, nil, "en"},
	} {
		if got := ResolveLocale(c.pick, c.preferred, c.offered); got != c.want {
			t.Errorf("ResolveLocale(%v, %v, %v) = %q, want %q", c.pick, c.preferred, c.offered, got, c.want)
		}
	}
}

func TestCreateLanguageRefusesATagThatIsNone(t *testing.T) {
	if _, err := CreateLanguage("not a tag", NoTranslations, nil); err == nil {
		t.Fatal("CreateLanguage accepted a tag that is none")
	}
}
