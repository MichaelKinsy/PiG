package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

// sharedNormalizers are rewrites every journey applies to rendered frames. Each states the Pi source or numbered divergence that makes PiG and Pi differ on purpose.
var sharedNormalizers = []Normalizer{
	{Pattern: `\b(\d+(?:\.\d+)?)s\b`, With: "<N>s", Reason: "elapsed seconds of a running or finished tool and turn depend on host load (Pi interactive-mode.ts, bash-execution.ts)"},
	{Pattern: `\b\d+ms\b`, With: "<N>ms", Reason: "elapsed milliseconds depend on host load"},
	{Pattern: `[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`, With: "⠋", Reason: "the loader's spinner glyph is an animation phase (Pi loader.ts)"},
	{Pattern: `pi-bash-[0-9a-f]+\.log`, With: "<bash-log>", Reason: "Pi names the truncated-output spill file with random bytes (bash.ts getTempFilePath)"},
}

var compiledShared []*regexp.Regexp

func init() {
	for _, n := range sharedNormalizers {
		compiledShared = append(compiledShared, regexp.MustCompile(n.Pattern))
	}
}

// normalizeFrame rewrites a captured screen into the form compared across products.
// normalizeFrameInfo is normalizeFrame that also reports whether a startup header was on screen.
func (c *Run) normalizeFrameInfo(s string) (string, bool) {
	for _, p := range []struct{ from, to string }{{c.SB.Cwd, "<cwd>"}, {c.SB.Root, "<root>"}, {c.SB.Home, "<home>"}} {
		s = strings.ReplaceAll(s, p.from, p.to)
	}
	s = c.maskSessionIdentity(s)
	s, header := stripHeader(s)
	for i, re := range compiledShared {
		s = re.ReplaceAllString(s, sharedNormalizers[i].With)
	}
	s = c.applyJourneyNormalizers(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n"), header
}

var (
	borderRow = regexp.MustCompile(`^─{2,}(?: .* ─+)?$`)
)

// applyJourneyNormalizers applies the journey's own rewrites, which state a known PiG/Pi difference (a numbered divergence or an open finding) and count their hits so a stale entry can be found.
func (c *Run) applyJourneyNormalizers(s string) string {
	for _, n := range c.J.Normalizers {
		if len(n.Kinds) > 0 && !slices.Contains(n.Kinds, c.Kind) {
			continue
		}
		re := regexp.MustCompile(n.Pattern)
		if re.MatchString(s) {
			noteHit(c.J.Name, n)
		}
		s = re.ReplaceAllString(s, n.With)
	}
	return s
}

var headerRow = regexp.MustCompile(`escape interrupt ·|Press ctrl\+o to show full startup help|can explain its own features|^\s*[▀▄█ ]+\s*(v\d[\w.+-]*)?\s*$|^\s*[▀▄█ ]*\s+v\d[\w.+-]*\s*$`)

// stripHeader removes the startup header (PiG's logo and Pi's, D2) from the top of the screen, whole or partly scrolled off, and the blank filler rows between the conversation and the editor. The headers differ in height (D2), so the filler count differs and so does the number of conversation rows that fit; the caller aligns the bottoms.
func stripHeader(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	header := false
	for len(lines) > 0 && (strings.TrimSpace(lines[0]) == "" || headerRow.MatchString(lines[0])) {
		if strings.TrimSpace(lines[0]) != "" {
			header = true
		}
		lines = lines[1:]
	}
	if header {
		lines = append([]string{"<header>"}, lines...)
	}
	// Find the editor's top border: the second border row from the bottom.
	seen := 0
	for i, line := range slices.Backward(lines) {
		if borderRow.MatchString(strings.TrimRight(line, " ")) {
			seen++
			if seen == 2 {
				j := i
				for j > 0 && strings.TrimSpace(lines[j-1]) == "" {
					j--
				}
				lines = append(append([]string{}, lines[:j]...), lines[i:]...)
				break
			}
		}
	}
	return strings.Join(lines, "\n"), header
}

// compareFrames compares the recorded frames of the two products.
func compareFrames(j *Journey, pig, pi *Run) []Failure {
	var out []Failure
	if len(pig.Snaps) != len(pi.Snaps) {
		out = append(out, Failure{Kind: "frame", Msg: fmt.Sprintf("PiG recorded %d frames, Pi %d", len(pig.Snaps), len(pi.Snaps))})
	}
	for i := 0; i < len(pig.Snaps) && i < len(pi.Snaps); i++ {
		a, b := pig.Snaps[i], pi.Snaps[i]
		if a.Name != b.Name {
			out = append(out, Failure{Kind: "frame", Msg: fmt.Sprintf("frame %d: PiG %q, Pi %q", i, a.Name, b.Name)})
			continue
		}
		if skipped(j.SkipFrames, a.Name) {
			continue
		}
		na, ha := pig.normalizeFrameInfo(a.Plain)
		nb, hb := pi.normalizeFrameInfo(b.Plain)
		if ha || hb {
			na, nb = collapseFiller(na), collapseFiller(nb)
			na, nb = alignBottoms(na, nb)
		}
		if d := lineDiff(na, nb); d != "" {
			out = append(out, Failure{Kind: "frame", Msg: fmt.Sprintf("frame %d %q differs (- PiG, + Pi):\n%s", i, a.Name, d)})
		}
	}
	return out
}

// alignBottoms trims the top of the taller of two frames so both have the same number of rows: a taller header leaves fewer conversation rows, and the visible tail of the conversation is what is compared.
func alignBottoms(a, b string) (string, string) {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	// The marker row stands for the header; the conversation rows below it are compared.
	drop := func(l []string) []string {
		if len(l) > 0 && l[0] == "<header>" {
			return l[1:]
		}
		return l
	}
	x, y = drop(x), drop(y)
	n := min(len(x), len(y))
	return strings.Join(x[len(x)-n:], "\n"), strings.Join(y[len(y)-n:], "\n")
}

// collapseFiller shortens every run of three or more blank rows to three. While the startup header is on screen the conversation does not fill the terminal, and the blank rows above the editor or an open selector number differently because the headers differ in height (D2); real spacing between messages is at most two rows.
func collapseFiller(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	run := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			run++
			if run > 3 {
				continue
			}
		} else {
			run = 0
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func skipped(list []string, name string) bool {
	for _, s := range list {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// normalizeRequest renders one provider request in a form that is equal when PiG and Pi send the same conversation: tool-call ids by order, sandbox paths masked, the system prompt (which names the product and its docs, D2/D22) reduced to a marker.
func (c *Run) normalizeRequest(r *Request) []string {
	var body map[string]any
	_ = json.Unmarshal(r.Raw, &body)
	var out []string
	ids := map[string]string{}
	idOf := func(id string) string {
		if v, ok := ids[id]; ok {
			return v
		}
		ids[id] = fmt.Sprintf("call#%d", len(ids)+1)
		return ids[id]
	}
	mask := func(s string) string {
		for _, p := range []struct{ from, to string }{{c.SB.Cwd, "<cwd>"}, {c.SB.Root, "<root>"}, {c.SB.Home, "<home>"}} {
			s = strings.ReplaceAll(s, p.from, p.to)
		}
		for _, re := range compiledShared {
			_ = re
		}
		return regexp.MustCompile(`pi-bash-[0-9a-f]+\.log`).ReplaceAllString(s, "<bash-log>")
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch k {
		case "messages":
		case "tools":
			tools, _ := body[k].([]any)
			for _, t := range tools {
				b, _ := json.Marshal(t)
				out = append(out, "tool "+string(b))
			}
		default:
			b, _ := json.Marshal(body[k])
			out = append(out, fmt.Sprintf("param %s=%s", k, b))
		}
	}
	for i, m := range r.Messages {
		switch m.Role {
		case "system", "developer":
			out = append(out, fmt.Sprintf("msg %d %s <prompt %t>", i, m.Role, strings.TrimSpace(m.Text) != ""))
			continue
		}
		var calls []string
		for _, c := range m.ToolCalls {
			var args any
			if json.Unmarshal([]byte(c.Arguments), &args) == nil {
				b, _ := json.Marshal(args)
				calls = append(calls, fmt.Sprintf("%s %s(%s)", idOf(c.ID), c.Name, mask(string(b))))
			} else {
				calls = append(calls, fmt.Sprintf("%s %s(%s)", idOf(c.ID), c.Name, mask(c.Arguments)))
			}
		}
		line := fmt.Sprintf("msg %d %s", i, m.Role)
		if m.ToolCallID != "" {
			line += " for=" + idOf(m.ToolCallID)
		}
		b, _ := json.Marshal(mask(m.Text))
		line += " text=" + string(b)
		if len(m.ImageURLs) > 0 {
			var sums []string
			for _, u := range m.ImageURLs {
				sums = append(sums, fmt.Sprintf("%d:%s", len(u), u[:min(len(u), 40)]))
			}
			line += " images=" + strings.Join(sums, ",")
		}
		if len(calls) > 0 {
			line += " calls=[" + strings.Join(calls, "; ") + "]"
		}
		out = append(out, c.applyJourneyNormalizers(line))
	}
	return out
}

// compareRequests compares the provider requests of the two products one by one.
func compareRequests(pig, pi *Run) []Failure {
	var out []Failure
	a, b := pig.P.Requests(), pi.P.Requests()
	if len(a) != len(b) {
		out = append(out, Failure{Kind: "request", Msg: fmt.Sprintf("PiG sent %d provider requests, Pi %d", len(a), len(b))})
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		na := strings.Join(pig.normalizeRequest(a[i]), "\n")
		nb := strings.Join(pi.normalizeRequest(b[i]), "\n")
		if d := lineDiff(na, nb); d != "" {
			out = append(out, Failure{Kind: "request", Msg: fmt.Sprintf("provider request %d differs (- PiG, + Pi):\n%s", i+1, d)})
		}
	}
	return out
}

// sessionLines normalizes the session files under the sandbox: entry ids become ordinals, timestamps and machine paths are masked.
func (c *Run) sessionLines() ([]string, error) {
	files := c.SB.SessionFiles()
	sort.Strings(files)
	var out []string
	for fi, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		ids := map[string]string{}
		ordinal := func(id string) string {
			if id == "" {
				return ""
			}
			if v, ok := ids[id]; ok {
				return v
			}
			ids[id] = fmt.Sprintf("e%d", len(ids))
			return ids[id]
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for n := 0; sc.Scan(); n++ {
			var v map[string]any
			if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
				return nil, fmt.Errorf("%s line %d: %w", filepath.Base(f), n+1, err)
			}
			c.normalizeEntry(v, ordinal)
			b, _ := json.Marshal(v)
			out = append(out, fmt.Sprintf("file%d %s", fi, c.applyJourneyNormalizers(string(b))))
		}
	}
	return out, nil
}

// sessionRefKeys are the entry-level fields that hold entry ids, in the order their ordinals are assigned.
var sessionRefKeys = []string{"id", "parentId", "firstKeptEntryId", "fromId", "targetId"}

func (c *Run) normalizeEntry(v map[string]any, ordinal func(string) string) {
	isHeader := v["type"] == "session"
	for _, k := range sessionRefKeys {
		if x, ok := v[k].(string); ok {
			if isHeader && k == "id" {
				v[k] = "<session-id>"
			} else {
				v[k] = ordinal(x)
			}
		}
	}
	c.normalizeValue(v)
}

// normalizeValue masks machine paths and removes wall-clock fields throughout an entry; tool-call ids inside content blocks come from the script and are compared as they are.
func (c *Run) normalizeValue(x any) {
	switch t := x.(type) {
	case map[string]any:
		delete(t, "timestamp")
		delete(t, "durationMs") // wall time of the provider call (Pi agent-session.ts assistant message metadata)
		if t["role"] == "system" {
			// The system message names the product and its docs directory (D2/D22); only the structure is compared.
			if sec, ok := t["sections"].(map[string]any); ok {
				sec["docs"] = "<docs>"
				sec["preamble"] = "<preamble>"
			}
		}
		for k, val := range t {
			if s, ok := val.(string); ok {
				t[k] = c.maskPaths(s)
			} else {
				c.normalizeValue(val)
			}
		}
	case []any:
		for i, val := range t {
			if s, ok := val.(string); ok {
				t[i] = c.maskPaths(s)
			} else {
				c.normalizeValue(val)
			}
		}
	}
}

func (c *Run) maskPaths(s string) string {
	for _, p := range []struct{ from, to string }{{c.SB.Cwd, "<cwd>"}, {c.SB.Root, "<root>"}, {c.SB.Home, "<home>"}} {
		s = strings.ReplaceAll(s, p.from, p.to)
	}
	s = c.maskSessionIdentity(s)
	return regexp.MustCompile(`pi-bash-[0-9a-f]+\.log`).ReplaceAllString(s, "<bash-log>")
}

var sessionStem = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z_([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// maskSessionIdentity replaces session file names and ids, which carry a clock reading and a random id, by the session's position in creation order, so a wrong parent or a wrong resumed session still shows.
func (c *Run) maskSessionIdentity(s string) string {
	files := c.SB.SessionFiles()
	sort.Strings(files)
	ordinal := map[string]string{}
	for i, f := range files {
		if m := sessionStem.FindStringSubmatch(filepath.Base(f)); m != nil {
			ordinal[m[1]] = fmt.Sprintf("<session #%d>", i)
		}
	}
	s = sessionStem.ReplaceAllStringFunc(s, func(stem string) string {
		if id := sessionStem.FindStringSubmatch(stem)[1]; ordinal[id] != "" {
			return ordinal[id]
		}
		return "<session ?>"
	})
	for id, name := range ordinal {
		s = strings.ReplaceAll(s, id, name)
	}
	return s
}

// compareSessions compares the persisted session files.
func compareSessions(pig, pi *Run) []Failure {
	a, err := pig.sessionLines()
	if err != nil {
		return []Failure{{Kind: "session", Msg: "PiG session: " + err.Error()}}
	}
	b, err := pi.sessionLines()
	if err != nil {
		return []Failure{{Kind: "session", Msg: "Pi session: " + err.Error()}}
	}
	if d := lineDiff(strings.Join(a, "\n"), strings.Join(b, "\n")); d != "" {
		return []Failure{{Kind: "session", Msg: "session files differ (- PiG, + Pi):\n" + d}}
	}
	return nil
}

var (
	hitMu sync.Mutex
	hits  = map[string]int{}
)

func hitKey(journey string, n Normalizer) string { return journey + "\x00" + n.Pattern }

func noteHit(journey string, n Normalizer) {
	hitMu.Lock()
	hits[hitKey(journey, n)]++
	hitMu.Unlock()
}

// StaleNormalizers returns the journey normalizers that never matched in this process: a known difference that no longer occurs must be deleted.
func StaleNormalizers(js []*Journey) []string {
	hitMu.Lock()
	defer hitMu.Unlock()
	var out []string
	for _, j := range js {
		for _, n := range j.Normalizers {
			if hits[hitKey(j.Name, n)] == 0 {
				out = append(out, fmt.Sprintf("%s: %q (%s)", j.Name, n.Pattern, n.Reason))
			}
		}
	}
	return out
}

// compareAgentFiles compares the final JSON content of the named agent-directory files.
func compareAgentFiles(j *Journey, pig, pi *Run) []Failure {
	var out []Failure
	for _, rel := range j.AgentFiles {
		read := func(r *Run) string {
			b, err := os.ReadFile(filepath.Join(r.SB.Agent, rel))
			if err != nil {
				return "<missing: " + err.Error() + ">"
			}
			var v any
			if json.Unmarshal(b, &v) != nil {
				return pig.maskPaths(string(b))
			}
			r.normalizeValue(v)
			enc, _ := json.MarshalIndent(v, "", " ")
			return r.applyJourneyNormalizers(string(enc))
		}
		if d := lineDiff(read(pig), read(pi)); d != "" {
			out = append(out, Failure{Kind: "file", Msg: fmt.Sprintf("agent file %s differs (- PiG, + Pi):\n%s", rel, d)})
		}
	}
	return out
}
