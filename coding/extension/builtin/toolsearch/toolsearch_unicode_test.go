package toolsearch_test

// pi: packages/coding-agent/src/extensions/tool-search/tool.ts

import (
	_ "embed"
	"reflect"
	"slices"
	"testing"
	"unicode"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// stripsDictionaries reports whether this process has no ICU word dictionaries (a pig_strip_word_dictionaries build, D92), where a dictionary script's run is one word and the Pi cases that split one
// give way to TestTokenizeKeepsDictionaryScriptRunsWholeWithoutDictionaries.
func stripsDictionaries() bool { return pigstrip.Has(pigstrip.ListFeatures, pigstrip.WordDictionaries) }

// Ports tool-search.test.ts "tokenize" (Pi 79006f06, #11037).
func TestTokenizeSegmentsUnicodeWords(t *testing.T) {
	for _, test := range []struct {
		text string
		want []string
	}{
		{"listIssues for the GitHub_repo", []string{"list", "issue", "git", "hub", "repo"}},
		{"searches queries HTTPServer", []string{"search", "query", "http", "server"}},
		{"repo.issues/search:queries", []string{"repo", "issue", "search", "query"}},
		{"搜索仓库问题", []string{"搜索", "仓库", "问题"}},
		{"文書を検索", []string{"文書", "を", "検索"}},
		{"문서 검색", []string{"문서", "검색"}},
		{"ค้นหาเอกสาร", []string{"ค้นหา", "เอกสาร"}},
		{"Поиск документов", []string{"поиск", "документов"}},
		{"البحث عن الملفات", []string{"البحث", "عن", "الملفات"}},
		{"फ़ाइल खोजें", []string{"फ़ाइल", "खोजें"}},
		{"Éléments cafés", []string{"éléments", "cafés"}},
		{"搜索GitHub_repoIssues résumé cafés", []string{"搜索", "git", "hub", "repo", "issue", "résumé", "cafés"}},
		// Canonically equivalent text is one term.
		{"CAFÉ cafe\u0301", []string{"café", "café"}},
		{" \t，。!? 😀 ", []string{}},
	} {
		if stripsDictionaries() && usesDictionary(test.text) {
			continue
		}
		if got := toolsearch.Tokenize(test.text); !reflect.DeepEqual(got, test.want) {
			t.Errorf("Tokenize(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}

// Ports tool-search.test.ts "matches Unicode queries against %s" (Pi 79006f06, #11037).
func TestBm25RankerMatchesUnicodeQueries(t *testing.T) {
	documents := []toolsearch.Document{
		{Name: "mcp__github__list_issues", Text: "List issues in a repository. state open or closed"},
		{Name: "mcp__github__create_pull_request", Text: "Open a pull request."},
	}
	for _, test := range [][2]string{
		{"搜索仓库问题", "问题"},
		{"文書を検索", "検索"},
		{"문서 검색", "검색"},
		{"ค้นหาเอกสาร", "เอกสาร"},
		{"Поиск документов", "документов"},
		{"البحث عن الملفات", "الملفات"},
		{"फ़ाइल खोजें", "खोजें"},
		{"Rechercher un café", "cafe\u0301"},
	} {
		if stripsDictionaries() && usesDictionary(test[0]) {
			continue
		}
		localized := toolsearch.CreateDocument(info("localized", test[0], ""), nil)
		got := names(toolsearch.NewBm25Ranker().Rank(test[1], append(append([]toolsearch.Document{}, documents...), localized), 8))
		if want := []string{"localized"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%q in %q: %q, want %q", test[1], test[0], got, want)
		}
	}
}

//go:embed testdata/tokenize_goldens.json
var tokenizeGoldens []byte

// usesDictionary reports whether the text holds a character of a script that ICU segments with a dictionary, the scripts a Piglet Binary built without the word dictionaries (D92) keeps whole.
func usesDictionary(text string) bool {
	for _, r := range text {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar) {
			return true
		}
	}
	return false
}

// The goldens are Pi's own tokenize at 79006f06 (testdata/gen_tokenize_goldens.mjs, Node 26 / ICU 78.3) over every script class: Latin with diacritics and in NFD, Cyrillic, Greek, Arabic,
// Hebrew, the Indic scripts, Hangul, Armenian, Georgian, Ethiopic, Tibetan, Mongolian and Cherokee, digits, and the scripts ICU segments with a dictionary (Han, Hiragana, Katakana, Thai, Lao,
// Khmer, Myanmar), phrases and random draws of each, mixed-script texts, lone surrogates and the Southeast Asian fixture of internal/wordsegmenter. Every term must match exactly; a Piglet
// that strips the word dictionaries keeps dictionary scripts whole (TestTokenizeKeepsDictionaryScriptRunsWholeWithoutDictionaries) and so skips the probes that hold one.
func TestTokenizeMatchesPiOnEveryScriptClass(t *testing.T) {
	var probes []struct {
		Class  string   `json:"class"`
		Text   string   `json:"text"`
		Tokens []string `json:"tokens"`
	}
	if err := jsstring.UnmarshalKeepingSurrogates(tokenizeGoldens, &probes); err != nil {
		t.Fatal(err)
	}
	stripped := stripsDictionaries()
	compared, skipped, nonEmpty := map[string]int{}, 0, 0
	for _, probe := range probes {
		if stripped && usesDictionary(probe.Text) {
			skipped++
			continue
		}
		compared[probe.Class]++
		if len(probe.Tokens) > 0 {
			nonEmpty++
		}
		if got := toolsearch.Tokenize(probe.Text); !slices.Equal(got, probe.Tokens) {
			t.Errorf("%s: Tokenize(%q) = %q, Pi %q", probe.Class, probe.Text, got, probe.Tokens)
		}
	}
	if nonEmpty < 2000 || (!stripped && skipped != 0) {
		t.Errorf("%d probes with terms, %d skipped: the goldens do not exercise the tokenizer", nonEmpty, skipped)
	}
	for _, class := range []string{"latin", "cyrillic", "greek", "arabic", "hebrew", "devanagari", "hangul", "thai", "lao", "khmer", "myanmar", "han", "hiragana", "katakana", "mixed"} {
		if compared[class] == 0 && (!stripped || !usesDictionaryClass(class)) {
			t.Errorf("no %s probe was compared", class)
		}
	}
	t.Logf("compared %v, skipped %d", compared, skipped)
}

func usesDictionaryClass(class string) bool {
	switch class {
	case "han", "hiragana", "katakana", "han-kana", "thai", "lao", "khmer", "myanmar":
		return true
	}
	return false
}

// A Piglet that strips the word dictionaries (D92), or a Binary built without them, segments each run of a dictionary script as one word, as ICU does without its dictionary data; the other scripts
// segment as in Pi.
func TestTokenizeKeepsDictionaryScriptRunsWholeWithoutDictionaries(t *testing.T) {
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.WordDictionaries))
	for _, test := range []struct {
		text string
		want []string
	}{
		{"搜索仓库问题", []string{"搜索仓库问题"}},
		{"ค้นหาเอกสาร", []string{"ค้นหาเอกสาร"}},
		{"搜索GitHub_repoIssues résumé cafés", []string{"搜索", "git", "hub", "repo", "issue", "résumé", "cafés"}},
		{"Поиск документов", []string{"поиск", "документов"}},
		{"문서 검색", []string{"문서", "검색"}},
	} {
		if got := toolsearch.Tokenize(test.text); !reflect.DeepEqual(got, test.want) {
			t.Errorf("Tokenize(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}
