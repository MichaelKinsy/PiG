package surfaces

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Which locale's data a tag formats with: what `Intl.NumberFormat` / `Intl.DateTimeFormat` resolve it to in the pinned
// Node. ECMA-402's steps over generated tables (intl_gen.go, see automation/gen/generate-intl-tables.mjs): reject what is
// not a structurally valid tag, canonicalize (CLDR's language, script and region aliases), look the result up in the
// exceptions the generator measured (a region alias with several replacements picks one by the language), then
// BestAvailableLocale over the locales ICU has data for, else the platform's default locale.

// ErrLocale is wrapped by the errors of a locale `Intl` refuses (a RangeError there) or that FormatNumber and
// FormatTime do not support (a Unicode extension such as `-u-nu-arab`: D in rows/chord-ui.md).
var ErrLocale = errors.New("surfaces: unsupported locale")

type localePair struct{ from, to string }

type tagParts struct {
	lang, script, region string
	variants             []string
	// plain is the tag itself when it is already in canonical case with no extension: key() then needs no building.
	plain string
}

func isAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i] | 0x20; c < 'a' || c > 'z' {
			return false
		}
	}
	return s != ""
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func isAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i] | 0x20
		if (c < 'a' || c > 'z') && (s[i] < '0' || s[i] > '9') {
			return false
		}
	}
	return s != ""
}

// parseTag splits a structurally valid language tag (ECMA-402's IsStructurallyValidLanguageTag) into its parts, dropping
// private use (`-x-`) and the extensions `Intl` does not use; a Unicode extension (`-u-`) is ErrLocale.
func parseTag(tag string) (tagParts, error) {
	if parts, ok := parsePlain(tag); ok {
		return parts, nil
	}
	bad := func() (tagParts, error) {
		return tagParts{}, fmt.Errorf("%w: %q is not a structurally valid language tag", ErrLocale, tag)
	}
	var parts tagParts
	rest := tag
	next := func() (string, bool) {
		if rest == "" {
			return "", false
		}
		head, tail, found := strings.Cut(rest, "-")
		if found && tail == "" {
			head = "\x00" // a trailing separator
		}
		rest = tail
		return head, true
	}
	lang, _ := next()
	if n := len(lang); !isAlpha(lang) || n < 2 || n == 4 || n > 8 {
		return bad()
	}
	parts.lang = strings.ToLower(lang)
	normal := parts.lang == lang
	sub, ok := next()
	if ok && len(sub) == 4 && isAlpha(sub) {
		parts.script = titleCase(sub)
		normal = normal && parts.script == sub
		sub, ok = next()
	}
	if ok && (len(sub) == 2 && isAlpha(sub) || len(sub) == 3 && isDigits(sub)) {
		parts.region = strings.ToUpper(sub)
		normal = normal && parts.region == sub
		sub, ok = next()
	}
	for ok && (len(sub) >= 5 && len(sub) <= 8 && isAlnum(sub) || len(sub) == 4 && sub[0] >= '0' && sub[0] <= '9' && isAlnum(sub)) {
		v := strings.ToLower(sub)
		if slices.Contains(parts.variants, v) {
			return bad()
		}
		parts.variants = append(parts.variants, v)
		normal = normal && v == sub
		sub, ok = next()
	}
	var singletons []byte
	for ok {
		if len(sub) != 1 || !isAlnum(sub) {
			return bad()
		}
		single := sub[0] | 0x20
		if slices.Contains(singletons, single) {
			return bad()
		}
		singletons = append(singletons, single)
		var subtags []string
		for {
			sub, ok = next()
			if !ok || len(sub) == 1 && single != 'x' {
				break
			}
			if single == 'x' {
				if len(sub) > 8 || !isAlnum(sub) {
					return bad()
				}
			} else if len(sub) < 2 || len(sub) > 8 || !isAlnum(sub) {
				return bad()
			}
			subtags = append(subtags, strings.ToLower(sub))
		}
		if len(subtags) == 0 {
			return bad()
		}
		if single == 'u' {
			// `en-US-u-va-posix` is how the platform spells the variant `en-US-posix`
			if len(subtags) != 2 || subtags[0] != "va" || subtags[1] != "posix" {
				return tagParts{}, fmt.Errorf("%w: %q has a Unicode extension (numbering system, calendar, hour cycle), which FormatNumber and FormatTime do not honor", ErrLocale, tag)
			}
			if !slices.Contains(parts.variants, "posix") {
				parts.variants = append(parts.variants, "posix")
			}
		}
		if single == 'x' {
			break
		}
	}
	if normal && len(singletons) == 0 {
		parts.plain = tag
	}
	return parts, nil
}

