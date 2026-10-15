package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type bugFilesCase struct {
	Session    string  `json:"session"`
	Crashes    *string `json:"crashes"`
	JSONL      *string `json:"jsonl"`
	Summary    *string `json:"summary"`
	HasSummary bool    `json:"hasSummary"`
}

type bugFilesOut struct {
	Names       []string `json:"names"`
	Diagnostics string   `json:"diagnostics"`
	JSONL       *string  `json:"jsonl"`
	Summary     *string  `json:"summary"`
}

// TestBugReportFilesMatchPi runs core/bug-report.ts collectBugReportDiagnostics and bugReportFiles against a SessionManager opened on the case's session file and
// the crash log read from the case's file, and PiG's CollectBugReportDiagnostics and BugReportFiles over the same files. Sessions hold user, assistant and tool-result
// messages, other entry types, assistant turns that stopped, errored or were aborted, with or without diagnostics, an error message, a raw stop reason, and details objects with integer-like keys in any order; the crash log (a JSON array) holds records with and without notified, malformed records and non-array roots. The bytes of diagnostics.json, the member list, and the session.jsonl
// and summary.md members (an empty string is present; a summary gets one trailing newline) must match. Not compared: an empty errorMessage or rawStopReason (PiG's AssistantMessage cannot tell "" from absent, Pi keeps the empty string), the insertion order of non-index details keys (PiG decodes details into a map), report.json (covered by the metadata tests) and the zip container.
func TestBugReportFilesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261124))
	pick := func(xs ...string) string { return xs[rng.Intn(len(xs))] }
	jsonString := func(s string) string { data, _ := json.Marshal(s); return string(data) }
	ts := func() string { return fmt.Sprintf("2026-01-0%dT0%d:00:00.000Z", 1+rng.Intn(9), rng.Intn(9)) }
	details := func() string {
		// PiG decodes details into a Go map, so the order of its non-index keys is lexicographic, not insertion order: the keys here are generated in that order (see the doc comment).
		keys := []string{"a", "b", "z", "é", "0", "2", "10"}
		var picked []string
		for _, key := range keys {
			if rng.Intn(3) == 0 {
				picked = append(picked, key)
			}
		}
		rng.Shuffle(len(picked), func(i, j int) {
			if _, ok := strconv.Atoi(picked[i]); ok == nil {
				if _, ok := strconv.Atoi(picked[j]); ok == nil {
					picked[i], picked[j] = picked[j], picked[i]
				}
			}
		})
		var members []string
		for _, key := range picked {
			value := pick(`1`, `1.5`, `null`, `"s"`, `[1,"x"]`, `{"a":1,"b":2}`, `true`, `1e21`, `-0`)
			members = append(members, jsonString(key)+":"+value)
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	diagnostic := func() string {
		parts := []string{`"type":` + jsonString(pick("retry", "fallback", "ws_closed")), `"timestamp":` + pick("1700000000000", "0", "1.5e12")}
		if rng.Intn(2) == 0 {
			e := `"message":` + jsonString(pick("boom", "", "a\u2028b <x>"))
			if rng.Intn(2) == 0 {
				e = `"name":"Error",` + e
			}
			if rng.Intn(2) == 0 {
				e += `,"stack":"at x"`
			}
			switch rng.Intn(3) {
			case 0:
				e += `,"code":"ECONN"`
			case 1:
				e += `,"code":429`
			}
			parts = append(parts, `"error":{`+e+`}`)
		}
		if rng.Intn(2) == 0 {
			parts = append(parts, `"details":`+details())
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	var cases []bugFilesCase
	for range 500 {
		var lines []string
		lines = append(lines, `{"type":"session","version":3,"id":"sess-`+fmt.Sprint(rng.Intn(1000))+`","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/x"}`)
		parent := "null"
		for i := range rng.Intn(8) {
			id := fmt.Sprintf("e%07d", i+1)
			var body string
			switch rng.Intn(6) {
			case 0:
				body = `"type":"message","message":{"role":"user","content":"hi","timestamp":1}`
			case 1:
				body = `"type":"thinking_level_change","thinkingLevel":"high"`
			case 2:
				body = `"type":"message","message":{"role":"toolResult","toolCallId":"c","toolName":"read","content":[{"type":"text","text":"x"}],"isError":false,"timestamp":1}`
			default:
				msg := `"role":"assistant","content":[{"type":"text","text":"ok"}],"api":` + jsonString(pick("openai-responses", "anthropic-messages")) +
					`,"provider":` + jsonString(pick("openai", "anthropic")) + `,"model":` + jsonString(pick("m1", "m2")) +
					`,"usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}` +
					`,"stopReason":` + jsonString(pick("stop", "stop", "length", "toolUse", "error", "aborted")) + `,"timestamp":1`
				if rng.Intn(4) == 0 {
					msg += `,"errorMessage":` + jsonString(pick("failed", "rate <limit>", "a\u2028b"))
				}
				if rng.Intn(4) == 0 {
					msg += `,"rawStopReason":` + jsonString(pick("end_turn", "max_tokens"))
				}
				if rng.Intn(3) == 0 {
					var ds []string
					for range rng.Intn(3) {
						ds = append(ds, diagnostic())
					}
					msg += `,"diagnostics":[` + strings.Join(ds, ",") + `]`
				}
				body = `"type":"message","message":{` + msg + `}`
			}
			lines = append(lines, `{`+body+`,"id":"`+id+`","parentId":`+parent+`,"timestamp":"`+ts()+`"}`)
			parent = `"` + id + `"`
		}
		c := bugFilesCase{Session: strings.Join(lines, "\n") + "\n"}
		if rng.Intn(2) == 0 {
			var records []string
			for range 1 + rng.Intn(3) {
				r := `{"timestamp":"` + ts() + `","version":"1.1.0","kind":` + jsonString(pick("uncaught_exception", "fatal_error")) + `,"message":` + jsonString(pick("m", "a\nb")) + `,"stack":` + pick("null", `"s"`) + `,"sessionFile":` + pick("null", `"/x.jsonl"`) + `,"cwd":"/tmp"`
				if rng.Intn(2) == 0 {
					r += `,"notified":` + pick("true", "false")
				}
				records = append(records, r+"}")
			}
			switch rng.Intn(8) {
			case 0:
				records = append(records, `5`, `null`, `{"timestamp":1,"message":"x"}`, `{"timestamp":"t"}`)
			case 1:
				c.Crashes = new(pick("{}", "null", "[", `"s"`))
			}
			if c.Crashes == nil {
				c.Crashes = new("[" + strings.Join(records, ",\n") + "]\n")
			}
		}
		if rng.Intn(2) == 0 {
			c.JSONL = new(pick("", "{\"a\":1}\n", "x"))
		}
		if rng.Intn(2) == 0 {
			c.HasSummary = true
			c.Summary = new(pick("", "text", "text\n", "a\n\n", "é\r\n"))
		}
		cases = append(cases, c)
	}

	var want []bugFilesOut
	pioracle.Run(t, `
const { collectBugReportDiagnostics, bugReportFiles } = await load("pi-coding-agent/core/bug-report.js");
const { SessionManager } = await load("pi-coding-agent/core/session-manager.js");
const { readCrashLog } = await load("pi-coding-agent/core/crash-log.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "bugf-"));
	const sessionFile = path.join(root, "s.jsonl");
	fs.writeFileSync(sessionFile, c.session);
	const crashFile = path.join(root, "crashes.jsonl");
	if (c.crashes !== null) fs.writeFileSync(crashFile, c.crashes);
	const sm = SessionManager.open(sessionFile, root);
	const diagnostics = collectBugReportDiagnostics(sm, readCrashLog(crashFile));
	const files = bugReportFiles({ metadata: {}, diagnostics, ...(c.jsonl === null ? {} : { sessionJsonl: c.jsonl }), ...(c.hasSummary ? { summary: c.summary } : {}) });
	const by = Object.fromEntries(files.map((f) => [f.name, f.data]));
	out.push({ names: files.map((f) => f.name), diagnostics: by["diagnostics.json"], jsonl: by["session.jsonl"] ?? null, summary: by["summary.md"] ?? null });
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, cases, &want)

	failures := 0
	for i, c := range cases {
		root := t.TempDir()
		sessionFile := filepath.Join(root, "s.jsonl")
		if err := os.WriteFile(sessionFile, []byte(c.Session), 0o600); err != nil {
			t.Fatal(err)
		}
		crashFile := filepath.Join(root, "crashes.jsonl")
		if c.Crashes != nil {
			if err := os.WriteFile(crashFile, []byte(*c.Crashes), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		session, err := NewSessionManagerWithDir("/tmp/x", root).Load(sessionFile)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		bundle := BugReportBundle{Diagnostics: CollectBugReportDiagnostics(session.ID(), session.GetEntries(), ReadCrashLog(crashFile)), SessionJSONL: c.JSONL}
		if c.HasSummary {
			bundle.Summary = c.Summary
		}
		files, err := BugReportFiles(bundle)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		var got bugFilesOut
		for _, f := range files {
			got.Names = append(got.Names, f.Name)
			switch f.Name {
			case "diagnostics.json":
				got.Diagnostics = string(f.Data)
			case "session.jsonl":
				got.JSONL = new(string(f.Data))
			case "summary.md":
				got.Summary = new(string(f.Data))
			}
		}
		if !reflect.DeepEqual(got, want[i]) {
			failures++
			if failures <= 4 {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want[i])
				t.Errorf("case %d\n session %s\n crashes %v\n Go %.2500s\n Pi %.2500s", i, c.Session, c.Crashes, gotJSON, wantJSON)
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
