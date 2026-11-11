package surfaces

import (
	"slices"
	"strings"
)

// Locale tags as Node's Intl treats them (Pi's canonicalLocale is `Intl.getCanonicalLocales(locale)[0]`, its direction `new
// Intl.Locale(locale).maximize().script`). Go has no ICU: the well-formedness grammar (ECMA-402 IsStructurallyValidLanguageTag: the
// `unicode_locale_id` of UTS 35, which refuses `en_US`, `i-klingon`, `zh-min-nan`, a four letter language) is code, and CLDR's alias and
// likely-script data is the tables of surfaces_locale_tables.go, generated from the pinned Node by testdata/locale_tables.mjs and checked
// against it by TestLocaleTablesMatchIntl: the aliases of a language, of a region, of a variant and of a script, those of a language that
// apply only with another subtag or not in lower case (`tw-AA`, `TW`, `en-t-tw`), those of a language and a region together (`sgn-BR`),
// and the value aliases of `-u-` keywords and `-t-` fields (`ca-islamicc`, `tz-hongkong`). Asked of Node when the tables are generated:
// every `-u-` value of three letters or digits (every key), of four for the keys that have aliases, of five letters for time zones and
// subdivisions, the words of CLDR's lists and of the zones' names, and every six letters that begin with a region for a time zone; a
// `-u-` or `-t-` value of seven or eight letters that is in none of those lists is not asked, so an alias of one is not in the tables.
// Equality with Intl is proved over the corpus of the test, which includes every alias the tables have and the classes above.

//go:generate sh -c "node testdata/locale_tables.mjs | gofmt > surfaces_locale_tables.go"

// localeTag is a structurally valid tag taken apart.
type localeTag struct {
	language, script, region string
	// nonLowercase is whether the language was not written all in lower case.
	nonLowercase bool
	variants     []string
	// extensions are the singleton extensions in canonical order (u and t sorted inside), the private use last.
	extensions []string
}

