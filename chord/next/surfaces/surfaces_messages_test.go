package surfaces

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/test/surfaces/messages.test.ts
// The cases of "defineMessages types every catalog by the first" are compile-time (`@ts-expect-error`) and have no Go
// form (doc.go, D450).

func count(plural, one string) MessageFunc {
	return func(lang *Language, args ...any) (string, error) {
		n := args[0].(float64)
		if n == 1 {
			return one, nil
		}
		formatted, err := FormatNumber(lang, n, nil)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s", formatted, plural), nil
	}
}

func mustMessages(t testing.TB, namespace string, catalogs ...LocaleCatalog) *Messages {
	t.Helper()
	m, err := DefineMessages(namespace, catalogs...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func notesMessages(t testing.TB) *Messages {
	saved := func(format string) MessageFunc {
		return func(_ *Language, args ...any) (string, error) { return fmt.Sprintf(format, args...), nil }
	}
	when := func(_ *Language, args ...any) (string, error) {
		at, ok := args[0].(interface{ ISOString() string })
		if !ok {
			return "", fmt.Errorf("at.toISOString is not a function")
		}
		return at.ISOString(), nil
	}
	return mustMessages(t, "notes",
		LocaleCatalog{"en", Catalog{"newNote": "New note", "empty": "No notes yet", "count": count("notes", "1 note"), "saved": saved(`Saved "%s" at %s`), "when": MessageFunc(when)}},
		LocaleCatalog{"de", Catalog{"newNote": "Neue Notiz", "empty": "Noch keine Notizen", "count": count("Notizen", "1 Notiz"), "saved": saved("„%s“ um %s gespeichert"), "when": MessageFunc(when)}},
		LocaleCatalog{"pt", Catalog{"newNote": "Nova nota", "empty": "Sem notas", "count": MessageFunc(func(_ *Language, args ...any) (string, error) { return fmt.Sprintf("%v notas", args[0]), nil }), "saved": saved("%s"), "when": MessageFunc(when)}},
	)
}

// languageOf is a language for locale with the contributed translations, its reports collected in reports.
func languageOf(t testing.TB, locale string, reports *[]MessagesReport, translations ...Translation) *Language {
	t.Helper()
	table := registries.RegistryTable[[]Translation]{{Plugin: "@test/fr", Version: 1, Value: translations}}
	index, err := ComposeTranslations(table, registries.ComposeContext{Report: func(registries.RegistryConflict) {}})
	if err != nil {
		t.Fatal(err)
	}
	lang, err := CreateLanguage(locale, index, func(r MessagesReport) {
		if reports != nil {
			*reports = append(*reports, r)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return lang
}

func mustT(t testing.TB, translator *Translator, key string, args ...any) string {
	t.Helper()
	text, err := translator.T(key, args...)
	if err != nil {
		t.Fatalf("t(%q): %v", key, err)
	}
	return text
}

func TestDefineMessagesRefusesANamespaceThatIsNoNameAndACatalogKeyThatIsNoLanguageTag(t *testing.T) {
	for _, c := range []struct {
		namespace string
		catalogs  []LocaleCatalog
		want      string
	}{
		{"Notes!", []LocaleCatalog{{"en", Catalog{"a": "A"}}}, "namespace"},
		{"notes", []LocaleCatalog{{"not a tag", Catalog{"a": "A"}}}, "no language tag"},
		{"notes", nil, "needs a catalog"},
	} {
		_, err := DefineMessages(c.namespace, c.catalogs...)
		if err == nil || !contains(err.Error(), c.want) {
			t.Errorf("DefineMessages(%q) error = %v, want one naming %q", c.namespace, err, c.want)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDefineMessagesListsItsCatalogsAsContributionsByCanonicalLocale(t *testing.T) {
	regional := mustMessages(t, "regional", LocaleCatalog{"en", Catalog{"a": "A"}}, LocaleCatalog{"pt-br", Catalog{"a": "Á"}})
	var got []string
	for _, tr := range regional.Translations {
		got = append(got, tr.Namespace+" "+tr.Locale)
	}
	if want := []string{"regional en", "regional pt-BR"}; !reflect.DeepEqual(got, want) {
		t.Errorf("translations = %v, want %v", got, want)
	}
}

func TestRendersTheFirstCatalogOutsideALanguage(t *testing.T) {
	notes := notesMessages(t)
	if got, err := notes.T("count", 2.0); err != nil || got != "2 notes" {
		t.Errorf("t(count, 2) = %q, %v", got, err)
	}
	if got := mustT(t, notes.In(nil), "newNote"); got != "New note" {
		t.Errorf("newNote = %q", got)
	}
	if _, err := notes.T("svaed"); err == nil {
		t.Error("an unknown key rendered")
	}
}

func TestRendersThePluginsOwnCatalogOfTheLanguageValuesAndFunctions(t *testing.T) {
	de := notesMessages(t).In(languageOf(t, "de", nil))
	if got := mustT(t, de, "newNote"); got != "Neue Notiz" {
		t.Errorf("newNote = %q", got)
	}
	if got := mustT(t, de, "count", 1234.0); got != "1.234 Notizen" {
		t.Errorf("count = %q", got)
	}
}

func TestPrefersTheHostsContributedCatalogFallingBackKeyByKeyDownTheChainReported(t *testing.T) {
	var reports []MessagesReport
	brazil := languageOf(t, "pt-BR", &reports, Translation{Namespace: "notes", Locale: "pt-BR", Messages: Catalog{"newNote": "Nova anotação"}})
	tr := notesMessages(t).In(brazil)
	if got := mustT(t, tr, "newNote"); got != "Nova anotação" {
		t.Errorf("newNote = %q", got)
	}
	if got := mustT(t, tr, "empty"); got != "Sem notas" {
		t.Errorf("empty = %q", got)
	}
	want := []MessagesReport{{Kind: ReportFallback, Namespace: "notes", Locale: "pt-BR", Key: "empty", Used: "pt"}}
	if !reflect.DeepEqual(reports, want) {
		t.Errorf("reports = %+v, want %+v", reports, want)
	}
}

func TestTakesAKeyAPartialContributedCatalogLacksFromThePluginsOwnOfTheSameLocale(t *testing.T) {
	var reports []MessagesReport
	better := Translation{Namespace: "notes", Locale: "de", Messages: Catalog{"newNote": "Neue Notiz!"}, Override: true}
	tr := notesMessages(t).In(languageOf(t, "de", &reports, better))
	if got := []string{mustT(t, tr, "newNote"), mustT(t, tr, "empty")}; !reflect.DeepEqual(got, []string{"Neue Notiz!", "Noch keine Notizen"}) {
		t.Errorf("got %v", got)
	}
	if len(reports) != 0 {
		t.Errorf("reports = %+v", reports)
	}
}

func TestFallsBackToTheFirstCatalogWhenNoCatalogOfTheChainHasTheKey(t *testing.T) {
	var reports []MessagesReport
	if got := mustT(t, notesMessages(t).In(languageOf(t, "ja", &reports)), "newNote"); got != "New note" {
		t.Errorf("newNote = %q", got)
	}
	want := []MessagesReport{{Kind: ReportFallback, Namespace: "notes", Locale: "ja", Key: "newNote", Used: ""}}
	if !reflect.DeepEqual(reports, want) {
		t.Errorf("reports = %+v, want %+v", reports, want)
	}
}

func TestReportsAContributedMessageThatThrowsAndGoesOn(t *testing.T) {
	var reports []MessagesReport
	broken := errors.New("broken translation")
	messages := Catalog{"count": MessageFunc(func(*Language, ...any) (string, error) { return "", broken })}
	tr := notesMessages(t).In(languageOf(t, "de", &reports, Translation{Namespace: "notes", Locale: "de", Messages: messages, Override: true}))
	if got := mustT(t, tr, "count", 2.0); got != "2 Notizen" {
		t.Errorf("count = %q", got)
	}
	want := MessagesReport{Kind: ReportFailed, Namespace: "notes", Locale: "de", Key: "count", Plugin: "@test/fr", Error: broken}
	if len(reports) == 0 || !reflect.DeepEqual(reports[0], want) {
		t.Errorf("reports = %+v, want first %+v", reports, want)
	}
}

func TestRendersAPlainTInsideAMessageFunctionInTheLanguageUnderWay(t *testing.T) {
	notes := notesMessages(t)
	line := MessageFunc(func(lang *Language, args ...any) (string, error) {
		text, err := notes.In(lang).T("count", args...)
		return text + "!", err
	})
	outer := mustMessages(t, "outer", LocaleCatalog{"en", Catalog{"line": line}}, LocaleCatalog{"de", Catalog{"line": line}})
	if got := mustT(t, outer.In(languageOf(t, "de", nil)), "line", 2.0); got != "2 Notizen!" {
		t.Errorf("de line = %q", got)
	}
	if got, _ := outer.T("line", 2.0); got != "2 notes!" {
		t.Errorf("plain line = %q", got)
	}
}

func TestBuildsDataAndRendersItInTheViewersLanguage(t *testing.T) {
	notes := notesMessages(t)
	saved := notes.Message("saved", "Plan", "2026-11-03T10:00:00Z")
	want := MessageData{Namespace: "notes", Key: "saved", Params: []any{"Plan", "2026-11-03T10:00:00Z"}}
	if !reflect.DeepEqual(saved, want) {
		t.Errorf("message = %+v", saved)
	}
	saved = notes.Message("saved", "Plan", "10:00")
	if got := notes.In(languageOf(t, "de", nil)).Render(saved); got != "„Plan“ um 10:00 gespeichert" {
		t.Errorf("de = %q", got)
	}
	if got := notes.Render(saved); got != `Saved "Plan" at 10:00` {
		t.Errorf("plain = %q", got)
	}
}

func TestShowsTheKeyOfAForeignOrUnknownMessageFromALinkReported(t *testing.T) {
	var reports []MessagesReport
	tr := notesMessages(t).In(languageOf(t, "de", &reports))
	if got := tr.Render(MessageData{Namespace: "other", Key: "hello", Params: []any{}}); got != "hello" {
		t.Errorf("foreign = %q", got)
	}
	if got := tr.Render(MessageData{Namespace: "notes", Key: "renamed", Params: []any{}}); got != "renamed" {
		t.Errorf("unknown = %q", got)
	}
	want := []MessagesReport{
		{Kind: ReportUnknown, Namespace: "other", Key: "hello", Expected: "notes"},
		{Kind: ReportUnknown, Namespace: "notes", Key: "renamed", Expected: "notes"},
	}
	if !reflect.DeepEqual(reports, want) {
		t.Errorf("reports = %+v, want %+v", reports, want)
	}
}

func TestShowsTheKeyOfAMessageWhoseParamsDoNotFitReported(t *testing.T) {
	var reports []MessagesReport
	got := notesMessages(t).In(languageOf(t, "de", &reports)).Render(MessageData{Namespace: "notes", Key: "when", Params: []any{"not a date"}})
	if got != "when" {
		t.Errorf("render = %q", got)
	}
	if len(reports) == 0 || reports[0].Kind != ReportFailed || reports[0].Namespace != "notes" || reports[0].Key != "when" || reports[0].Plugin != "" {
		t.Errorf("reports = %+v", reports)
	}
}

// Pi always sends params: [] (`message(key, ...params)`): a message with no arguments renders and is no unknown.
func TestAMessageWithNoArgumentsHasEmptyParamsAndRenders(t *testing.T) {
	notes := notesEnDe(t)
	message := notes.Message("empty")
	if message.Params == nil || len(message.Params) != 0 {
		t.Fatalf("params = %#v, want an empty non-nil slice", message.Params)
	}
	if got := notes.Render(message); got != "No notes" {
		t.Fatalf("render = %q, want %q", got, "No notes")
	}
}

// Intention of language.test.ts "the render scope › is current only while a render runs, and a nested render restores the
// outer" (Pi's inLanguage/currentLanguage, which Go has no form of: D450): a render in one language that renders another
// message in a second language leaves the first render in its own language afterwards, whether the nested render
// returns, fails or panics, and nothing of either outlives them. Go's scope is the explicit `lang` argument, so the guard
// is that nesting `other.In(fr)` inside a message function never changes what the outer function's own `lang` formats.
func TestANestedRenderInAnotherLanguageLeavesTheOuterRenderInItsOwnEvenWhenTheNestedOneFailsOrPanics(t *testing.T) {
	var reports []MessagesReport
	de, fr := languageOf(t, "de", &reports), languageOf(t, "fr", &reports)
	inner := mustMessages(t, "inner",
		LocaleCatalog{"en", Catalog{
			"number": MessageFunc(func(lang *Language, args ...any) (string, error) { return FormatNumber(lang, args[0].(float64), nil) }),
			"throws": MessageFunc(func(*Language, ...any) (string, error) { return "", errors.New("render failed") }),
			"panics": MessageFunc(func(*Language, ...any) (string, error) { panic("render panicked") }),
		}})
	var nested [3]string
	var nestedErrors [2]error
	outer := mustMessages(t, "outer",
		LocaleCatalog{"en", Catalog{
			"both": MessageFunc(func(lang *Language, args ...any) (string, error) {
				var err error
				if nested[0], err = FormatNumber(lang, 1234.5, nil); err != nil {
					return "", err
				}
				if nested[1], err = inner.In(fr).T("number", 1234.5); err != nil {
					return "", err
				}
				_, nestedErrors[0] = inner.In(fr).T("throws")
				_, nestedErrors[1] = inner.In(fr).T("panics")
				if nested[2], err = FormatNumber(lang, 1234.5, nil); err != nil {
					return "", err
				}
				return nested[0] + "|" + nested[1] + "|" + nested[2], nil
			}),
			"after": MessageFunc(func(lang *Language, args ...any) (string, error) { return FormatNumber(lang, 1234.5, nil) }),
		}})
	inDe, err := FormatNumber(de, 1234.5, nil)
	if err != nil {
		t.Fatal(err)
	}
	inFr, err := FormatNumber(fr, 1234.5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inDe == inFr {
		t.Fatalf("de and fr format 1234.5 alike (%q): the test cannot tell the languages apart", inDe)
	}
	got, err := outer.In(de).T("both")
	if err != nil {
		t.Fatal(err)
	}
	if want := inDe + "|" + inFr + "|" + inDe; got != want {
		t.Fatalf("outer render = %q, want %q: the outer language before, the nested one inside, the outer one restored", got, want)
	}
	if nestedErrors[0] == nil || nestedErrors[1] == nil {
		t.Fatalf("nested errors = %v, want the failure and the panic as errors", nestedErrors)
	}
	// Nothing of the nested renders outlives them: a render with no language is the source language again.
	if text, err := outer.In(nil).T("after"); err != nil || text == inDe || text == inFr {
		t.Fatalf("a render in no language = %q, %v, want the root locale's own formatting", text, err)
	}
	if text, err := outer.In(de).T("after"); err != nil || text != inDe {
		t.Fatalf("a later render in de = %q, %v, want %q", text, err, inDe)
	}
}
