// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// blob builds deterministic base64 text of exactly n characters without a committed payload.
func blob(n int) string {
	var out strings.Builder
	sum := sha256.Sum256([]byte("fixtureguard"))
	for out.Len() < n {
		sum = sha256.Sum256(sum[:])
		out.WriteString(base64.StdEncoding.EncodeToString(sum[:]))
	}
	return strings.NewReplacer("=", "A", "+", "Q", "/", "z").Replace(out.String()[:n])
}

// words returns n distinct letter-only words.
func words(n int) []string {
	out := make([]string, n)
	for i := range out {
		var w []byte
		for v := i; ; v /= 26 {
			w = append(w, byte('a'+v%26))
			if v < 26 {
				break
			}
		}
		out[i] = "w" + string(w) + "x"
	}
	return out
}

func codes(found []finding) []string {
	var out []string
	for _, f := range found {
		out = append(out, f.code)
	}
	return out
}

func TestScanTextRejectsCapturedProviderShapes(t *testing.T) {
	reasoning := &strings.Builder{}
	all := words(120)
	for i := 0; i+3 <= len(all); i += 3 {
		reasoning.WriteString(`data: {"choices":[{"delta":{"reasoning_text":"` + strings.Join(all[i:i+3], " ") + ` "}}]}` + "\n")
	}
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"copilot opaque reasoning blob", `{"choices":[{"delta":{"reasoning_opaque":"` + blob(25000) + `"}}]}`, codeOpaque},
		{"openai encrypted content in an escaped body", `{"body":"{\"encrypted_content\":\"` + blob(900) + `\"}"}`, codeOpaque},
		{"anthropic thinking signature", `{"type":"thinking","thinking":"x","signature":"` + blob(300) + `"}`, codeOpaque},
		{"yaml style signature", "thinking_signature: '" + blob(120) + "'\n", codeOpaque},
		{"blob under an unknown key", `{"payload":"` + blob(opaqueBlobMin+10) + `"}`, codeOpaque},
		{"copilot usage row", `data: {"copilot_usage":{"total_nano_aiu":1}}`, codeBilling},
		{"billing batch cost", `{"token_details":[{"cost_per_batch":1,"batch_size":1}]}`, codeBilling},
		{"github request id header", `x-github-request-id: 9A1C:3F5B72:1C8E4D:6522A7B1`, codeBilling},
		{"uuid request id header", `"x-request-id": "3f2b8c1e-7d4a-4e6b-9a15-c0d8e2f41b37"`, codeBilling},
		{"chat completion id", `data: {"id":"chatcmpl-` + blob(29) + `"}`, codeProvider},
		{"anthropic message id", `{"id":"msg_01` + blob(22) + `"}`, codeProvider},
		{"captured created epoch", `{"created":1761834127}`, codeProvider},
		{"captured created timestamp", `{"created_at":"2026-10-30T14:22:07.381Z"}`, codeProvider},
		{"posix home path", `{"cwd":"/Users/jdoe/work/app"}`, codeHomePath},
		{"linux home path", `at /home/realperson/.cache/x`, codeHomePath},
		{"windows home path", `C:\\Users\\JDoe\\app`, codeHomePath},
		{"reasoning prose across deltas", reasoning.String(), codeReasoning},
	}
	for _, tc := range cases {
		found := scanText("testdata/x.jsonl", []byte(tc.content), true)
		if !slices.Contains(codes(found), tc.want) {
			t.Errorf("%s: findings %v, want %s", tc.name, found, tc.want)
		}
		for _, f := range found {
			if strings.Contains(f.message, "jdoe") || strings.Contains(f.message, "realperson") {
				t.Errorf("%s: message echoes content: %q", tc.name, f.message)
			}
		}
	}
}

