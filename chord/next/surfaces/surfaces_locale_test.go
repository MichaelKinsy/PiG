package surfaces

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

// pi: packages/chord/src/surfaces/translations.ts (canonicalLocale) and language.ts (directionOf), against the Intl of the pinned Node.

type intlAnswer struct {
	Canon *string `json:"canon"`
	Dir   *string `json:"dir"`
}

// askIntl is what Node's Intl says of each tag.
func askIntl(t *testing.T, tags []string) []intlAnswer {
	t.Helper()
	input, err := json.Marshal(tags)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/locale_oracle.mjs")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Intl oracle: %v\n%s", err, &stderr)
	}
	var answers []intlAnswer
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	if len(answers) != len(tags) {
		t.Fatalf("Intl oracle answered %d of %d tags", len(answers), len(tags))
	}
	return answers
}

func localeCorpus() []string {
	tags := []string{
		"pt-br", "iw", "ac", "aa-AB", "en-XX", "eng", "alb", "zh-hant-tw", "en-u-ca-gregory", "de-DE-u-co-phonebk", "i-klingon", "en-US-POSIX",
		"und", "EN", "es-419", "sr-Latn-RS", "in", "mo", "art-lojban", "cmn", "zh-cmn-Hans", "en-GB-oed", "ji", "sh", "no", "tl", "zh-min-nan",
		"de-1996", "en-x-foo", "x-private", "", "en_US", "fil", "qaa", "en-Latn-US", "ar-SA-u-nu-latn", "ja-JP-u-ca-japanese",
		"ar", "he", "fa", "ur", "ps", "sd", "ckb", "yi", "dv", "ug", "az-Arab", "az-IR", "pa-PK", "ks", "syr", "ku-Arab", "mzn", "uz-Arab",
		"ms-Arab", "ha-Arab", "rhg", "nqo", "ff-Adlm", "sd-Deva", "pa-Arab", "ku", "lrc", "glk", "bal", "arc", "sam", "ar-EG", "ru-SU", "uz-SU",
		"en-US-u-kn-true", "en-u-kn-true-ca-gregory", "en-t-fr-ca", "en-t-m0-name", "en-a-bbb-u-ca-x", "en-a", "en-u", "en-x", "en-x-", "en-",
		"-en", "en--US", "EN-latn-us", "de-de-1996", "de-1996-1996", "abcd", "abcde", "abcdefgh", "abcdefghi", "en-u-ca", "en-U-CA-GREGORY",
		"en-u-attr-ca-gregory", "en-u-ca-gregory-ca-buddhist", "sr-Cyrl-CS", "hy-arevela", "ja-Latn-hepburn-heploc", "zh-guoyu", "cel-gaulish",
		"en-US-u-va-posix", "en-polytoni", "el-polytoni", "ru-SU-u-co-x", "en-ABCDE", "en-1234", "en-123", "en-12", "en-a1b2c", "und-Latn",
		"und-US", "x-i-foo", "i-ami", "sgn-BE-FR", "en-GB-x-twain", "en-US-x-a", "en-x-a-b", "en-x-a-bcdefghij", "en-t-en-latn-us-t0-abc",
	}
	for _, a := range "abcdefghijklmnopqrstuvwxyz" {
		for _, b := range "abcdefghijklmnopqrstuvwxyz" {
			tags = append(tags, string(a)+string(b))
			for _, c := range "abcdefghijklmnopqrstuvwxyz" {
				tags = append(tags, string(a)+string(b)+string(c))
			}
		}
	}
	for _, language := range []string{"en", "ru", "uz", "sr", "zh", "de", "fr", "sh"} {
		for _, a := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
			for _, b := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
				tags = append(tags, language+"-"+string(a)+string(b))
			}
		}
		for i := range 1000 {
			tags = append(tags, fmt.Sprintf("%s-%03d", language, i))
		}
	}
	tags = append(tags, aliasCorpus()...)
	// Random well-formed and malformed tags, seeded.
	random := rand.New(rand.NewSource(7))
	// Languages first, then subtags.
	languages := []string{"en", "de", "zh", "sr", "ar", "ja", "fr", "abc", "ab", "und", "nu", "uz", "ru", "tw", "TW", "bh", "Bh", "tnf"}
	pieces := []string{"en", "de", "Latn", "Hant", "Arab", "US", "DE", "GB", "419", "1996", "rozaj", "posix", "u", "t", "x", "a", "ca", "co", "nu",
		"gregory", "phonebk", "latn", "fr", "abc", "ab", "k0", "m0", "und", "hepburn", "heploc", "aaland", "polytoni", "xyzzy12345",
		"true", "yes", "names", "islamicc", "hongkong", "tz", "x"}
	for range 3000 {
		n := 1 + random.Intn(6)
		parts := make([]string, n)
		parts[0] = languages[random.Intn(len(languages))]
		for i := 1; i < n; i++ {
			parts[i] = pieces[random.Intn(len(pieces))]
		}
		tags = append(tags, strings.Join(parts, "-"))
	}
	return tags
}