func isAlphanumeric(text string) bool {
	return text != "" && !strings.ContainsFunc(text, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
}

// isVariantSubtag is whether text is a variant: five to eight alphanumerics, or a digit and three more.
func isVariantSubtag(text string) bool {
	return isAlphanumeric(text) && (between(text, 5, 8) || len(text) == 4 && text[0] >= '0' && text[0] <= '9')
}

func between(text string, low, high int) bool { return len(text) >= low && len(text) <= high }

// parseLocaleTag is tag's parts, lower-cased except the script (`Latn`) and the region (`US`); false when tag is not a structurally
// valid language tag.
func parseLocaleTag(tag string) (localeTag, bool) {
	var parsed localeTag
	parts := strings.Split(tag, "-")
	language := parts[0]
	if !isAlpha(language) || len(language) == 4 || !between(language, 2, 8) {
		return parsed, false
	}
	parsed.language = strings.ToLower(language)
	parsed.nonLowercase = language != parsed.language
	index := 1
	if index < len(parts) && len(parts[index]) == 4 && isAlpha(parts[index]) {
		script := strings.ToLower(parts[index])
		parsed.script = strings.ToUpper(script[:1]) + script[1:]
		index++
	}
	if index < len(parts) && (len(parts[index]) == 2 && isAlpha(parts[index]) || len(parts[index]) == 3 && isDigits(parts[index])) {
		parsed.region = strings.ToUpper(parts[index])
		index++
	}
	for ; index < len(parts); index++ {
		variant := strings.ToLower(parts[index])
		if !isVariantSubtag(variant) {
			break
		}
		if slices.Contains(parsed.variants, variant) {
			return parsed, false
		}
		parsed.variants = append(parsed.variants, variant)
	}
	slices.Sort(parsed.variants)
	extensions := map[byte][]string{}
	var private []string
	for index < len(parts) {
		singleton := strings.ToLower(parts[index])
		if len(singleton) != 1 || !isAlphanumeric(singleton) {
			return parsed, false
		}
		start := index + 1
		end := start
		for end < len(parts) && len(parts[end]) > 1 {
			end++
		}
		subtags := make([]string, 0, end-start)
		for _, subtag := range parts[start:end] {
			subtags = append(subtags, strings.ToLower(subtag))
		}
		key := singleton[0]
		if _, dup := extensions[key]; dup || (key == 'x' && private != nil) {
			return parsed, false
		}
		switch key {
		case 'x':
			// The private use takes every subtag to the end, one character long ones too.
			subtags = subtags[:0]
			for _, subtag := range parts[start:] {
				subtags = append(subtags, strings.ToLower(subtag))
			}
			end = len(parts)
			if len(subtags) == 0 {
				return parsed, false
			}
			for _, subtag := range subtags {
				if !between(subtag, 1, 8) || !isAlphanumeric(subtag) {
					return parsed, false
				}
			}
			private = subtags
		case 'u':
			canonical, ok := canonicalUnicodeExtension(subtags)
			if !ok {
				return parsed, false
			}
			extensions[key] = canonical
		case 't':
			canonical, ok := canonicalTransformedExtension(subtags)
			if !ok {
				return parsed, false
			}
			extensions[key] = canonical
		default:
			if len(subtags) == 0 {
				return parsed, false
			}
			for _, subtag := range subtags {
				if !between(subtag, 2, 8) || !isAlphanumeric(subtag) {
					return parsed, false
				}
			}
			extensions[key] = subtags
		}
		index = end
	}
	keys := make([]byte, 0, len(extensions))
	for key := range extensions {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		parsed.extensions = append(parsed.extensions, singletonText(string(key), extensions[key]))
	}
	if private != nil {
		parsed.extensions = append(parsed.extensions, singletonText("x", private))
	}
	return parsed, true
}

// singletonText is the extension singleton with its subtags; one that is only `true` or `yes` is the bare singleton (`en-x-true` is
// `en-x`, `uz-a-yes` is `uz-a`), the one value of a `-u-` keyword that Intl drops wherever a lone one is. The `u` singleton keeps
// them (`en-u-true` is an attribute, as it is in Intl): canonicalUnicodeExtension drops the `true` of a keyword.
func singletonText(singleton string, subtags []string) string {
	if len(subtags) == 0 || singleton != "u" && len(subtags) == 1 && (subtags[0] == "true" || subtags[0] == yesSubtag) {
		return singleton
	}
	return singleton + "-" + strings.Join(subtags, "-")
}

// yesSubtag is the value of a `-u-` keyword that is `true` for every key (`kb-yes` is `kb`; testdata/locale_tables.mjs asks Intl of every key).
const yesSubtag = "yes"

// aliasedValues is the canonical form of the keyword or field key with values (subtags): the table's value for key and values joined by `-`
// (the table's are lower case, a value of several subtags too), else values.
func aliasedValues(table map[string]string, key string, values []string) []string {
	if replacement, ok := table[key+"-"+strings.Join(values, "-")]; ok {
		return strings.Split(replacement, "-")
	}
	return values
}

// canonicalUnicodeExtension is the subtags of a `-u-` extension (lower case): attributes, then keywords by key, a `true` value
// dropped; false when they are no attributes and keywords.
func canonicalUnicodeExtension(subtags []string) ([]string, bool) {
	var attributes []string
	index := 0
	for ; index < len(subtags) && between(subtags[index], 3, 8) && isAlphanumeric(subtags[index]); index++ {
		attributes = append(attributes, subtags[index])
	}
	type keyword struct {
		key    string
		values []string
	}
	var keywords []keyword
	for ; index < len(subtags); index++ {
		subtag := subtags[index]
		if len(subtag) == 2 && isAlphanumeric(subtag) && isAlpha(subtag[1:]) {
			keywords = append(keywords, keyword{key: subtag})
			continue
		}
		if len(keywords) == 0 || !between(subtag, 3, 8) || !isAlphanumeric(subtag) {
			return nil, false
		}
		last := &keywords[len(keywords)-1]
		last.values = append(last.values, subtag)
	}
	if len(attributes) == 0 && len(keywords) == 0 {
		return nil, false
	}
	slices.Sort(attributes)
	attributes = slices.Compact(attributes)
	slices.SortStableFunc(keywords, func(a, b keyword) int { return strings.Compare(a.key, b.key) })
	keywords = slices.CompactFunc(keywords, func(a, b keyword) bool { return a.key == b.key })
	out := attributes
	for _, each := range keywords {
		out = append(out, each.key)
		values := each.values
		if len(values) == 1 && values[0] == yesSubtag {
			values = []string{"true"}
		}
		values = aliasedValues(unicodeValueAliases, each.key, values)
		if len(values) == 1 && values[0] == "true" {
			continue
		}
		out = append(out, values...)
	}
	return out, true
}

// canonicalTransformedExtension is the subtags of a `-t-` extension (lower case): the source language, then the fields by key; false
// when they are none of these.
func canonicalTransformedExtension(subtags []string) ([]string, bool) {
	index := 0
	var out []string
	if index < len(subtags) && isAlpha(subtags[index]) && len(subtags[index]) != 4 && between(subtags[index], 2, 8) {
		language, ok := parseLocaleTag(strings.Join(takeLanguageID(subtags), "-"))
		if !ok {
			return nil, false
		}
		// The source language is aliased as a language that is not all lower case is.
		language.nonLowercase = true
		out = append(out, strings.ToLower(language.canonicalize().String()))
		index = len(takeLanguageID(subtags))
	}
	type field struct {
		key    string
		values []string
	}
	var fields []field
	for ; index < len(subtags); index++ {
		subtag := subtags[index]
		if len(subtag) == 2 && isAlpha(subtag[:1]) && isDigits(subtag[1:]) {
			fields = append(fields, field{key: subtag})
			continue
		}
		if len(fields) == 0 || !between(subtag, 3, 8) || !isAlphanumeric(subtag) {
			return nil, false
		}
		last := &fields[len(fields)-1]
		last.values = append(last.values, subtag)
	}
	if len(out) == 0 && len(fields) == 0 {
		return nil, false
	}
	for _, each := range fields {
		if len(each.values) == 0 {
			return nil, false
		}
	}
	slices.SortStableFunc(fields, func(a, b field) int { return strings.Compare(a.key, b.key) })
	for _, each := range fields {
		out = append(out, each.key)
		out = append(out, aliasedValues(transformedValueAliases, each.key, each.values)...)
	}
	return out, true
}

// takeLanguageID is the leading subtags of a `-t-` extension that make a language id: the language, a script, a region, variants.
func takeLanguageID(subtags []string) []string {
	n := 1
	if n < len(subtags) && len(subtags[n]) == 4 && isAlpha(subtags[n]) {
		n++
	}
	if n < len(subtags) && (len(subtags[n]) == 2 && isAlpha(subtags[n]) || len(subtags[n]) == 3 && isDigits(subtags[n])) {
		n++
	}
	for n < len(subtags) && isVariantSubtag(subtags[n]) {
		n++
	}
	return subtags[:n]
}

// String is the tag's canonical text, its aliases not applied.
func (tag localeTag) String() string {
	parts := []string{tag.language}
	if tag.script != "" {
		parts = append(parts, tag.script)
	}
	if tag.region != "" {
		parts = append(parts, tag.region)
	}
	parts = append(parts, tag.variants...)
	parts = append(parts, tag.extensions...)
	return strings.Join(parts, "-")
}

// likelyRegion is the region alias of region for the language: the table's entry for the pair, else the region's default.
func (tag localeTag) regionAlias() (string, bool) {
	if replacement, ok := languageRegionAliases[tag.language+"-"+tag.region]; ok {
		return replacement, true
	}
	replacement, ok := regionAliases[tag.region]
	return replacement, ok
}

// mergeLanguage is the tag with its language replaced by replacement, a language id: its script and region fill what the tag has none of.
func (tag *localeTag) mergeLanguage(replacement string) {
	parsed, ok := parseLocaleTag(replacement)
	if !ok {
		return
	}
	tag.language = parsed.language
	if tag.script == "" {
		tag.script = parsed.script
	}
	if tag.region == "" {
		tag.region = parsed.region
	}
}

// canonicalize applies CLDR's aliases to the tag.
func (tag localeTag) canonicalize() localeTag {
	// A language with one variant and nothing else that has an alias of its own (`art-lojban` is `jbo`).
	if len(tag.variants) == 1 && tag.script == "" && tag.region == "" {
		if replacement, ok := variantAliases[tag.language+"-"+tag.variants[0]]; ok && !strings.HasPrefix(replacement, tag.language+"-") {
			if parsed, ok := parseLocaleTag(replacement); ok {
				parsed.extensions = append(parsed.extensions, tag.extensions...)
				return parsed.sortedExtensions()
			}
		}
	}
	// A language and a region that are one language of their own, the region gone (`sgn-BR` is `bzs`).
	rules := languageRegionRules
	if tag.script != "" {
		rules = scriptedLanguageRegionRules
	}
	if replacement, ok := rules[tag.language+"-"+tag.region]; ok {
		tag.language, tag.region = replacement, ""
	}
	if replacement, ok := languageAliases[tag.language]; ok {
		tag.mergeLanguage(replacement)
	} else if replacement, ok := nonLowercaseLanguageAliases[tag.language]; ok && tag.nonLowercase {
		tag.mergeLanguage(replacement)
	} else if replacement, ok := qualifiedLanguageAliases[tag.language]; ok && (tag.script != "" || tag.region != "" || len(tag.variants) > 0 || len(tag.extensions) > 0) {
		// `tw` alone stays `tw`; with anything after it, it is `ak`.
		tag.language = replacement
	}
	if replacement, ok := scriptAliases[tag.script]; ok {
		tag.script = replacement
	}
	if tag.region != "" {
		if replacement, ok := tag.regionAlias(); ok {
			tag.region = replacement
		}
	}
	variants := tag.variants
	// `hepburn` with `heploc` is `alalc97`.
	if slices.Contains(variants, "hepburn") && slices.Contains(variants, "heploc") {
		variants = slices.DeleteFunc(slices.Clone(variants), func(variant string) bool { return variant == "hepburn" || variant == "heploc" })
		variants = append(variants, "alalc97")
	}
	var renamed, posix []string
	for _, variant := range variants {
		replacement, ok := variantAliases["en-"+variant]
		replacement = strings.TrimPrefix(replacement, "en-")
		switch {
		case !ok:
			renamed = append(renamed, variant)
		case replacement == "en":
			// A variant that is a language of its own (`bokmal`, `arevela`) goes: the tags that it changes are in variantAliases.
		case strings.HasPrefix(replacement, "u-"):
			posix = append(posix, replacement)
			renamed = append(renamed, variant)
		case isDigits(replacement) && len(replacement) == 3 || len(replacement) == 2 && isAlpha(replacement) && replacement == strings.ToUpper(replacement):
			// `aaland` is the region AX, and nothing where the tag has a region.
			if tag.region == "" {
				tag.region = replacement
			}
		default:
			renamed = append(renamed, replacement)
		}
	}
	variants = renamed
	// `posix` is `-u-va-posix` as the one variant the tag is left with.
	if len(variants) == 1 && len(posix) == 1 {
		tag.extensions = append(tag.extensions, posix[0])
		variants = nil
	}
	slices.Sort(variants)
	tag.variants = variants
	return tag.sortedExtensions()
}

// sortedExtensions merges the extensions of the same singleton and orders them (the private use last).
func (tag localeTag) sortedExtensions() localeTag {
	if len(tag.extensions) < 2 {
		return tag
	}
	merged := map[string][]string{}
	var private string
	for _, extension := range tag.extensions {
		if extension == "x" || strings.HasPrefix(extension, "x-") {
			private = extension
			continue
		}
		if len(extension) > 2 {
			merged[extension[:1]] = append(merged[extension[:1]], strings.Split(extension[2:], "-")...)
		} else if _, present := merged[extension[:1]]; !present {
			merged[extension[:1]] = nil
		}
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	tag.extensions = nil
	for _, key := range keys {
		subtags := merged[key]
		if key == "u" {
			if canonical, ok := canonicalUnicodeExtension(subtags); ok {
				subtags = canonical
			}
		}
		tag.extensions = append(tag.extensions, singletonText(key, subtags))
	}
	if private != "" {
		tag.extensions = append(tag.extensions, private)
	}
	return tag
}

// CanonicalLocale is locale canonical (`pt-br` is `pt-BR`, `iw` is `he`); false when it is no language tag
// (`Intl.getCanonicalLocales` throws: `en_US`, `i-klingon`, an empty text).
func CanonicalLocale(locale string) (string, bool) {
	tag, ok := parseLocaleTag(locale)
	if !ok {
		return "", false
	}
	return tag.canonicalize().String(), true
}

// directionOf is the text direction of locale: right to left when its likely script is (Arabic for `ar`, `ur`, `fa`).
func directionOf(locale string) (string, error) {
	parsed, ok := parseLocaleTag(locale)
	if !ok {
		return "", &localeError{locale}
	}
	tag := parsed.canonicalize()
	script := tag.script
	rtl := false
	switch {
	case script != "":
		rtl = slices.Contains(rtlScripts, script)
	default:
		_, rtl = rtlLanguages[tag.language]
		if tag.region != "" {
			pair := tag.language + "-" + tag.region
			if _, yes := rtlLanguageRegions[pair]; yes {
				rtl = true
			}
			if _, no := ltrLanguageRegions[pair]; no {
				rtl = false
			}
		}
	}
	if rtl {
		return "rtl", nil
	}
	return "ltr", nil
}

// localeError is the error of a text that is no language tag.
type localeError struct{ locale string }

func (e *localeError) Error() string { return "surfaces: " + e.locale + " is no language tag" }