// parsePlain is the common tag, `ll`, `lll`, `ll-RR`, `ll-Ssss` or `ll-Ssss-RR` in canonical case, split without
// allocating; anything else goes the long way.
func parsePlain(tag string) (tagParts, bool) {
	var parts tagParts
	end := strings.IndexByte(tag, '-')
	if end < 0 {
		end = len(tag)
	}
	if end < 2 || end > 3 || !isLower(tag[:end]) {
		return parts, false
	}
	parts.lang = tag[:end]
	rest := tag[end:]
	if len(rest) >= 5 && rest[0] == '-' && len(titleOrEmpty(rest[1:5])) == 4 && (len(rest) == 5 || rest[5] == '-') {
		parts.script = rest[1:5]
		rest = rest[5:]
	}
	if rest != "" {
		if rest[0] != '-' {
			return parts, false
		}
		region := rest[1:]
		if len(region) == 2 && isUpper(region) || len(region) == 3 && isDigits(region) {
			parts.region = region
		} else {
			return parts, false
		}
	}
	parts.plain = tag
	return parts, true
}

func isLower(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return s != ""
}

func isUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return s != ""
}

// titleOrEmpty is s when it is a script subtag in canonical case, else "".
func titleOrEmpty(s string) string {
	if len(s) == 4 && s[0] >= 'A' && s[0] <= 'Z' && isLower(s[1:]) {
		return s
	}
	return ""
}

// titleCase is s in the case of a script subtag (`Latn`), without allocating when it already is.
func titleCase(s string) string {
	ok := s[0] >= 'A' && s[0] <= 'Z'
	for i := 1; i < len(s) && ok; i++ {
		ok = s[i] >= 'a' && s[i] <= 'z'
	}
	if ok {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func (p tagParts) key() string {
	if p.plain != "" {
		return p.plain
	}
	var b strings.Builder
	b.WriteString(p.lang)
	for _, s := range [...]string{p.script, p.region} {
		if s != "" {
			b.WriteByte('-')
			b.WriteString(s)
		}
	}
	for _, v := range p.variants {
		b.WriteByte('-')
		b.WriteString(v)
	}
	return b.String()
}

func lookupPair(table []localePair, from string) (string, bool) {
	i, found := slices.BinarySearchFunc(table, from, func(e localePair, t string) int { return strings.Compare(e.from, t) })
	if !found {
		return "", false
	}
	return table[i].to, true
}

// aliased applies CLDR's language, script and region aliases (a replacement fills the subtags the tag lacks).
func (p tagParts) aliased() tagParts {
	if to, ok := lookupPair(intlLanguageAliases[:], p.lang); ok {
		p.plain = ""
		replacement, _ := parseTag(to)
		p.lang = replacement.lang
		if p.script == "" {
			p.script = replacement.script
		}
		if p.region == "" {
			p.region = replacement.region
		}
	}
	if to, ok := lookupPair(intlScriptAliases[:], p.script); ok {
		p.plain = ""
		p.script = to
	}
	if to, ok := lookupPair(intlRegionAliases[:], p.region); ok {
		p.plain = ""
		p.region = to
	}
	return p
}

// bestAvailable is ECMA-402's BestAvailableLocale: the longest prefix of tag, dropping subtags from the end, that has data.
func bestAvailable(available []string, tag string) (int, bool) {
	for {
		if i, found := slices.BinarySearch(available, tag); found {
			return i, true
		}
		pos := strings.LastIndexByte(tag, '-')
		if pos < 0 {
			return 0, false
		}
		if pos >= 2 && tag[pos-2] == '-' {
			pos -= 2
		}
		tag = tag[:pos]
	}
}

// resolveLocale is the index into available of the locale tag formats with, tag being absent (nil Language: the
// platform's default locale; an unavailable tag falls back to it as `Intl` does).
func resolveLocale(tag string, available []string, exceptions []localePair) (int, error) {
	parts, err := parseTag(tag)
	if err != nil {
		return 0, err
	}
	if i, ok := resolveParts(parts, available, exceptions); ok {
		return i, nil
	}
	if platform := platformLocale(); platform != tag {
		if parts, err := parseTag(platform); err == nil {
			if i, ok := resolveParts(parts, available, exceptions); ok {
				return i, nil
			}
		}
	}
	i, _ := slices.BinarySearch(available, "en-US")
	return i, nil
}

func resolveParts(parts tagParts, available []string, exceptions []localePair) (int, bool) {
	if to, ok := lookupPair(exceptions, parts.key()); ok {
		return slices.BinarySearch(available, to)
	}
	return bestAvailable(available, parts.aliased().key())
}

// platformLocale is the platform's default locale (`Intl`'s): the first of LC_ALL, LC_MESSAGES, LANG that is a tag,
// else en-US.
func platformLocale() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value, _, _ := strings.Cut(os.Getenv(name), ".")
		value, _, _ = strings.Cut(value, "@")
		value = strings.ReplaceAll(value, "_", "-")
		if _, err := parseTag(value); err == nil {
			return value
		}
	}
	return "en-US"
}