// aliasCorpus is the tags of the alias rules Intl.getCanonicalLocales has beyond a bare language, a region and a variant (listed from the
// data the generator probes Node for): the value aliases of `-u-` keywords and `-t-` fields, every three-letter value of the keywords that have
// aliases, the aliases that need more than the language and the region (`tw-AA`, `sgn-BR`, `Qaai`), and every language with a few regions.
func aliasCorpus() []string {
	tags := []string{
		"en-u-ca-islamicc", "en-u-ca-ethiopic-amete-alem", "en-u-ca-gregory", "en-u-ca-islamic-civil", "en-u-ks-primary", "en-u-ks-tertiary", "en-u-ks-level2",
		"en-u-ms-imperial", "en-u-ms-uksystem", "en-u-tz-cnckg", "en-u-tz-eire", "en-u-tz-utc", "en-u-tz-cet", "en-u-tz-uct", "en-u-sd-no23", "en-u-rg-no23",
		"en-u-sd-fra", "en-u-sd-frl", "en-u-rg-frg", "en-u-kb-yes", "en-u-kn-yes", "en-u-kb-no", "en-u-kb-true", "en-u-kb-false", "en-u-kf-yes", "en-u-ca-yes",
		"en-u-nu-traditio", "en-u-co-big5han", "en-u-co-phonebk", "en-u-hc-h12", "en-u-ca-islamicc-nu-latn", "und-u-ca-islamicc", "de-DE-u-co-phonebk-ca-islamicc",
		"en-u-true", "en-u-yes", "en-u-true-yes", "en-u-attr-ca-islamicc", "en-u-ca-islamicc-ca-gregory", "en-u-ca-ethiopic-amete-alem-nu-latn", "en-u-ca-ethiopic-amete", "en-u-ca-ethiopic", "iw-u-ca-islamicc",
		"he-t-he-u-ca-islamicc", "en-t-m0-names", "en-t-m0-prprname", "en-t-m0-ungegn", "en-t-m0-bgn", "en-t-fr-m0-names-d0-lower", "en-t-fr-latn-m0-names",
		"und-Qaai", "en-qaai", "en-Qaai-US", "sh-Qaai", "iw-qaai-IL", "und-Zinh",
		"tw", "tw-AA", "tw-US", "tw-Latn", "tw-u-ca-gregory", "tw-x-a", "tw-1996", "tw-US-u-nu-latn", "tnf-AF", "swc-CD", "prs-AF", "drw-AF", "cnr-ME", "cnr-Latn", "bh-IN",
		"sgn-BR", "sgn-br", "sgn-BR-u-ca-gregory", "sgn-Latn-BR", "sgn-US", "sgn-840", "sgn-BE-FR", "sgn-BE-NL", "sgn-CH-DE", "sgn-DD", "sgn-FX", "sgn-BR-x-a", "sgn-BR-1996",
	}
	// Every alias the tables have (the generator found them in Node), alone and among other subtags and extensions.
	for pair := range unicodeValueAliases {
		tags = append(tags, "en-u-"+pair, "de-DE-u-co-phonebk-"+pair, "und-u-attr-"+pair+"-nu-latn", "fr-u-"+pair+"-x-priv")
	}
	for pair := range transformedValueAliases {
		tags = append(tags, "en-t-"+pair, "en-t-fr-"+pair, "de-t-en-latn-"+pair+"-d0-lower", "en-t-"+pair+"-u-ca-gregory")
	}
	for language := range qualifiedLanguageAliases {
		tags = append(tags, language, language+"-AA", language+"-Latn", language+"-1996", language+"-x-a", language+"-u-ca-gregory", language+"-Latn-US-u-nu-latn")
	}
	for pair := range languageRegionRules {
		tags = append(tags, pair, pair+"-u-ca-gregory", pair+"-x-a", pair+"-1996", strings.Replace(pair, "-", "-Latn-", 1))
	}
	for script := range scriptAliases {
		tags = append(tags, "und-"+script, "en-"+script+"-US", "sh-"+script, "en-"+strings.ToLower(script)+"-1996")
	}
	// The review's classes: time zone aliases of six letters and more, the qualified languages in capitals and as a `-t-` source, a lone `true`
	// in a private use.
	for _, word := range []string{"factory", "hongkong", "iceland", "israel", "jamaica", "mxstis", "navajo", "poland", "portugal", "turkey", "usnavajo", "japan", "singapore", "egypt", "cuba", "eire", "gb", "gmt", "libya", "nz", "prc", "roc", "rok", "uct", "utc", "zulu"} {
		tags = append(tags, "en-u-tz-"+word, "de-u-ca-gregory-tz-"+word, "en-u-tz-"+word+"-nu-latn")
	}
	for _, language := range []string{"tw", "tnf", "swc", "prs", "drw", "cnr", "bh"} {
		cases := []string{language, strings.ToUpper(language), strings.ToUpper(language[:1]) + language[1:], language[:1] + strings.ToUpper(language[1:])}
		for _, each := range cases {
			tags = append(tags, each, each+"-AA", each+"-Latn", each+"-Latn-US", each+"-1996", each+"-u-ca-gregory", each+"-x-a", "en-t-"+each, "en-t-"+each+"-latn", "en-t-"+each+"-AA", "en-t-"+each+"-m0-names")
		}
	}
	tags = append(tags, "en-x-true", "en-X-TRUE", "en-u-ca-gregory-x-true", "en-t-ja-x-true", "en-x-true-a", "en-x-a-true", "x-true", "en-t-ja-x-true-a", "en-u-x-true", "en-a-bbb-x-true")
	keys := []string{"ca", "co", "ks", "ms", "nu", "tz", "rg", "sd", "kb", "kn", "kc", "kf", "kh", "kk", "kr", "kv", "hc", "fw", "lb", "lw", "mu", "ss", "va", "cu", "em", "dx"}
	for _, key := range keys {
		for _, a := range "abcdefghijklmnopqrstuvwxyz" {
			for _, b := range "abcdefghijklmnopqrstuvwxyz" {
				for _, c := range "abcdefghijklmnopqrstuvwxyz" {
					tags = append(tags, "en-u-"+key+"-"+string(a)+string(b)+string(c))
				}
			}
		}
	}
	// Every language with a region that is deprecated, one that is not, a script and a variant.
	for _, a := range "abcdefghijklmnopqrstuvwxyz" {
		for _, b := range "abcdefghijklmnopqrstuvwxyz" {
			for _, c := range "abcdefghijklmnopqrstuvwxyz" {
				language := string(a) + string(b) + string(c)
				tags = append(tags, language+"-AA", language+"-BR", language+"-SU", language+"-Latn", language+"-u-ca-gregory")
				if language[2] == 'a' {
					tags = append(tags, language[:2]+"-AA", language[:2]+"-BR", language[:2]+"-SU", language[:2]+"-Latn", language[:2]+"-u-ca-gregory")
				}
			}
		}
	}
	return tags
}

func TestLocaleTablesMatchIntl(t *testing.T) {
	tags := localeCorpus()
	answers := askIntl(t, tags)
	failures := 0
	bad := func(format string, args ...any) {
		t.Helper()
		t.Errorf(format, args...)
		if failures++; failures >= 60 {
			t.Fatal("too many differences")
		}
	}
	for i, tag := range tags {
		canon, ok := CanonicalLocale(tag)
		want := answers[i]
		switch {
		case want.Canon == nil && ok:
			bad("CanonicalLocale(%q) = %q, Intl refuses it", tag, canon)
		case want.Canon != nil && !ok:
			bad("CanonicalLocale(%q) refuses it, Intl says %q", tag, *want.Canon)
		case want.Canon != nil && canon != *want.Canon:
			bad("CanonicalLocale(%q) = %q, Intl says %q", tag, canon, *want.Canon)
		}
		if want.Dir != nil {
			dir, err := directionOf(tag)
			if err != nil || dir != *want.Dir {
				bad("directionOf(%q) = %q (%v), Intl says %q", tag, dir, err, *want.Dir)
			}
		}
	}
}
