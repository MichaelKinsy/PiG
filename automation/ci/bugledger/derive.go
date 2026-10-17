// SPDX-License-Identifier: MIT

package main

import (
	"regexp"
	"strings"
)

// internalMarker matches wording that belongs to the project's private coordination, not to a public document. A sentence that carries one is dropped from text taken from commit messages.
var internalMarker = regexp.MustCompile(`(?i:integrate-\d+|\b(lanes?|handoffs?|ready-queue|merge-train|reviewers?|staging|tern|fixlane|autobind|dcore|integrator)\b|\b(lg|rev|port|gap|ledger|win-pr|d-harness)-[a-z0-9-]+|team/|/home/|~/|\bprobes?\b|\bci\d+|\bpi-?(ai|tui|coding-agent) \d)|\bRed\b|\bGreen\b|\bQ\d\b|\bF\d{1,2}\b|\b[Pp]i v?\d+\.\d+(\.\d+)?\b|\d%` +
	// Words of the porting and review records: ledger rows and shards, review batches and rule requests, behavior contract IDs, mutation checks, and the mapping files.
	`|(?i:\bshards?\b|\b(gap|open|closure|not-exercised) rows?\b|\b\d+ rows\b|\brows of\b|\bbatch-[a-z0-9-]+|\brule requests?\b|\breview(ed|'s)?\b|\bmutations?\b|\bmutation-checked\b|\bcontracts? [a-z-]+/[a-z-]+|PORT_MAP|renames\.json|mapping-v\d|\bbaselined?\b|\bratchet|\boracles?\b|\bdetector\b|\blead'?s ruling\b|\bthe lead\b|divergence-guard|divguard|portlint|comb-audit)|\bS\d+[a-z]\b` +
	// Private infrastructure: the lane hosts, their shared trees and home paths. The literals are assembled so this file does not name them.
	`|(?i:\bimla` + `dris\b|\bsmc` + `\d\b|\bpig-` + `(lanes|staging)\b|\.dev` + `cache\b|/Users/[a-z])`)

var (
	hexToken = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	// removedBeforePunct is a removed hash with the space before it, when punctuation follows, as in "pi-mono <hash>: ...".
	removedBeforePunct = regexp.MustCompile(`\s*\x00([:;,.)])`)
)

// isCommitHash reports whether a hex token is a commit hash rather than a number: it has both letters and digits.
func isCommitHash(h string) bool {
	return strings.ContainsAny(h, "abcdef") && strings.ContainsAny(h, "0123456789")
}

// sentences splits prose into sentences without splitting inside a code span or after an abbreviation such as "e.g.".
func sentences(s string) []string {
	var out []string
	var cur strings.Builder
	inCode := false
	runes := []rune(s)
	for i, r := range runes {
		cur.WriteRune(r)
		if r == '`' {
			inCode = !inCode
		}
		if inCode || (r != '.' && r != '!' && r != '?') {
			continue
		}
		if i+1 < len(runes) && runes[i+1] != ' ' && runes[i+1] != '\n' {
			continue
		}
		end := strings.TrimSpace(cur.String())
		if strings.HasSuffix(end, "e.g.") || strings.HasSuffix(end, "i.e.") || strings.HasSuffix(end, "etc.") || strings.HasSuffix(end, "vs.") {
			continue
		}
		if i+2 < len(runes) && runes[i+1] == ' ' && runes[i+2] >= 'a' && runes[i+2] <= 'z' {
			continue
		}
		out = append(out, end)
		cur.Reset()
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out
}

var (
	beforeSentence = regexp.MustCompile(`^(Before\b|Previously\b|PiG (previously|used to)\b|It (used to|had)\b|They (used to|had)\b|The \w[\w ]*? (used to|had)\b|.*\bused to\b)`)
	causeWords     = regexp.MustCompile(`(?i)\b(because|caused by|root cause|the cause|due to|since the|raced|race between|never (re)?(armed|read|checked|set|cleared|called)|was not|did not|never reached)\b`)
	causeLabel     = regexp.MustCompile(`(?im)^(root cause|cause)\s*:\s*`)
)

// symptomOf returns the sentence that says what the user saw: a "Before..." or "used to" sentence when the note has one, otherwise the first sentence.
func symptomOf(note string) (symptom, fix string) {
	ss := sentences(note)
	if len(ss) == 0 {
		return note, ""
	}
	for i, s := range ss {
		if beforeSentence.MatchString(s) {
			rest := append(append([]string{}, ss[:i]...), ss[i+1:]...)
			for _, p := range []string{"Before this fix,", "Before the fix,", "Before,", "Before"} {
				if t, ok := strings.CutPrefix(s, p); ok {
					s = t
					break
				}
			}
			return upperFirst(s), strings.Join(rest, " ")
		}
	}
	return ss[0], strings.Join(ss[1:], " ")
}

func upperFirst(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, ","))
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

