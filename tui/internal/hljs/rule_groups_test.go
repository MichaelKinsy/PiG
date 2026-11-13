package hljs

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// highlightJSJoin is highlight.js join(regexps, "|"): each pattern in its own group, with backreferences renumbered. It is the reference joinRules is held to when it keeps every group.
func highlightJSJoin(regexps []pattern) string {
	numCaptures := 0
	parts := make([]string, len(regexps))
	for i, regex := range regexps {
		numCaptures++
		offset := numCaptures
		re, _ := regex.source()
		var out strings.Builder
		for re != "" {
			loc := backrefRE.FindStringSubmatchIndex(re)
			if loc == nil {
				out.WriteString(re)
				break
			}
			out.WriteString(re[:loc[0]])
			match := re[loc[0]:loc[1]]
			re = re[loc[1]:]
			if match[0] == '\\' && loc[2] >= 0 {
				n, _ := strconv.Atoi(match[1:])
				out.WriteString(`\` + strconv.Itoa(n+offset))
			} else {
				out.WriteString(match)
				if match == "(" {
					numCaptures++
				}
			}
		}
		parts[i] = "(" + out.String() + ")"
	}
	return strings.Join(parts, "|")
}

// TestJoinRulesKeepingEveryGroupIsHighlightJSJoin: with every group kept, the expression of every matcher of every language is the one highlight.js joins, and each rule's group has the number highlight.js keeps in matchIndexes: one more than the groups of the rules before it.
func TestJoinRulesKeepingEveryGroupIsHighlightJSJoin(t *testing.T) {
	ruleGroupsLeftOut = false
	defer func() { ruleGroupsLeftOut = true }()
	r := NewRegistry()
	r.LoadAllLanguages()
	languages, matchers := r.Languages(), 0
	for _, lang := range languages {
		for _, m := range matchersOf(compileLanguage(r.getLanguage(lang))) {
			patterns := make([]pattern, len(m.regexes))
			matchAt := 1
			for i, rule := range m.regexes {
				patterns[i] = rule.re
				if rule.group != matchAt {
					t.Fatalf("%s: rule %d of %q has group %d, highlight.js matchIndexes %d", lang, i, m.matcherRe.source, rule.group, matchAt)
				}
				matchAt += countMatchGroups(rule.re) + 1
			}
			if want := highlightJSJoin(patterns); m.matcherRe.source != want {
				t.Fatalf("%s: joinRules gives %q, highlight.js join %q", lang, m.matcherRe.source, want)
			}
			matchers++
		}
	}
	if matchers < len(languages) {
		t.Fatalf("%d matchers of %d languages", matchers, len(languages))
	}
	t.Logf("%d matchers of %d languages", matchers, len(languages))
}

// TestLeavingRuleGroupsOutHighlightsTheSame: the HTML of every corpus sample is the same whether the matchers' expressions hold the groups inside their rules or leave them out.
func TestLeavingRuleGroupsOutHighlightsTheSame(t *testing.T) {
	without := NewRegistry()
	without.LoadAllLanguages()
	ruleGroupsLeftOut = false
	with := NewRegistry()
	with.LoadAllLanguages()
	defer func() { ruleGroupsLeftOut = true }()
	for _, c := range resumeCorpus(t) {
		if !with.SupportsLanguage(c.Lang) {
			continue
		}
		ruleGroupsLeftOut = true
		got, err := without.Highlight(c.Code, c.Lang, true)
		ruleGroupsLeftOut = false
		want, wantErr := with.Highlight(c.Code, c.Lang, true)
		if (err == nil) != (wantErr == nil) || got != want {
			t.Fatalf("%s (%s): groups left out %q (%v), groups kept %q (%v)", c.ID, c.Lang, got, err, want, wantErr)
		}
	}
}

// TestLeavingRuleGroupsOutMatchesTheSame: scanning every corpus sample (and a text of punctuation and non-ASCII units) the way the highlighter does, from each match to the next, every matcher of the sample's language finds the same match of the same rule whether its expression holds the groups inside its rules or leaves them out, and the groups of a rule whose callbacks read them are the same. Under the race detector every eighth matcher is compared.
func TestLeavingRuleGroupsOutMatchesTheSame(t *testing.T) {
	without := NewRegistry()
	without.LoadAllLanguages()
	with := NewRegistry()
	with.LoadAllLanguages()
	// The flag is read when a matcher compiles, and the variants of a matcher compile as it runs.
	defer func() { ruleGroupsLeftOut = true }()
	type pair struct{ without, with []*multiRegex }
	matchers := map[string]pair{}
	checked, leftOut := 0, 0
	compare := func(c corpusSample, text string) {
		units := stringToUnits(text)
		for i, m := range matchers[c.Lang].without {
			if raceEnabled && i%8 != 0 {
				continue
			}
			kept := matchers[c.Lang].with[i]
			for p := 0; p <= len(units); {
				m.lastIndex, kept.lastIndex = p, p
				ruleGroupsLeftOut = true
				got := m.exec(units, new(enhancedMatch))
				ruleGroupsLeftOut = false
				want := kept.exec(units, new(enhancedMatch))
				checked++
				same := (want == nil) == (got == nil)
				if same && want != nil {
					same = want.index == got.index && want.match.end == got.match.end && want.position == got.position && want.matchType() == got.matchType()
					if same && got.opts != nil && got.opts.groups {
						rule := slices.IndexFunc(m.regexes, func(r multiRegexRule) bool { return r.opts == got.opts })
						for k := 1; same && k <= countMatchGroups(m.regexes[rule].re); k++ {
							wantText, wantOK := want.group(k)
							gotText, gotOK := got.group(k)
							same = wantOK == gotOK && string(wantText) == string(gotText)
						}
					}
				}
				if !same {
					t.Fatalf("%s %s from %d: with the groups left out %v, with them %v\nmatcher %s", c.ID, c.Lang, p, got, want, m.matcherRe.source)
				}
				if want == nil {
					break
				}
				p = want.index + 1
			}
		}
	}
	for _, c := range distinctSamples(t) {
		if !without.SupportsLanguage(c.Lang) {
			continue
		}
		if _, ok := matchers[c.Lang]; !ok {
			ruleGroupsLeftOut = true
			a := matchersOf(compileLanguage(without.getLanguage(c.Lang)))
			ruleGroupsLeftOut = false
			b := matchersOf(compileLanguage(with.getLanguage(c.Lang)))
			if len(a) != len(b) {
				t.Fatalf("%s: %d matchers with the groups left out, %d with them", c.Lang, len(a), len(b))
			}
			matchers[c.Lang] = pair{a, b}
			for i, m := range a {
				if m.matcherRe.groups < b[i].matcherRe.groups {
					leftOut++
				}
			}
			compare(c, dispatchProbeText)
		}
		compare(c, c.Code)
	}
	t.Logf("%d matches compared; %d matchers have groups that are left out", checked, leftOut)
	if checked == 0 || leftOut == 0 {
		t.Fatalf("%d matches compared, %d matchers with groups left out: the corpus no longer exercises the matchers", checked, leftOut)
	}
}

// TestTheEmptyRuleMatchesTheEmptyStringEverywhere: the pattern of a mode without a begin or an end matches the empty string at every position of a text, its end included, so a matcher that has only such rules left at a position answers without running its expression; and a matcher of that one rule answers what its expression does.
func TestTheEmptyRuleMatchesTheEmptyStringEverywhere(t *testing.T) {
	texts := [][]rune{nil, stringToUnits(dispatchProbeText + "ab cd_9\n\nx"), stringToUnits("_"), stringToUnits(" ")}
	for _, ignoreCase := range []bool{false, true} {
		re, err := newJSRegExp(matchesEmptyEverywhere, ignoreCase)
		if err != nil {
			t.Fatal(err)
		}
		for _, units := range texts {
			for p := 0; p <= len(units); p++ {
				if m, ok := re.exec(units, p); !ok || m.index != p || m.end != p {
					t.Fatalf("/%s/ from %d of %q: %s, want the empty string there", matchesEmptyEverywhere, p, string(units), describeMatch(m, ok))
				}
				if m, ok := re.execAt(units, p); !ok || m.index != p || m.end != p {
					t.Fatalf("/%s/ at %d of %q: %s, want the empty string there", matchesEmptyEverywhere, p, string(units), describeMatch(m, ok))
				}
			}
		}
	}
	r := NewRegistry()
	r.LoadAllLanguages()
	units := texts[1]
	matchers := 0
	for _, lang := range []string{"typescript", "python", "json", "go", "bash", "xml", "css", "sql"} {
		for _, m := range matchersOf(compileLanguage(r.getLanguage(lang))) {
			if !m.emptyRule {
				continue
			}
			matchers++
			for p := 0; p <= len(units)+1; p++ {
				m.lastIndex = p
				got := m.exec(units, new(enhancedMatch))
				want, wantOK := m.matcherRe.exec(units, p)
				if (got != nil) != wantOK || got != nil && (got.index != want.index || got.match.end != want.end || got.opts != m.regexes[0].opts) {
					t.Fatalf("%s: the matcher of the empty rule from %d: %v, its expression %s", lang, p, got, describeMatch(want, wantOK))
				}
			}
		}
	}
	if matchers == 0 {
		t.Fatal("no matcher of the empty rule alone among the languages")
	}
}

// TestDispatchLiteralIsAPlainText pins which patterns a dispatch compares as plain text: units that stand for themselves and escaped ASCII punctuation. Under ignoreCase a unit of the text matches the units of its case class, as the pattern's expression does.
func TestDispatchLiteralIsAPlainText(t *testing.T) {
	for _, c := range []struct{ source, text string }{
		{`'`, "'"}, {`"`, `"`}, {`//`, "//"}, {`\/\*`, "/*"}, {`\*\/`, "*/"}, {`:=`, ":="}, {`<!--`, "<!--"}, {`\\`, `\`}, {`#`, "#"}, {"é", "é"},
		{`a|b`, ""}, {`\n`, ""}, {`a+`, ""}, {`\w`, ""}, {`[a]`, ""}, {`a{2}`, ""}, {`^#`, ""}, {`#$`, ""}, {`a.b`, ""}, {`(a)`, ""}, {``, ""}, {`\`, ""}, {`\_`, ""}, {`a]`, ""},
	} {
		got := newDispatchLiteral(c.source, false)
		if (got == nil) != (c.text == "") || got != nil && string(got.units) != c.text {
			t.Errorf("newDispatchLiteral(%q) = %v, want the text %q", c.source, got, c.text)
		}
	}
	text := stringToUnits("xAb-aB")
	for _, c := range []struct {
		ignoreCase bool
		at         int
		want       bool
	}{{false, 1, false}, {true, 1, true}, {true, 4, true}, {false, 4, false}, {true, 5, false}, {true, 0, false}} {
		if got := newDispatchLiteral("ab", c.ignoreCase).at(text, c.at); got != c.want {
			t.Errorf("the text ab, ignoreCase %v, at %d of %q: %v, want %v", c.ignoreCase, c.at, string(text), got, c.want)
		}
	}
}
