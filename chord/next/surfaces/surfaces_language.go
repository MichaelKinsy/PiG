package surfaces

import (
	"slices"
	"strings"
)

// Ports packages/chord/src/surfaces/language.ts.

// Language is a locale to render in: its direction, its fallback chain, the host's catalogs and the reports'
// destination.
type Language struct {
	// Locale is a canonical language tag (`de`, `pt-BR`).
	Locale string
	// Dir is "ltr" or "rtl".
	Dir string
	// Chain is the locales tried in order before a namespace's first catalog: `pt-BR`, `pt`.
	Chain        []string
	Translations *TranslationIndex
	Report       func(MessagesReport)
}

// ReportKind tells what a MessagesReport reports.
type ReportKind string

const (
	// ReportFallback: a key found only further down the chain.
	ReportFallback ReportKind = "fallback"
	// ReportFailed: a contributed message that threw (or a message from a link that does not fit).
	ReportFailed ReportKind = "failed"
	// ReportUnknown: `Render` got a message of another namespace or a key the catalogs do not have; it shows the key.
	ReportUnknown ReportKind = "unknown"
)

// MessagesReport is what rendering reports. Upstream's three union members share this struct; the fields each Kind
// carries are the ones its member declares. Go has no `undefined`: Used and Plugin are "" for it (a locale and a plugin
// name are never empty).
type MessagesReport struct {
	Kind      ReportKind
	Namespace string
	// Locale is the language's locale (fallback) or the locale whose catalog failed (failed); empty for unknown.
	Locale string
	Key    string
	// Used is the locale whose catalog had the key (fallback); "" is the namespace's first catalog.
	Used string
	// Plugin is the contributing plugin (failed); "" is the namespace's own catalogs.
	Plugin string
	// Error is the error a contributed message threw (failed).
	Error error
	// Expected is the namespace the render expected (unknown).
	Expected string
}

// CreateLanguage is the language `locale` (canonical) with the host's translations; report gets every report as it
// happens. It returns the error `new Intl.Locale(locale)` throws for a tag that is none.
func CreateLanguage(locale string, translations *TranslationIndex, report func(MessagesReport)) (*Language, error) {
	dir, err := directionOf(locale)
	if err != nil {
		return nil, err
	}
	return &Language{Locale: locale, Dir: dir, Chain: chainOf(locale), Translations: translations, Report: report}, nil
}

// ResolveLocale is the locale to use: the user's pick when a catalog offers it (or its base), else the first of the
// device's preferred some catalog offers, else English (the source of truth) when offered, else the first offered, else
// `en`. A nil pick is upstream's `null`.
func ResolveLocale(pick *string, preferred, offered []string) string {
	if pick != nil {
		if locale, ok := MatchLocale([]string{*pick}, offered); ok {
			return locale
		}
	}
	if locale, ok := MatchLocale(preferred, offered); ok {
		return locale
	}
	if slices.Contains(offered, "en") {
		return "en"
	}
	if len(offered) > 0 {
		return offered[0]
	}
	return "en"
}

// MatchLocale is the first of preferred that offered (canonical tags) has: itself, one of its bases (`de-CH` for
// `de-CH-1996`, then `de`), or another of its language (`de-DE` for `de-AT`); false when none matches. Invalid tags are
// skipped (the device's list is the platform's data).
func MatchLocale(preferred, offered []string) (string, bool) {
	for _, wanted := range preferred {
		locale, ok := CanonicalLocale(wanted)
		if !ok {
			continue
		}
		for _, each := range chainOf(locale) {
			if slices.Contains(offered, each) {
				return each, true
			}
		}
		base := baseLanguage(locale)
		if i := slices.IndexFunc(offered, func(each string) bool { return baseLanguage(each) == base }); i >= 0 {
			return offered[i], true
		}
	}
	return "", false
}

// chainOf is locale and its bases, longest first: `zh-Hant-TW`, `zh-Hant`, `zh`.
func chainOf(locale string) []string {
	parts := strings.Split(locale, "-")
	chain := make([]string, len(parts))
	for i := range parts {
		chain[i] = strings.Join(parts[:len(parts)-i], "-")
	}
	return chain
}

// baseLanguage is the language subtag of a canonical tag (`pt` of `pt-BR`).
func baseLanguage(locale string) string {
	base, _, _ := strings.Cut(locale, "-")
	return base
}

// rtlScripts are the scripts written right to left (ISO 15924), as `Intl.Locale` maximizes a tag's script.
var rtlScripts = []string{"Adlm", "Arab", "Hebr", "Mand", "Nkoo", "Rohg", "Samr", "Syrc", "Thaa", "Yezi"}
