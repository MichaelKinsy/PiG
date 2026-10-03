package hljs

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestJSRegExpMatchesNode compares the translated patterns with JavaScript on the constructs translateJSRegExp rewrites: ASCII word boundaries, ignoreCase canonicalization, line terminators, empty and complete classes, Annex B escapes and braces, named groups and code units (testdata/regexp-oracle.mjs, Node 24).
func TestJSRegExpMatchesNode(t *testing.T) {
	raw, err := os.ReadFile("testdata/regexp-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Source     string
		IgnoreCase bool
		Input      []rune
		Match      *struct{ Index, Length int }
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty oracle")
	}
	for _, c := range cases {
		re, err := newJSRegExp(c.Source, c.IgnoreCase)
		if err != nil {
			t.Errorf("/%s/: %v", c.Source, err)
			continue
		}
		got := re.exec(c.Input, 0)
		switch {
		case got == nil && c.Match == nil:
		case got == nil || c.Match == nil:
			t.Errorf("/%s/ i=%v on %q: match %v, JavaScript %v", c.Source, c.IgnoreCase, c.Input, got != nil, c.Match != nil)
		case got.index != c.Match.Index || got.spans[1]-got.spans[0] != c.Match.Length:
			t.Errorf("/%s/ i=%v on %q: [%d,+%d], JavaScript [%d,+%d]", c.Source, c.IgnoreCase, c.Input, got.index, got.spans[1]-got.spans[0], c.Match.Index, c.Match.Length)
		}
	}
}

// quantifiedGroups lists the capturing groups of a JavaScript pattern that sit inside a quantified group or are quantified themselves.
func quantifiedGroups(source string) map[int]bool {
	src := toUnits(source)
	quantified := map[int]bool{}
	type open struct{ first int }
	var stack []open
	groups := 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '[':
			i = classEnd(src, i)
		case '(':
			capturing := i+1 >= len(src) || src[i+1] != '?' || (i+3 < len(src) && src[i+2] == '<' && src[i+3] != '=' && src[i+3] != '!')
			if capturing {
				groups++
			}
			stack = append(stack, open{first: groups + 1})
			if capturing {
				stack[len(stack)-1].first = groups
			}
		case ')':
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			next := i + 1
			if next < len(src) && (src[next] == '*' || src[next] == '+' || src[next] == '?' || src[next] == '{' && braceQuantifierLength(src[next:]) > 0) {
				for g := top.first; g <= groups; g++ {
					quantified[g] = true
				}
			}
		}
	}
	return quantified
}

// backreferences lists the groups a JavaScript pattern refers back to.
func backreferences(source string) []int {
	t := &regexpTranslator{src: toUnits(source)}
	t.scanGroups()
	var refs []int
	src := t.src
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '[':
			i = classEnd(src, i)
		case '\\':
			if i+1 >= len(src) {
				continue
			}
			if src[i+1] == 'k' && t.names != nil {
				end := i + 3
				for end < len(src) && src[end] != '>' {
					end++
				}
				refs = append(refs, t.names[string(src[i+3:min(end, len(src))])])
			} else if src[i+1] >= '1' && src[i+1] <= '9' {
				n, end := 0, i+1
				for end < len(src) && src[end] >= '0' && src[end] <= '9' {
					n = n*10 + int(src[end]-'0')
					end++
				}
				if n <= t.groupCount {
					refs = append(refs, n)
				}
			}
			i++
		}
	}
	return refs
}

// TestGrammarsReadNoQuantifiedCaptures guards the one regexp2 difference the translation keeps: JavaScript resets the captures of a quantified group on each iteration and rejects an empty iteration, while regexp2 keeps earlier captures. Match extents are equal, so the difference is observable only where a backreference or a callback reads such a group. No grammar does.
func TestGrammarsReadNoQuantifiedCaptures(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	checked := 0
	for _, node := range r.nodes {
		var patterns []pattern
		var readsGroupOne []pattern
		switch node := node.(type) {
		case *Mode:
			patterns = append(patterns, node.begin, node.end, node.match, node.lexemes, node.beforeMatch, node.illegal)
			patterns = append(patterns, node.illegal.list...)
			for _, cb := range []callback{node.onBegin, node.onEnd} {
				if cb != nil && strings.HasPrefix(callbackName(cb), "endSameAsBegin.") {
					readsGroupOne = append(readsGroupOne, node.begin, node.end)
				}
			}
		case *keywordsObject:
			patterns = append(patterns, node.pattern)
		}
		for _, p := range patterns {
			source, ok := p.source()
			if !ok {
				continue
			}
			quantified := quantifiedGroups(source)
			for _, ref := range backreferences(source) {
				checked++
				if quantified[ref] {
					t.Errorf("/%s/ refers back to quantified group %d", source, ref)
				}
			}
		}
		for _, p := range readsGroupOne {
			source, ok := p.source()
			if !ok {
				continue
			}
			checked++
			if quantifiedGroups(source)[1] {
				t.Errorf("END_SAME_AS_BEGIN reads quantified group 1 of /%s/", source)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no backreference or callback group checked")
	}
}

func callbackName(cb callback) string {
	for name, candidate := range callbacks {
		if sameFunc(candidate, cb) {
			return name
		}
	}
	return ""
}

func sameFunc(a, b callback) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// The interactive mode loads every language on a background task while the owner loop highlights, so registries and the shared compiled-pattern cache must be safe for concurrent use. Run with -race.
func TestRegistriesHighlightWhileLoading(t *testing.T) {
	samples := map[string]string{"go": "func main() { fmt.Println(`x`) }", "python": "def f(x):\n    return f'{x}'", "bash": "echo \"$HOME\" | grep -c x", "json": `{"a": [1, true, null]}`}
	first, second := NewRegistry(), NewRegistry()
	var wg sync.WaitGroup
	wg.Go(first.LoadAllLanguages)
	for _, r := range []*Registry{first, second} {
		for language, code := range samples {
			wg.Go(func() {
				for range 20 {
					if _, err := r.Highlight(code, language, true); err != nil {
						t.Error(err)
						return
					}
				}
			})
		}
	}
	wg.Go(func() {
		for range 20 {
			if _, err := second.HighlightAuto("SELECT 1 FROM t", nil); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Wait()
	if !first.SupportsLanguage("ada") || second.SupportsLanguage("ada") {
		t.Fatal("loading every language affected the wrong registry")
	}
}
