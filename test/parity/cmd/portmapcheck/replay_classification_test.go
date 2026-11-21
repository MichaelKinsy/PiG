package main

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestAKillByAssertionsStandsWhenALaterTestPanics: a mutant fails a test by an assertion and a later test of the same replay panics (a race
// the mutant provokes, which only shows under load). The failed assertion is the proof: the crash does not undo it and is reported beside
// the kill. A replay whose only effect is a crash, or whose crash came before any assertion failed, proves nothing.
func TestAKillByAssertionsStandsWhenALaterTestPanics(t *testing.T) {
	const two = "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/widget.ts\nfunc TestWidgetA(t *testing.T) {\n\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello a\")\n\t}\n}\n\nfunc TestWidgetB(t *testing.T) {\n\tif (Widget{Name: \"b\"}).Hello() != \"hello b\" {\n\t\tt.Fatal(\"hello b\")\n\t}\n}\n"
	mutate := func(replacement string) string {
		b, _ := json.Marshal(map[string][]mutant{"packages/fx/src/widget.ts": {{File: "lib/widget.go", Old: "\"hello \" + w.Name", New: replacement}}})
		return string(b)
	}
	for _, tc := range []struct {
		name, mutant, want, also string
	}{
		{"the first test fails an assertion, the second panics", mutate(`func() string { if w.Name == "b" { panic(w.Name) }; return "bye " + w.Name }()`), "", "the mutant also panicked: panic: b"},
		{"the first test panics before any assertion failed", mutate(`func() string { if w.Name == "a" { panic(w.Name) }; return "bye " + w.Name }()`), "the mutant panicked: panic: a", ""},
		{"both tests fail by an assertion", mutate(`"bye " + w.Name`), "", ""},
	} {
		root := markerFixture(t, two, tc.mutant)
		res, err := checkWith(root, true)
		if err != nil || len(res) != 1 {
			t.Fatalf("%s: %v %d", tc.name, err, len(res))
		}
		got := strings.Join(res[0].Reasons, ";")
		if (tc.want == "") != res[0].Proven || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: proven=%v reasons=%q, want %q", tc.name, res[0].Proven, got, tc.want)
		}
		var mutants map[string][]mutant
		if err := json.Unmarshal([]byte(tc.mutant), &mutants); err != nil {
			t.Fatal(err)
		}
		_, infra, also := mutantProblem(root, res[0], mutants["packages/fx/src/widget.ts"], nil, nil)
		if infra != "" || (tc.also == "") != (also == "") || !strings.Contains(also, tc.also) {
			t.Errorf("%s: infra %q, reported beside the kill %q, want %q", tc.name, infra, also, tc.also)
		}
	}
}

