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
		return fmt.Sprintf("%s %s", FormatNumber(lang, n, nil), plural), nil
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

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }

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