type paragraphs []string

func splitParagraphs(body string) paragraphs {
	var out paragraphs
	for p := range strings.SplitSeq(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		p = strings.TrimSpace(spaceRun.ReplaceAllString(p, " "))
		if p == "" || strings.HasPrefix(p, "Signed-off-by:") || strings.HasPrefix(p, "Co-authored-by:") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// publicText drops the sentences that carry private wording and the bare commit hashes from s. It returns "" when nothing public remains.
// bytePhrase is wording the public-claims check rejects in user-facing text: a commit message may say a result is byte-identical to Pi's for one pinned output, but the ledger page is user-facing.
var bytePhrase = regexp.MustCompile(`(?i)\bbyte[- ](?:for[- ]byte|identical)\b`)

func publicText(s string) string {
	var keep []string
	for _, sentence := range sentences(s) {
		if internalMarker.MatchString(sentence) || bytePhrase.MatchString(sentence) {
			continue
		}
		// A sentence whose subject is another commit names it as an earlier change.
		if lead := hexToken.FindStringIndex(sentence); lead != nil && lead[0] == 0 && isCommitHash(sentence[:lead[1]]) {
			sentence = "An earlier change" + sentence[lead[1]:]
		}
		sentence = hexToken.ReplaceAllStringFunc(sentence, func(h string) string {
			if isCommitHash(h) {
				return "\x00"
			}
			return h
		})
		sentence = removedBeforePunct.ReplaceAllString(sentence, "$1")
		keep = append(keep, strings.ReplaceAll(sentence, "\x00", ""))
	}
	return strings.TrimSpace(spaceRun.ReplaceAllString(strings.Join(keep, " "), " "))
}

const maxCause = 700

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return strings.TrimRight(s[:cut], " ,;:") + "…"
}

// rootCauseOf reads the cause of the bug from the sources in order of how directly they state it: a "Root cause:" paragraph of the fix commit message, a commit paragraph or release-note sentence that says why, and last the first paragraph of the commit message, which for a fix describes the behavior Pi has and the mismatch.
func rootCauseOf(messages []string, note string) (text, from string) {
	var firsts []string
	for _, msg := range messages {
		ps := splitParagraphs(msg)
		for _, p := range ps {
			if loc := causeLabel.FindStringIndex(p); loc != nil && loc[0] == 0 {
				if t := publicText(p[loc[1]:]); t != "" {
					return clip(t, maxCause), "commit"
				}
			}
		}
		for i, p := range ps {
			if i == 0 {
				continue // the subject line is not a paragraph of the body
			}
			if causeWords.MatchString(p) {
				if t := publicText(p); t != "" {
					return clip(t, maxCause), "commit"
				}
			}
		}
		if len(ps) > 1 {
			firsts = append(firsts, ps[1])
		}
	}
	for _, s := range sentences(note) {
		if causeWords.MatchString(s) && !internalMarker.MatchString(s) {
			return s, "release note"
		}
	}
	for _, p := range firsts {
		if t := publicText(p); t != "" {
			return clip(t, maxCause), "commit"
		}
	}
	return "", ""
}

type impactRule struct {
	re    *regexp.Regexp
	label string
}

// impactRules classify a release note by what it says went wrong. The first rule that matches decides. The classes describe the wording of the note, not a measured severity.
var impactRules = []impactRule{
	{regexp.MustCompile(`(?i)credential|secret|permission|owner-only|world-readable|injection|sandbox escape|unauthori[sz]ed`), "Credential or permission exposure"},
	{regexp.MustCompile(`(?i)crash|panic|\bhang|hung\b|freez|deadlock|never (returns|ends|exits|finishes)|without bound|unbounded|out of memory|exits? (1|0)\b|outlives|exited 1`), "Crash, hang or runaway process"},
	{regexp.MustCompile(`(?i)\blost\b|loses|corrupt|overwr[io]te|silently dropped|data loss|dropped (the|a|every|members)|discarded`), "Lost or altered data"},
	{regexp.MustCompile(`(?i)\bfail(s|ed|ure)?\b|\berror(s|ed)?\b|rejected|refus|EPERM|cannot|could not|\binvalid\b|no longer (works|runs|loads)|did not (load|start|run)`), "An operation failed or was rejected"},
	{regexp.MustCompile(`(?i)render|\bdraw|colou?r|highlight|display|repaint|cursor|layout|padded|spinner|glyph`), "Wrong display"},
	{regexp.MustCompile(`(?i)\bsorted|order|escaped|wrong|incorrect|instead of`), "Wrong output or ordering"},
}

func impactOf(note string) string {
	for _, r := range impactRules {
		if r.re.MatchString(note) {
			return r.label
		}
	}
	return "Behavior differed from Pi"
}

var platformWords = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`(?i)\bwindows\b|\.exe\b|EPERM|PowerShell|\bpwsh\b`), "Windows"},
	{regexp.MustCompile(`(?i)\bmacos\b|darwin|\bmac os\b`), "macOS"},
	{regexp.MustCompile(`(?i)\blinux\b`), "Linux"},
	{regexp.MustCompile(`(?i)\bWSL\b`), "WSL"},
	{regexp.MustCompile(`(?i)\btermux\b|\bandroid\b`), "Android and Termux"},
}

