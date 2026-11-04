// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

// Rule codes. FG006 (private term) is never allowlisted.
const (
	codeOpaque    = "FG001"
	codeBilling   = "FG002"
	codeProvider  = "FG003"
	codeHomePath  = "FG004"
	codeReasoning = "FG005"
	codePrivate   = "FG006"
)

// Thresholds, tuned against the repository's synthetic fixtures: the longest value under an opaque-field key in the tree is shorter than 40 characters, and no file carries more than 26 distinct words of reasoning-field text.
const (
	// A captured signature or encrypted reasoning value is hundreds to tens of thousands of characters; a synthetic stand-in is a short word.
	opaqueFieldMin = 80
	// A base64 run this long under any key is a captured blob, not a stand-in. The longest legitimate mixed-case run in the tree (an embedded JPEG sample) is 1440 characters.
	opaqueBlobMin = 4096
	// Request-id style header values from a real provider are UUIDs or longer; fixtures use values like r1 or request-1.
	requestIDMin = 20
	// A captured stream spreads reasoning over many small deltas, so the text accumulates per file.
	reasoningCharsMin = 500
	// Distinct words, not total words: the oracle transcripts repeat one-word deltas thousands of times, while prose spreads over many different words.
	reasoningDistinctWordsMin = 50
	// A request-id value must look random: enough digits and distinct characters that a readable label such as session-affinity or 11111111-2222-3333-4444-555555555555 does not qualify.
	requestIDDigitsMin   = 6
	requestIDDistinctMin = 8
)

const requestIDKeys = `x-request-id|x-github-request-id|x-ms-request-id|x-ms-client-request-id|apim-request-id|request-id|x-amzn-requestid|cf-ray`

var (
	// Anthropic's redacted_thinking block carries its encrypted payload under a plain data key.
	redactedData = regexp.MustCompile(`\\*"data\\*"\s*:\s*\\*"([A-Za-z0-9+/_=-]{` + strconv.Itoa(opaqueFieldMin) + `,})`)
	opaqueField  = regexp.MustCompile(`(?i)(?:encrypted_content|reasoning_opaque|thinking_signature|thought_?signature|encrypted_reasoning|reasoning_encrypted_content|redacted_data|signature)\\*["']?\s*[:=]\s*\\*["']([A-Za-z0-9+/_=-]{` + strconv.Itoa(opaqueFieldMin) + `,})`)
	hexOnly      = regexp.MustCompile(`^[0-9a-fA-F]+$`)

	billingKey = regexp.MustCompile(`(?i)\b(?:copilot_usage|cost_per_batch|total_nano_aiu|quota_snapshots|premium_interactions|copilot_quota[a-z_]*|x-copilot-[a-z-]+)\b`)
	requestID  = regexp.MustCompile(`(?i)\b(?:` + requestIDKeys + `)\\*["']?\s*[:=,]\s*\\*["']?\s*([0-9A-Za-z:._-]{` + strconv.Itoa(requestIDMin) + `,})`)
	// HAR headers are {"name":"x-request-id","value":"..."} objects, so the value follows a second key.
	requestIDHAR = regexp.MustCompile(`(?i)\b(?:` + requestIDKeys + `)\\*"\s*,\s*\\*"value\\*"\s*:\s*\\*"([0-9A-Za-z:._-]{` + strconv.Itoa(requestIDMin) + `,})`)

	// Bedrock and Vertex route Anthropic ids through an infix (toolu_bdrk_01..., toolu_vrtx_...).
	providerID   = regexp.MustCompile(`chatcmpl-[A-Za-z0-9]{16,}|\b(?:toolu|req)_(?:[a-z]{4}_)?(?:01)?[A-Za-z0-9]{16,}|\bmsg_(?:[a-z]{4}_)?(?:01)?[A-Za-z0-9]{20,}|\bresp_[0-9a-f]{24,}|\brs_[0-9a-f]{24,}|\bcall_[A-Za-z0-9]{20,}|\bfc_[0-9a-f]{24,}|\bgen-[0-9]{9,}-[A-Za-z0-9]{10,}`)
	providerTime = regexp.MustCompile(`"(?:created|created_at)"\s*:\s*(?:(1[6-9][0-9]{8})\b|"20[2-3][0-9]-[0-9]{2}-[0-9]{2}T[0-9:]{8}\.([0-9]{3})[0-9]*)`)

	posixHome   = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])/(?:Users|home)/([A-Za-z0-9._-]+)`)
	windowsHome = regexp.MustCompile(`[A-Za-z]:\\+Users\\+([A-Za-z0-9._ -]+)`)

	reasoningLine = regexp.MustCompile(`\\*"(?:reasoning_text|reasoning_content|reasoning|thinking|summary_text|reasoning_summary|thought|text|delta)\\*"\s*:\s*\\*"((?:[^"\\]|\\[^"])*)`)
	// Go struct literals in fixture-like sources: Thinking: "...", Reasoning: `...`.
	goReasoningLine = regexp.MustCompile("\\b(?:Thinking|Reasoning|ReasoningText|ReasoningContent|SummaryText)\\s*:\\s*(?:\"((?:[^\"\\\\]|\\\\.)*)\"|`([^`]*)`)")
	reasoningWords  = regexp.MustCompile(`[A-Za-z]{2,}`)
)