func TestScanTextAcceptsSyntheticFixtures(t *testing.T) {
	repeated := strings.Repeat(`data: {"delta":{"thinking":"one"}}`+"\n", 5000)
	cases := map[string]string{
		"short signature stand-in":       `{"type":"thinking","thinking":"hmm","signature":"sig-1"}`,
		"short reasoning_opaque":         `{"reasoning_opaque":"opaque-1"}`,
		"hex digest under signature":     `{"signature":"` + strings.Repeat("ab12", 40) + `"}`,
		"embedded image sample":          `{"base64":"` + blob(1440) + `"}`,
		"long hex run":                   strings.Repeat("ab12cd34", 700),
		"readable request id":            `headers: {"x-client-request-id": "session-affinity-label"}`,
		"repeated digit request id":      `res.setHeader("x-amzn-requestid","11111111-2222-3333-4444-555555555555")`,
		"short request id":               `{"x-request-id":"r1"}`,
		"synthetic ids":                  `{"id":"msg_1","call":"call_1","resp":"resp_abc"}`,
		"round created epoch":            `{"created":1700000000}`,
		"round created timestamp":        `{"created_at":"2026-01-01T00:00:00.000Z"}`,
		"placeholder accounts":           `/Users/me/a /home/user/b /home/you/c C:\\Users\\me\\d /home/u/e`,
		"repeated one-word deltas":       repeated,
		"short reasoning":                `data: {"delta":{"reasoning_text":"let me think about it"}}`,
		"usage with ordinary token keys": `{"usage":{"input_tokens":1,"output_tokens":2,"prompt_tokens_details":{"cached_tokens":0}}}`,
	}
	for name, content := range cases {
		if found := scanText("testdata/x.jsonl", []byte(content), true); len(found) != 0 {
			t.Errorf("%s: unexpected findings %v", name, found)
		}
	}
}

func TestFixtureOnlyRulesSkipOtherFiles(t *testing.T) {
	content := []byte(`{"id":"chatcmpl-` + blob(29) + `","cwd":"/Users/jdoe/x","created":1761834127}`)
	if found := scanText("docs/x.md", content, false); len(found) != 0 {
		t.Fatalf("prose files only get the capture rules, got %v", found)
	}
	opaque := []byte(`"reasoning_opaque":"` + blob(500) + `"`)
	if found := scanText("docs/x.md", opaque, false); len(found) != 1 || found[0].code != codeOpaque {
		t.Fatalf("opaque blob outside fixtures: %v", found)
	}
}

func TestIsFixture(t *testing.T) {
	for path, want := range map[string]bool{
		"ai/testdata/stream.jsonl":              true,
		"internal/x/fixtures/a.txt":             true,
		"test/parity/scenarios/a/b.toml":        true,
		"test/parity/cmd/sourcehygiene/main.go": false,
		"ai/stream_test.go":                     false,
		"docs/site/docs/a.md":                   false,
		"ai/testdata/gen/helper.go":             true,
	} {
		if got := isFixture(path); got != want {
			t.Errorf("isFixture(%q) = %v, want %v", path, got, want)
		}
	}
}

func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRunScansUntrackedFilesAndReportsExitCodes(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"ok/testdata/a.json":   `{"signature":"sig-1"}`,
		"bad/testdata/b.jsonl": `{"reasoning_opaque":"` + blob(2000) + `"}`,
	})
	var out, errOut bytes.Buffer
	if code := run(root, "", &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1: %s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "bad/testdata/b.jsonl:1: FG001") || strings.Contains(out.String(), "ok/testdata") {
		t.Fatalf("output %q", out.String())
	}
	if strings.Contains(out.String(), blob(40)) {
		t.Fatal("output echoes the captured value")
	}
	clean := gitRepo(t, map[string]string{"ok/testdata/a.json": `{"signature":"sig-1"}`})
	out.Reset()
	if code := run(clean, "", &out, &errOut); code != 0 {
		t.Fatalf("clean tree exit %d: %s", code, out.String())
	}
}

