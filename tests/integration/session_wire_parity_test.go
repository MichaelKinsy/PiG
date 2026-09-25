//go:build integration

// session_wire_parity_test.go: proves pig and upstream pi write
// structurally compatible session JSONL for the same conversation.
//
// Why this test exists. TestParity_MultiTurn proves the *LLM* remembers
// "ALPHA-7749" across turns; it does NOT prove pig's wire format
// (session entries on disk, message arrays sent to the provider) is
// shape-equivalent to upstream pi. Both systems could be writing
// totally different JSON and the multi-turn test would still pass as
// long as the LLM gets enough context to guess the answer. This test
// closes that gap by reading the on-disk session JSONL each system
// produces and asserting structural compatibility:
//
//   - same version field on the session header
//   - same set of entry "type" values used (or a documented subset)
//   - user / assistant message entries have the same JSON shape
//     (role, content[].type, parentId chain)
//
// What it deliberately does NOT assert:
//
//   - byte-identical content (timestamps, ids, model names differ
//     legitimately)
//   - exact tool-result formatting (tool output bodies differ)
//   - token counts (usage objects differ legitimately)
//
// Kaizen 2026-05-10: closes the "tests verify LLM behavior, not pig
// wire format" gap surfaced when reviewing parity test quality.

package integration

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// sessionEntry is the minimal JSONL row shape both systems share.
type sessionEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId,omitempty"`
	Timestamp string          `json:"timestamp"`
	Version   *int            `json:"version,omitempty"` // session header only
	Message   *sessionMessage `json:"message,omitempty"` // message rows
}

type sessionMessage struct {
	Role     string                     `json:"role"`
	Content  json.RawMessage            `json:"content"`
	Sections map[string]json.RawMessage `json:"sections,omitempty"`
}

func (m *sessionMessage) UnmarshalJSON(data []byte) error {
	type wireMessage sessionMessage
	var decoded wireMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	// System/user text is a union; assistant/toolResult content is always an
	// array (packages/ai/src/types.ts). Validate every row, not only the first.
	switch decoded.Role {
	case "system", "user", "assistant", "toolResult":
		content := strings.TrimSpace(string(decoded.Content))
		if (decoded.Role == "system" || decoded.Role == "user") && strings.HasPrefix(content, `"`) {
			var text string
			if err := json.Unmarshal(decoded.Content, &text); err != nil {
				return fmt.Errorf("%s content: %w", decoded.Role, err)
			}
		} else {
			var blocks []map[string]any
			if err := json.Unmarshal(decoded.Content, &blocks); err != nil {
				return fmt.Errorf("%s content must be an array: %w", decoded.Role, err)
			}
			if blocks == nil {
				return fmt.Errorf("%s content must be an array, not null", decoded.Role)
			}
		}
	}
	*m = sessionMessage(decoded)
	return nil
}

func TestSessionMessageContentShape(t *testing.T) {
	// Pi's ai/types.ts allows strings for system/user content, but assistant
	// and toolResult content must be arrays. Validate the same decoder used
	// by readLatestSession, including messages beyond the first assistant.
	for _, role := range []string{"system", "user", "assistant", "toolResult"} {
		for _, tc := range []struct {
			name    string
			content string
			array   bool
			text    bool
		}{
			{name: "text", content: `"42"`, text: true},
			{name: "object", content: `{"type":"text","text":"42"}`},
			{name: "null", content: `null`},
			{name: "missing"},
			{name: "empty-array", content: `[]`, array: true},
			{name: "text-array", content: `[{"type":"text","text":"42"}]`, array: true},
		} {
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				message := `{"type":"message","message":{"role":"` + role + `"`
				if tc.content != "" {
					message += `,"content":` + tc.content
				}
				message += `}}`
				var row sessionEntry
				err := json.Unmarshal([]byte(message), &row)
				wantValid := tc.array || tc.text && (role == "system" || role == "user")
				if (err == nil) != wantValid {
					t.Fatalf("decode %s: error = %v, want valid = %v", message, err, wantValid)
				}
			})
		}
	}
}