// syntheticHomes are the placeholder account names fixtures use. Any other /Users/<name> or /home/<name> is a real account until proven otherwise.
var syntheticHomes = map[string]bool{
	"me": true, "me2": true, "user": true, "user2": true, "you": true, "u": true, "x": true, "example": true,
	"tester": true, "someone": true, "victim": true, "owner": true, "foo": true, "alice": true, "bob": true,
	"test": true, "name": true, "username": true, "runner": true, "pig": true,
	"public": true, "default": true, "shared": true,
}

var (
	preOpaque    = [][]byte{[]byte("ignature"), []byte("ncrypted"), []byte("paque"), []byte("redacted_data"), []byte("redacted_thinking")}
	preBilling   = [][]byte{[]byte("usage"), []byte("batch"), []byte("nano_aiu"), []byte("quota"), []byte("premium"), []byte("x-copilot"), []byte("request-id"), []byte("requestid"), []byte("cf-ray")}
	preProvider  = [][]byte{[]byte("chatcmpl-"), []byte("msg_"), []byte("resp_"), []byte("rs_"), []byte("req_"), []byte("toolu_"), []byte("call_"), []byte("fc_"), []byte("gen-"), []byte("created")}
	preHome      = [][]byte{[]byte("/Users/"), []byte("/home/"), []byte(`Users\`)}
	preReasoning = [][]byte{[]byte("reasoning"), []byte("thinking"), []byte("thought"), []byte("summary_text"), []byte("Thinking"), []byte("Reasoning"), []byte("SummaryText")}
)

func containsAny(line []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(line, n) {
			return true
		}
	}
	return false
}

type finding struct {
	path    string
	line    int
	code    string
	message string
}

// reasoningTally accumulates natural-language reasoning text across one file.
type reasoningTally struct {
	chars, firstLine int
	words            map[string]struct{}
}

func (r *reasoningTally) add(line int, value []byte) {
	words := reasoningWords.FindAll(value, -1)
	if len(words) == 0 {
		return
	}
	if r.firstLine == 0 {
		r.firstLine = line
		r.words = map[string]struct{}{}
	}
	for _, w := range words {
		r.words[string(bytes.ToLower(w))] = struct{}{}
	}
	r.chars += len(value)
}

// scanText applies the structural detectors to one text file. fixture selects the detectors that only make sense in test data: provider ids and timestamps, and home paths.
func scanText(path string, data []byte, fixture bool) []finding {
	var found []finding
	add := func(line int, code, message string) {
		found = append(found, finding{path, line, code, message})
	}
	var tally reasoningTally
	number := 0
	for rest := data; len(rest) > 0; {
		number++
		var line []byte
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			line, rest = rest, nil
		}
		if len(line) < 12 {
			continue
		}
		if containsAny(line, preOpaque) {
			for _, m := range opaqueField.FindAllSubmatch(line, -1) {
				if !hexOnly.Match(m[1]) {
					add(number, codeOpaque, fmt.Sprintf("opaque encrypted or signature value of %d characters", len(m[1])))
				}
			}
		}
		if bytes.Contains(line, []byte("redacted_thinking")) {
			for _, m := range redactedData.FindAllSubmatch(line, -1) {
				if !hexOnly.Match(m[1]) {
					add(number, codeOpaque, fmt.Sprintf("redacted thinking payload of %d characters", len(m[1])))
				}
			}
		}
		if len(line) >= opaqueBlobMin {
			for _, blob := range base64Runs(line, opaqueBlobMin) {
				if !hexOnly.Match(blob) && mixedBase64(blob) {
					add(number, codeOpaque, fmt.Sprintf("base64-like blob of %d characters", len(blob)))
				}
			}
		}
		if containsAny(line, preBilling) {
			if billingKey.Match(line) {
				add(number, codeBilling, "provider billing or quota row")
			}
			for _, re := range []*regexp.Regexp{requestID, requestIDHAR} {
				for _, m := range re.FindAllSubmatch(line, -1) {
					if randomLooking(m[1]) {
						add(number, codeBilling, fmt.Sprintf("provider request-id value of %d characters", len(m[1])))
					}
				}
			}
		}
		if containsAny(line, preReasoning) {
			for _, m := range reasoningLine.FindAllSubmatch(line, -1) {
				tally.add(number, m[1])
			}
			for _, m := range goReasoningLine.FindAllSubmatch(line, -1) {
				value := m[1]
				if len(value) == 0 {
					value = m[2]
				}
				tally.add(number, value)
			}
		}
		if !fixture {
			continue
		}
		if containsAny(line, preProvider) {
			if providerID.Match(line) {
				add(number, codeProvider, "real-looking provider id")
			}
			for _, m := range providerTime.FindAllSubmatch(line, -1) {
				if capturedTime(m) {
					add(number, codeProvider, "real-looking provider timestamp")
				}
			}
		}
		if containsAny(line, preHome) {
			for range homeMatches(line) {
				add(number, codeHomePath, homeMessage)
			}
		}
	}
	// Reassembled streams: a path or reasoning sentence split across deltas is invisible line by line.
	var streamTally reasoningTally
	for _, s := range reassemble(path, data) {
		if fixture {
			for _, at := range homeMatches(s.buf) {
				add(s.lineAt(at), codeHomePath, homeMessage)
			}
		}
		if s.whole && reasoningFields[s.field] {
			streamTally.add(s.lines[0], s.buf)
		}
	}
	for _, t := range []*reasoningTally{&tally, &streamTally} {
		if t.chars >= reasoningCharsMin && len(t.words) >= reasoningDistinctWordsMin {
			add(t.firstLine, codeReasoning, fmt.Sprintf("natural-language reasoning text (%d characters, %d distinct words) in stream or session fields", t.chars, len(t.words)))
		}
	}
	return dedupe(found)
}

const homeMessage = "absolute home path of a non-placeholder account"

// homeMatches returns the start offsets of home paths whose account is not a placeholder.
func homeMatches(b []byte) []int {
	var at []int
	for _, m := range posixHome.FindAllSubmatchIndex(b, -1) {
		if !syntheticHomes[string(bytes.ToLower(b[m[2]:m[3]]))] {
			at = append(at, m[2])
		}
	}
	for _, m := range windowsHome.FindAllSubmatchIndex(b, -1) {
		if !syntheticHomes[string(bytes.ToLower(bytes.TrimSpace(b[m[2]:m[3]])))] {
			at = append(at, m[2])
		}
	}
	return at
}

// dedupe drops repeated findings so a value seen both line by line and in a reassembled stream is reported once.
func dedupe(found []finding) []finding {
	seen := map[finding]bool{}
	out := found[:0]
	for _, f := range found {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func isBase64Byte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '_' || c == '-'
}

// base64Runs returns the maximal runs of base64 or base64url characters at least min long. RE2 caps a repeat count at 1000, so the long-run scan is explicit.
func base64Runs(line []byte, min int) [][]byte {
	var runs [][]byte
	start := -1
	for i := 0; i <= len(line); i++ {
		if i < len(line) && isBase64Byte(line[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && i-start >= min {
			runs = append(runs, line[start:i])
		}
		start = -1
	}
	return runs
}

// randomLooking reports an identifier with the digit and character spread of a generated id.
func randomLooking(value []byte) bool {
	var digits int
	distinct := map[byte]struct{}{}
	for _, c := range value {
		if c >= '0' && c <= '9' {
			digits++
		}
		if isBase64Byte(c) && c != '-' && c != '_' && c != '+' && c != '/' {
			distinct[bytes.ToLower([]byte{c})[0]] = struct{}{}
		}
	}
	return digits >= requestIDDigitsMin && len(distinct) >= requestIDDistinctMin
}

// mixedBase64 reports a run that mixes lower case, upper case and digits, which separates encoded binary from a long word or a repeated filler.
func mixedBase64(b []byte) bool {
	var lower, upper, digit bool
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	return lower && upper && digit
}

// capturedTime reports a created or created_at value a fixture author would not type: a non-round epoch second or non-zero milliseconds.
func capturedTime(m [][]byte) bool {
	if len(m[1]) > 0 {
		seconds, err := strconv.ParseInt(string(m[1]), 10, 64)
		return err == nil && seconds%1000 != 0
	}
	return len(m[2]) > 0 && string(m[2]) != "000"
}