func TestPrivateTermsFailWithoutPrintingTheTerm(t *testing.T) {
	const secret = "Zorblax-Internal-Name"
	root := gitRepo(t, map[string]string{
		"docs/a.md":              "first line\nmentions " + strings.ToLower(secret) + " here\n",
		"testdata/clean.json":    `{"a":1}`,
		"notes-" + secret + ".x": "named for the term\n",
	})
	terms := filepath.Join(t.TempDir(), "terms.txt")
	if err := os.WriteFile(terms, []byte("\nunrelated-term\n"+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run(root, terms, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1: %s%s", code, out.String(), errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "docs/a.md:2: FG006: private term at terms-file line 3") {
		t.Fatalf("missing file:line and term index in %q", text)
	}
	if !strings.Contains(text, "notes-<term-3>.x:0: FG006: path matches private term at terms-file line 3") {
		t.Fatalf("missing path match in %q", text)
	}
	if strings.Contains(strings.ToLower(text), "zorblax") || strings.Contains(strings.ToLower(errOut.String()), "zorblax") || strings.Contains(text, "unrelated") {
		t.Fatalf("output prints a term: %q", text)
	}
}

func TestPrivateTermsFileValidation(t *testing.T) {
	root := gitRepo(t, map[string]string{"a.txt": "x\n"})
	var out, errOut bytes.Buffer
	inside := filepath.Join(root, "terms.txt")
	if err := os.WriteFile(inside, []byte("secret-term\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(root, inside, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "outside the repository") {
		t.Fatalf("inside-repo terms file: exit %d, stderr %q", code, errOut.String())
	}
	outside := filepath.Join(t.TempDir(), "short.txt")
	if err := os.WriteFile(outside, []byte("ok-term\nab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	code := run(root, outside, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "line 2 is shorter") || strings.Contains(errOut.String(), "ab\n") {
		t.Fatalf("short term: exit %d, stderr %q", code, errOut.String())
	}
	if code := run(root, filepath.Join(t.TempDir(), "missing.txt"), &out, &errOut); code != 2 {
		t.Fatalf("missing terms file: exit %d, want 2", code)
	}
}

func TestAllowlistSuppressesAndReportsStaleEntries(t *testing.T) {
	saved := allowlist
	t.Cleanup(func() { allowlist = saved })
	allowlist = []allowed{
		{path: "a/testdata/x.json", code: codeHomePath, reason: "test"},
		{path: "a/testdata/gone.json", code: codeHomePath, reason: "test"},
		{path: "a/testdata/unscanned.json", code: codeHomePath, reason: "test"},
	}
	findings := []finding{
		{"a/testdata/x.json", 1, codeHomePath, "m"},
		{"a/testdata/x.json", 2, codeOpaque, "m"},
		{"a/testdata/x.json", 3, codePrivate, "m"},
	}
	kept, stale := applyAllowlist(findings, map[string]bool{"a/testdata/x.json": true, "a/testdata/gone.json": true})
	if len(kept) != 2 || kept[0].code != codeOpaque || kept[1].code != codePrivate {
		t.Fatalf("kept %v", kept)
	}
	if len(stale) != 1 || stale[0].path != "a/testdata/gone.json" {
		t.Fatalf("stale %v", stale)
	}
	allowlist = []allowed{{path: "a/testdata/x.json", code: codePrivate, reason: "never"}}
	if kept, _ := applyAllowlist([]finding{{"a/testdata/x.json", 1, codePrivate, "m"}}, nil); len(kept) != 1 {
		t.Fatal("a private-term finding must not be allowlistable")
	}
}

func TestAllowlistEntriesHaveReasons(t *testing.T) {
	for _, a := range allowlist {
		if strings.TrimSpace(a.reason) == "" || a.code == codePrivate {
			t.Errorf("allowlist entry %s %s needs a reason and a suppressible rule", a.path, a.code)
		}
	}
}

// The repository's own fixtures are the green case: every existing synthetic fixture passes the thresholds.
func TestRepositoryFixturesPass(t *testing.T) {
	if testing.Short() {
		t.Skip("scans the whole repository")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files, err := listFiles(root)
	if err != nil || len(files) < 1000 {
		t.Skipf("not a git checkout of the repository: %v (%d files)", err, len(files))
	}
	findings, scanned, err := scanTree(root, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	kept, stale := applyAllowlist(findings, scanned)
	for _, f := range kept {
		t.Errorf("%s:%d %s %s", f.path, f.line, f.code, f.message)
	}
	for _, s := range stale {
		t.Errorf("stale allowlist entry %s %s", s.path, s.code)
	}
}
