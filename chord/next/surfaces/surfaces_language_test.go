package surfaces

import (
	"context"
	"errors"
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

// Ports "the render scope > is current only while a render runs, and a nested render restores the outer" with Pi's inputs
// and expected results. Pi's scope is a module variable a synchronous render sets and restores; Go's is the context the
// render is given (language mechanics: the language is the context's): InLanguage hands render a context carrying the language, and the outer context is untouched
// when it returns or panics.
func TestTheRenderScopeIsCurrentOnlyWhileARenderRunsAndANestedRenderRestoresTheOuter(t *testing.T) {
	de, fr := newLanguage(t, "de"), newLanguage(t, "fr")
	root := context.Background()
	if CurrentLanguage(root) != nil {
		t.Fatal("a language is current outside a render")
	}
	seen := InLanguage(root, de, func(outer context.Context) [3]*Language {
		return [3]*Language{
			CurrentLanguage(outer),
			InLanguage(outer, fr, CurrentLanguage),
			CurrentLanguage(outer),
		}
	})
	if seen != [3]*Language{de, fr, de} {
		t.Fatalf("seen = %v, want [de fr de]", seen)
	}
	if CurrentLanguage(root) != nil {
		t.Fatal("a language outlived its render")
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != "render failed" {
				t.Fatalf("recovered %v, want the render's panic", recovered)
			}
		}()
		InLanguage(root, de, func(context.Context) struct{} { panic("render failed") })
	}()
	if CurrentLanguage(root) != nil {
		t.Fatal("a language outlived a render that failed")
	}
	// A render in no language is the scope with none current, inside another.
	if got := InLanguage(root, de, func(outer context.Context) *Language {
		return InLanguage(outer, nil, CurrentLanguage)
	}); got != nil {
		t.Fatalf("a nested render in no language sees %v, want none", got)
	}
}

// Pi's t outside In renders in the current language: a Messages in a scope renders in the language the context carries.
func TestMessagesInAScopeRenderInTheCurrentLanguage(t *testing.T) {
	notes := mustMessages(t, "notes",
		LocaleCatalog{"en", Catalog{"hello": "Hello"}},
		LocaleCatalog{"de", Catalog{"hello": "Hallo"}})
	de := newLanguage(t, "de")
	root := context.Background()
	if got, err := notes.InScope(root).T("hello"); err != nil || got != "Hello" {
		t.Fatalf("outside a render = %q, %v, want the first catalog", got, err)
	}
	got := InLanguage(root, de, func(ctx context.Context) string {
		text, err := notes.InScope(ctx).T("hello")
		if err != nil {
			t.Fatal(err)
		}
		return text
	})
	if got != "Hallo" {
		t.Fatalf("in de = %q, want Hallo", got)
	}
}

// InLanguage's context is derived from the one it is given: a value and a cancellation of the parent reach the render, which is
// what lets a render honour the caller's deadline and carry its trace.
func TestInLanguageHandsTheRenderTheParentsValuesAndCancellation(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "carried"))
	de := newLanguage(t, "de")
	saw := InLanguage(parent, de, func(ctx context.Context) [3]any {
		before := ctx.Err()
		cancel()
		return [3]any{ctx.Value(key{}), before, ctx.Err()}
	})
	after, _ := saw[2].(error)
	if saw[0] != "carried" || saw[1] != nil || !errors.Is(after, context.Canceled) {
		t.Fatalf("the render saw value %v, error before cancel %v, error after %v; want carried, nil, context canceled", saw[0], saw[1], saw[2])
	}
}