// TestReplayClassification: what a replay of a mutant shows, from the output go test prints. Only a failed assertion kills; a crash
// after one is reported beside it; a crash before one, a timeout, a build error, passing tests and a failure with nothing reported are
// each their own note.
func TestReplayClassification(t *testing.T) {
	failed := exec.Command("go", "tool", "no-such-tool-for-portmapcheck").Run()
	if failed == nil {
		t.Fatal("expected a failing go command")
	}
	const stack = "[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x4a1b2c]\n\ngoroutine 7 [running]:\ntesting.tRunner.func1.2({0x5a0f20, 0x6f4a60})\n\t/usr/local/go/src/testing/testing.go:1872 +0x237\ntesting.tRunner.func1()\n\t/usr/local/go/src/testing/testing.go:1875 +0x35e\npanic({0x5a0f20?, 0x6f4a60?})\n\t/usr/local/go/src/runtime/panic.go:783 +0x132\nfixture/lib.Widget.Hello(...)\n\t/work/lib/widget.go:5 +0x1a\nfixture/lib.TestB(0xc0000a4000)\n\t/work/lib/b_test.go:9 +0x1a\nFAIL\tfixture/lib\t0.004s\nFAIL\n"
	const nilPanic = "panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]\n"
	for _, tc := range []struct {
		name, out string
		err       error
		killed    bool
		note      string
	}{
		{"the tests passed", "ok  \tfixture/lib\t0.004s\n", nil, false, "the tests passed with the mutant"},
		{"the mutant does not build", "# fixture/lib\nlib/widget.go:5:50: undefined: undefined\nFAIL\tfixture/lib [build failed]\n", failed, false, "the mutant did not build: lib/widget.go:5:50: undefined: undefined"},
		{"a failed assertion", "--- FAIL: TestA (0.00s)\n    a_test.go:9: want 1\nFAIL\nFAIL\tfixture/lib\t0.004s\nFAIL\n", failed, true, ""},
		{"a failed assertion of a subtest", "--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/sub (0.00s)\n        a_test.go:9: want 1\nFAIL\nFAIL\tfixture/lib\t0.004s\nFAIL\n", failed, true, ""},
		{"a failed assertion, then a later test panics",
			"--- FAIL: TestA (0.00s)\n    a_test.go:9: want 1\n--- FAIL: TestB (0.00s)\n" + nilPanic + stack, failed, true,
			"the mutant also panicked: " + strings.TrimSpace(nilPanic) + " at /work/lib/widget.go:5"},
		{"a test reports a line, then panics: its lines follow its header and are not an assertion",
			"--- FAIL: TestB (0.00s)\n    b_test.go:5: arranging\n" + nilPanic + stack, failed, false,
			"the mutant panicked: " + strings.TrimSpace(nilPanic) + " at /work/lib/widget.go:5; no test failed by an assertion first"},
		{"a failed assertion, then a later test reports an error and panics (the shape of a hung test)",
			"--- FAIL: TestA (0.00s)\n    a_test.go:9: want 1\n--- FAIL: TestB (30.09s)\n    b_test.go:5: the awaited signal never came\n" + nilPanic + stack, failed, true,
			"the mutant also panicked: " + strings.TrimSpace(nilPanic) + " at /work/lib/widget.go:5"},
		{"a failed assertion, then another goroutine panics",
			"--- FAIL: TestA (0.00s)\n    a_test.go:9: want 1\n" + nilPanic + "\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x4a1b2c]\n\ngoroutine 9 [running]:\nfixture/lib.work()\n\t/work/lib/widget.go:5 +0x1a\nFAIL\tfixture/lib\t0.004s\nFAIL\n", failed, true,
			"the mutant also panicked: " + strings.TrimSpace(nilPanic) + " at /work/lib/widget.go:5"},
		{"the tests time out", "panic: test timed out after 10m0s\n\trunning tests:\n\t\tTestA (10m0s)\n\ngoroutine 1 [running]:\nFAIL\tfixture/lib\t600.012s\nFAIL\n", failed, false,
			"the mutant made the tests time out and no test failed by an assertion: panic: test timed out after 10m0s"},
		{"a failed assertion, then the tests time out", "--- FAIL: TestA (0.00s)\n    a_test.go:9: want 1\npanic: test timed out after 10m0s\nFAIL\tfixture/lib\t600.012s\nFAIL\n", failed, true,
			"the replay hit its timeout after failed assertions: panic: test timed out after 10m0s"},
		{"a test fails with nothing reported", "--- FAIL: TestA (0.00s)\nFAIL\nFAIL\tfixture/lib\t0.004s\nFAIL\n", failed, false, "the replay failed without a failed assertion: FAIL"},
	} {
		got := classifyReplay(tc.out, tc.err)
		if got.killed != tc.killed || got.note != tc.note {
			t.Errorf("%s: killed %v note %q, want killed %v note %q", tc.name, got.killed, got.note, tc.killed, tc.note)
		}
	}
}

// TestReplayCommandPassesBoundedTimeout: every mutant replay bounds `go test` with an explicit -timeout of max(10m, 4*took),
// capped at the evidence bound, and unmeasured runs get 10m.
func TestReplayCommandPassesBoundedTimeout(t *testing.T) {
	cmd := replayCommand("/repo", "ai", "/tmp/overlay.json", []string{"TestA"}, 0)
	var got string
	for _, arg := range cmd.Args {
		if v, ok := strings.CutPrefix(arg, "-timeout="); ok {
			got = v
		}
	}
	if got == "" {
		t.Fatalf("replayCommand args %v carry no -timeout flag", cmd.Args)
	}
	if got != (10 * time.Minute).String() {
		t.Errorf("replayCommand unmeasured timeout = %q, want %q", got, 10*time.Minute)
	}
	measured := replayCommand("/repo", "ai", "/tmp/overlay.json", []string{"TestA"}, 3*time.Minute)
	if !slices.Contains(measured.Args, "-timeout=12m0s") {
		t.Errorf("replayCommand measured args %v want -timeout=12m0s", measured.Args)
	}
	for _, tc := range []struct {
		name string
		took time.Duration
		want time.Duration
	}{
		{"unmeasured uses default floor", 0, 10 * time.Minute},
		{"fast run uses default floor", 30 * time.Second, 10 * time.Minute},
		{"boundary run uses default floor", 2*time.Minute + 30*time.Second, 10 * time.Minute},
		{"slow run scales by factor", 3 * time.Minute, 12 * time.Minute},
		{"very long run caps at evidence bound", 10 * time.Minute, 30 * time.Minute},
	} {
		if d := replayTimeout(tc.took); d != tc.want {
			t.Errorf("%s: replayTimeout(%v) = %v, want %v", tc.name, tc.took, d, tc.want)
		}
	}
}

// TestEvidenceTimeoutFlagMatchesDuration: the `-test.timeout=` flag passed to the evidence run is derived from evidenceTimeoutDuration.
func TestEvidenceTimeoutFlagMatchesDuration(t *testing.T) {
	if evidenceTimeout != evidenceTimeoutDuration.String() {
		t.Errorf("evidenceTimeout = %q, want %q", evidenceTimeout, evidenceTimeoutDuration.String())
	}
}