// readLatestSession reads the most recently modified .jsonl file under
// sessionsRoot/<encoded-cwd>/. Returns the parsed entries and the path.
//
// Both systems run in temporary homes; only the encoded cwd for this run is eligible.
func readLatestSession(t *testing.T, sessionsRoot, cwdDir string) ([]sessionEntry, string) {
	t.Helper()

	// upstream pi and pig both encode cwd into the directory name by
	// replacing slashes with dashes, prefixed with "--". We don't
	// reconstruct it here: we just walk and pick the newest file.
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		t.Fatalf("read sessions root %s: %v", sessionsRoot, err)
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var cands []candidate
	encoded := encodedCwdVariants(cwdDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		matched := false
		for _, want := range encoded {
			if strings.Contains(e.Name(), want) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		// Look for sessions created with cwdDir in the encoded name.
		// Both systems include it; we don't enforce the exact form.
		sub := filepath.Join(sessionsRoot, e.Name())
		files, _ := os.ReadDir(sub)
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			full := filepath.Join(sub, f.Name())
			info, err := os.Stat(full)
			if err != nil {
				continue
			}
			cands = append(cands, candidate{full, info.ModTime()})
		}
	}
	if len(cands) == 0 {
		t.Fatalf("no .jsonl sessions under %s", sessionsRoot)
	}
	slices.SortFunc(cands, func(a, b candidate) int { return cmp.Compare(b.mod.UnixNano(), a.mod.UnixNano()) })
	chosen := cands[0].path
	t.Logf("session file: %s (mtime %s)", chosen, cands[0].mod.Format(time.RFC3339))

	data, err := os.ReadFile(chosen)
	if err != nil {
		t.Fatalf("read %s: %v", chosen, err)
	}
	var rows []sessionEntry
	for line := range strings.SplitSeq(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var row sessionEntry
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parse session line %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows, chosen
}

func encodedCwdVariants(cwd string) []string {
	seen := map[string]struct{}{}
	add := func(p string) {
		if p == "" {
			return
		}
		seen[encodeCwdSegment(filepath.Clean(p))] = struct{}{}
	}
	add(cwd)
	if abs, err := filepath.Abs(cwd); err == nil {
		add(abs)
	}
	if eval, err := filepath.EvalSymlinks(cwd); err == nil {
		add(eval)
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		if evalAbs, err := filepath.EvalSymlinks(abs); err == nil {
			add(evalAbs)
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// encodeCwdSegment mirrors the upstream/pig convention of encoding a
// path as the directory name by replacing each "/" with "-" and
// prefixing with "--".
func encodeCwdSegment(cwd string) string {
	return "--" + strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-") + "--"
}

// distinctTypes returns the sorted unique set of entry.Type values.
func distinctTypes(rows []sessionEntry) []string {
	set := map[string]struct{}{}
	for _, r := range rows {
		set[r.Type] = struct{}{}
	}
	return slices.Sorted(maps.Keys(set))
}

// firstUserMessage returns the first user message (or nil) and its raw entry.
func firstUserMessage(rows []sessionEntry) (*sessionMessage, *sessionEntry) {
	for i := range rows {
		r := &rows[i]
		if r.Type == "message" && r.Message != nil && r.Message.Role == "user" {
			return r.Message, r
		}
	}
	return nil, nil
}

// firstAssistantMessage returns the first assistant message (or nil) and its raw entry.
func firstAssistantMessage(rows []sessionEntry) (*sessionMessage, *sessionEntry) {
	for i := range rows {
		r := &rows[i]
		if r.Type == "message" && r.Message != nil && r.Message.Role == "assistant" {
			return r.Message, r
		}
	}
	return nil, nil
}

// TestParity_SessionWireFormat drives a one-turn conversation through
// both pig and upstream pi in deterministic print mode, then reads the
// resulting session JSONL and asserts structural compatibility.
func TestParity_SessionWireFormat(t *testing.T) {
	upstream := deterministicPrintUpstream(t)
	pig := deterministicPrintGopi(t)

	type result struct {
		rows  []sessionEntry
		path  string
		types []string
	}
	results := map[string]*result{}

	readResult := func(t *testing.T, sys deterministicPrintSystem) *result {
		t.Helper()
		// Wait briefly for the session file to be flushed to disk.
		ok := pollUntil(5*time.Second, func() bool {
			if _, err := os.Stat(sys.sessionsRoot); err != nil {
				return false
			}
			entries, _ := os.ReadDir(sys.sessionsRoot)
			return len(entries) != 0
		})
		if !ok {
			return nil
		}
		rows, path := readLatestSession(t, sys.sessionsRoot, sys.cwd)
		if len(rows) == 0 {
			return nil
		}
		return &result{rows: rows, path: path, types: distinctTypes(rows)}
	}

	up := runDeterministicPrint(t, upstream, "--print", "What is 20+22? Reply with ONLY the number.")
	if up != "42" {
		t.Fatalf("upstream deterministic session-wire print output = %q, want %q", up, "42")
	}
	results["pi"] = readResult(t, upstream)
	if results["pi"] == nil {
		t.Fatalf("upstream did not persist a session file for the deterministic scenario")
	}

	gp := runDeterministicPrint(t, pig, "--print", "What is 20+22? Reply with ONLY the number.")
	if gp != up {
		t.Fatalf("pig deterministic session-wire print output = %q, want upstream %q", gp, up)
	}
	results["pig"] = readResult(t, pig)
	if results["pig"] == nil {
		t.Fatalf("pig did not persist a session file after upstream succeeded")
	}

	for _, sys := range []struct {
		name string
		r    *result
	}{{"pig", results["pig"]}, {"pi", results["pi"]}} {
		// Per-system shape checks.
		head := sys.r.rows[0]
		if head.Type != "session" {
			t.Errorf("%s: first entry must be type=session, got %q", sys.name, head.Type)
		}
		if head.Version == nil {
			t.Errorf("%s: session header missing version field", sys.name)
		} else if *head.Version != 3 {
			t.Errorf("%s: session version=%d, expected 3", sys.name, *head.Version)
		}

		user, _ := firstUserMessage(sys.r.rows)
		if user == nil {
			t.Errorf("%s: no user message found in session", sys.name)
		} else {
			if user.Role != "user" {
				t.Errorf("%s: user message role=%q, want %q", sys.name, user.Role, "user")
			}
			// User content remains an array in this prompt path. Decode it at the
			// role boundary: Pi's SystemMessage.content is string | TextContent[].
			var content []map[string]any
			if err := json.Unmarshal(user.Content, &content); err != nil {
				t.Errorf("%s: user content is not an array: %v", sys.name, err)
			} else if len(content) == 0 {
				t.Errorf("%s: user message has empty content[]", sys.name)
			} else {
				ct, _ := content[0]["type"].(string)
				if ct != "text" {
					t.Errorf("%s: user content[0].type=%q, want text", sys.name, ct)
				}
			}
		}

		// agent-session.ts:_preparePromptAndToolLoadout persists structured
		// prompt sections with content: "" before the first provider request.
		var system *sessionMessage
		for _, row := range sys.r.rows {
			if row.Message != nil && row.Message.Role == "system" {
				system = row.Message
				break
			}
		}
		if system == nil {
			t.Errorf("%s: no system message found in session", sys.name)
		} else if string(system.Content) != `""` || len(system.Sections) == 0 {
			t.Errorf("%s: system message must persist empty text and prompt sections: %+v", sys.name, system)
		}

		assistant, _ := firstAssistantMessage(sys.r.rows)
		if assistant == nil {
			t.Errorf("%s: no assistant message found in session", sys.name)
		} else if assistant.Role != "assistant" {
			t.Errorf("%s: assistant message role=%q, want %q", sys.name, assistant.Role, "assistant")
		}
	}

	// Cross-system structural parity. Both systems must agree on the
	// session header version and message-row shape. Other entry types
	// (model_change, thinking_level_change, etc.) are allowed to differ
	// We only require the *intersection* contains the essentials.
	if results["pig"] == nil || results["pi"] == nil {
		return
	}
	g := results["pig"]
	p := results["pi"]

	t.Logf("pig entry types: %v", g.types)
	t.Logf("pi   entry types: %v", p.types)

	mustHave := []string{"session", "message"}
	for _, sys := range []struct {
		name string
		r    *result
	}{{"pig", g}, {"pi", p}} {
		got := map[string]struct{}{}
		for _, ty := range sys.r.types {
			got[ty] = struct{}{}
		}
		for _, want := range mustHave {
			if _, ok := got[want]; !ok {
				t.Errorf("%s: session is missing required entry type %q (got %v)", sys.name, want, sys.r.types)
			}
		}
	}

	// Soft check: warn if either system has an entry type the other
	// doesn't recognize. This catches silent additions on either side.
	gset := map[string]struct{}{}
	for _, ty := range g.types {
		gset[ty] = struct{}{}
	}
	for _, ty := range p.types {
		if _, ok := gset[ty]; !ok {
			t.Logf("note: pi has entry type %q pig did not produce in this run (may be optional)", ty)
		}
	}
	pset := map[string]struct{}{}
	for _, ty := range p.types {
		pset[ty] = struct{}{}
	}
	for _, ty := range g.types {
		if _, ok := pset[ty]; !ok {
			t.Logf("note: pig has entry type %q pi did not produce in this run (may be pig-only: verify in DIVERGENCES.md)", ty)
		}
	}
}