func platformOf(note string) string {
	var names []string
	for _, p := range platformWords {
		if p.re.MatchString(note) {
			names = append(names, p.name)
		}
	}
	if len(names) == 0 {
		return "All platforms"
	}
	return strings.Join(names, ", ")
}

// piPortCommit matches the head of a commit message that ports a Pi change: "Port Pi 1.1.0 ...", "Follow Pi 1.0.4 ...", "Port of Pi main <commit>" (a fix on Pi's main branch after the pinned release), "Ports Pi v1.0.4..v1.1.0 <commit> (#<n>)", "... (Pi 1.1.0)", or a first paragraph that cites a Pi release, a Pi source file and the Pi pull request.
var piPortCommit = regexp.MustCompile(`(?m)^(Port(s|ed)?|Follow(s|ed)?) Pi (v?\d|\d+\.\d+)|^Port of Pi main [0-9a-f]{7,40}\b|\(Pi \d+\.\d+\.\d+\)\s*$|^Pi v?\d+\.\d+\.\d+ \S+\.ts\b.*\(#\d+\)`)

// portHead returns the subject and the first paragraph of a commit message: where a port names what it ports. Later paragraphs cite Pi for comparison, which does not make the commit a port.
func portHead(msg string) string {
	ps := splitParagraphs(msg)
	if len(ps) > 2 {
		ps = ps[:2]
	}
	return strings.Join(ps, "\n")
}

var piPortRE = regexp.MustCompile(`(?i)\bports? (of )?(the |a )?(fix|bug fix)es? (from|in|of) Pi\b|\bPi (\d+\.\d+\.\d+ )?(fixed|fixes|shipped a fix)\b|\bfixed in Pi\b|\bPi's fix\b|\bPort(ed|s)? Pi's fix`)

// titleOf shortens the first sentence of a note into a heading.
func titleOf(note string) string {
	ss := sentences(note)
	t := note
	if len(ss) > 0 {
		t = ss[0]
	}
	t = strings.TrimSuffix(t, ".")
	return clip(t, 110)
}
