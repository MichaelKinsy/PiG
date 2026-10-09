package durabletest

// pi: packages/durable/src/testing/env-conformance.ts

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/env"
)

// Pi packages/durable/src/testing/env-conformance.ts createEnvConformance: the same 24 named cases (the symlink cases only when symlinks work), watch cases carry the 30 s timeout, and a provider failure is the case's result.
// mutation-checked: dropping the reads and writes of EnvConformanceCase.Name, EnvConformanceCase.Run, EnvConformanceOptions.WithEnv fails it
// Pi: packages/durable/src/testing/env-conformance.ts:102 (name)
// Pi: packages/durable/src/testing/env-conformance.ts:58 (run)
// Pi: packages/durable/src/testing/env-conformance.ts:95 (withEnv)
// Pi source: packages/durable/src/testing/env-conformance.ts:102-108 (createCase, watchCase) and :125 (first case).
// mutation-checked: the mutants "createCase renames every case", "createCase drops the watch timeout" and "a case never calls the provider" fail it.
func TestCreateEnvConformanceCasesMatchUpstream(t *testing.T) {
	want := []string{
		"argv exec distinguishes timeout from abort",
		"argv exec honors cwd and exit codes",
		"argv exec passes arguments to the program without shell parsing",
		"argv exec reports missing programs and empty argv as spawn errors",
		"binary reader follows symlinks unless noFollow refuses the final one",
		"binary reader keeps reading the file it opened after a rename",
		"binary reader reads byte ranges of the opened file",
		"binary reader refuses directories, missing files and aborted opens",
		"binary reader scans lines like decoding the whole file",
		"directory reader pages every entry exactly once",
		"directory reader refuses missing paths and files",
		"directory reader reports the end and refuses use after close",
		"directory reader skips entries removed during enumeration",
		"exec reports the stream of every chunk in both forms",
		"watch follows a directory replaced at the same path",
		"watch follows directories created together with their contents",
		"watch keeps recursive coverage where a non-recursive target overlaps",
		"watch keeps watching a path whose parent is renamed and recreated",
		"watch reports a missing file's creation, changes, replacement and removal",
		"watch reports a missing target whose ancestors are created",
		"watch reports changes to the file a watched symbolic link points to",
		"watch skips excluded entries and reports a rename out of them",
		"watch stops reporting once closed",
		"windowed exec keeps the exact tail and counts what it skips",
	}
	providerFailure := errors.New("provider failed")
	withEnv := func(func(env.ExecutionEnv) error) error { return providerFailure }
	cases := CreateEnvConformance(EnvConformanceOptions{Assertions: CreateTestingAssertions(t), WithEnv: withEnv})
	var got []string
	for _, testCase := range cases {
		got = append(got, testCase.Name)
		if err := testCase.Run(); !errors.Is(err, providerFailure) {
			t.Errorf("%s: Run returned %v, want the provider's error", testCase.Name, err)
		}
		watching := len(testCase.Name) > 5 && testCase.Name[:5] == "watch"
		if watching != (testCase.Timeout == 30*time.Second) {
			t.Errorf("%s: timeout %v", testCase.Name, testCase.Timeout)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("cases %q, want %q", got, want)
	}
	withoutSymlinks := CreateEnvConformance(EnvConformanceOptions{Assertions: CreateTestingAssertions(t), WithEnv: withEnv, Symlinks: new(false)})
	if len(withoutSymlinks) >= len(cases) {
		t.Fatalf("Symlinks false kept %d of %d cases", len(withoutSymlinks), len(cases))
	}
}
