package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestConformance_SessionAndSkillHelpersAgreeAcrossSDKs: Pi exports CURRENT_SESSION_VERSION, VIRTUAL_MODEL_STATE_ENTRY, getLatestCompactionEntry, parseSkillBlock and
// formatSkillsForPrompt (coding-agent index.ts; core/session-manager.ts:41,372, core/virtual-models.ts:32, core/agent-session.ts:177, core/skills.ts:358). The Node SDK
// runs Pi's own code, so its answer is the reference: every case runs in each SDK and must equal it.
func TestConformance_SessionAndSkillHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	type tcase = map[string]any
	block := func(name, location, content, tail string) string {
		return `<skill name="` + name + `" location="` + location + "\">\n" + content + "\n</skill>" + tail
	}
	skill := func(name, description, path string, disabled bool) tcase {
		return tcase{"name": name, "description": description, "filePath": path, "baseDir": "/b", "sourceInfo": tcase{"path": path, "source": "local", "scope": "user", "origin": "top-level"}, "disableModelInvocation": disabled}
	}
	compaction := func(summary string) tcase {
		return tcase{"type": "compaction", "summary": summary, "firstKeptEntryId": "a", "tokensBefore": 1}
	}
	message := tcase{"type": "message", "message": tcase{"role": "user", "content": "hi"}}
	cases := []tcase{
		{"fn": "consts"},
		{"fn": "latest", "entries": []any{}}, {"fn": "latest", "entries": []any{message}}, {"fn": "latest", "entries": []any{compaction("one")}},
		{"fn": "latest", "entries": []any{compaction("one"), message, compaction("two"), message}}, {"fn": "latest", "entries": []any{message, tcase{"type": 5}, tcase{"type": nil}, tcase{}, compaction("last")}},
		{"fn": "latest", "entries": []any{compaction("a"), compaction("b"), compaction("c")}},
	}
	for _, text := range []string{
		block("a", "b", "content", ""), block("a", "b", "content", "\n\nhi there"), block("a", "b", "content", "\n\n  hi there \n "), block("a", "b", "content", "\n\n"), block("a", "b", "content", "\n\n \n\t "),
		block("a", "b", "content", "\n"), block("a", "b", "content", "\n\n\ufeffbom \u00a0"), block("a", "b", "content", "\n\n\u0085nel\u0085"), block("a", "b", "content", "\n\n\u2003em\u3000"), block("a", "b", "content", "\n\n\u200bzw\u200b"),
		block("a", "b", "content", "\n\n\u2028line\u2029"), block("a", "b", "content", "\nx"), block("a", "b", "content", "x"), block("a", "b", "", ""), block("a", "b", "", "\n\nuser"),
		block("a", "b", "line1\nline2\n", ""), block("a", "b", "line1\r\nline2", "\r\n\r\nuser"), block("a", "b", "x\n</skill>\n\nmore", ""), block("a", "b", "x\n</skill>\n\nmore", "\n\ntail"),
		block("a", "b", "x\n</skill>y", "\n\nz"), block("a", "b", "x\n</skill>\n", "\n\nz"), block("a", "b", "x\n</skill>\n\n", ""), block("a", "b", "x\n</skill>\n\n", "\n\nuser"),
		block("", "b", "c", ""), block("a", "", "c", ""), block("na\nme", "lo\ncation", "c", ""), block("a b", "/p/q r", "c", ""), block("日本", "😀", "é", "\n\n😀"), block("a>", "b>", "c", ""),
		` <skill name="a" location="b">` + "\nc\n</skill>", `<skill name="a" location="b"` + ">\nc\n</skill>x", `<skill name="a"  location="b">` + "\nc\n</skill>", `<skill name="a" location="b">c` + "\n</skill>",
		`<skill name="a" location="b">` + "\nc\n</skill", `<skill name="a" location="b">` + "\nc\n</skill>\n\n\n\nx", `<skill name=a location=b>` + "\nc\n</skill>", "", "<skill", `<skill name="a`, `<skill name="a" location="b`,
		"prefix " + block("a", "b", "c", ""), block("a", "b", "c", "") + "\n\nuser\n\nmore\n\n",
	} {
		cases = append(cases, tcase{"fn": "parse", "text": text})
	}
	skills := []any{
		skill("a", "desc", "/p/a/SKILL.md", false), skill("b", "d2", "/p/b/SKILL.md", true), skill("c&d", `<tag> "quoted" 'single' & more`, `/p/c&d/"x"/SKILL.md`, false),
		skill("", "", "", false), skill("日本", "😀 é", "/日本/SKILL.md", false), skill("a", "multi\nline\ttab", "p", false),
	}
	for _, tool := range []any{nil, "read", "bash", "indirect", "other", "", "READ"} {
		for _, list := range [][]any{{}, skills[1:2], skills[:1], skills, skills[2:3], skills[3:5], {skills[1], skills[1]}} {
			c := tcase{"fn": "format", "skills": list}
			if tool != nil {
				c["tool"] = tool
			}
			cases = append(cases, c)
		}
	}

	run := func(t *testing.T, h *harness, c tcase) map[string]any {
		t.Helper()
		args, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "skills_probe", string(args)) {
			t.Fatal("fixture has no skills_probe command")
		}
		var reported string
		pollUntilConformance(t, "skills_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "skills:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got
	}

	var reference []map[string]any
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node" {
			continue
		}
		h := tc.make(t)
		for _, c := range cases {
			reference = append(reference, run(t, h, c))
		}
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}
	if len(reference) != len(cases) {
		t.Fatalf("the Node reference ran %d of %d cases", len(reference), len(cases))
	}
	// Values Pi's source implies, so a mistake shared by every SDK cannot pass.
	if want := (tcase{"version": 3.0, "virtual": "pi.virtual-model-state"}); !reflect.DeepEqual(map[string]any(reference[0]), map[string]any(want)) {
		t.Fatalf("reference constants = %v", reference[0])
	}
	if entry, _ := reference[4]["entry"].(map[string]any); entry == nil || entry["summary"] != "two" {
		t.Fatalf("reference getLatestCompactionEntry picked %v, want the last compaction", reference[4])
	}
	if parsed, _ := reference[11]["block"].(map[string]any); parsed == nil || parsed["userMessage"] != nil {
		t.Fatalf("reference parseSkillBlock of a block followed by white space only = %v, want no user message", reference[11])
	}
	if parsed, _ := reference[12]["block"].(map[string]any); parsed != nil {
		t.Fatalf("reference parseSkillBlock of a block followed by one newline = %v, want null", reference[12])
	}
	if parsed, _ := reference[10]["block"].(map[string]any); parsed != nil {
		t.Fatalf("reference parseSkillBlock of a block followed by two newlines and nothing = %v, want null", reference[10])
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for i, c := range cases {
				if got := run(t, h, c); !reflect.DeepEqual(got, reference[i]) {
					args, _ := json.Marshal(c)
					t.Errorf("case %d %.160s: got %v, Node (Pi) reports %v", i, args, got, reference[i])
				}
			}
		})
	}
}
