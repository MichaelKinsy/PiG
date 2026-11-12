package durabletest

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// capturingTB is a testing.TB whose Fatalf records the message and stops its caller, as a failing test stops.
type capturingTB struct {
	testing.TB
	message string
}

func (*capturingTB) Helper() {}
func (tb *capturingTB) Fatalf(format string, args ...any) {
	tb.message = strings.TrimSpace(strings.ReplaceAll(format, "%v", "")) + errString(args)
	panic(failedCheck{message: tb.message})
}

func errString(args []any) string {
	var out []string
	for _, arg := range args {
		if err, ok := arg.(error); ok {
			out = append(out, err.Error())
		}
	}
	return strings.Join(out, " ")
}

func failure(run func(tb testing.TB)) (message string, failed bool) {
	tb := &capturingTB{}
	defer func() {
		if recovered := recover(); recovered != nil {
			message, failed = tb.message, true
		}
	}()
	run(tb)
	return "", false
}

// runner.ts registerStorageConformance and registerEnvConformance register one runner case per conformance case, and a case that rejects fails its runner
// test: the failure of the case's storage or environment provider reaches the runner as that case's failure, and a case that completes passes.
func TestConformanceRunnerCasesFailWithTheirProvidersFailure(t *testing.T) {
	providerFailure := errors.New("provider failed")
	t.Run("storage", func(t *testing.T) {
		memory := func(use func(durable.Storage) error) error { return use(storage.NewMemoryStorage()) }
		failing := func(func(durable.Storage) error) error { return providerFailure }
		for index, name := range caseNames() {
			if message, failed := failure(func(tb testing.TB) { runStorageConformanceCase(tb, memory, index) }); failed {
				t.Fatalf("case %q failed on correct storage: %s", name, message)
			}
			if message, failed := failure(func(tb testing.TB) { runStorageConformanceCase(tb, failing, index) }); !failed || !strings.Contains(message, "provider failed") {
				t.Fatalf("case %q with a failing provider: failed = %v, message %q", name, failed, message)
			}
		}
	})
	t.Run("environment", func(t *testing.T) {
		failing := func(func(env.ExecutionEnv) error) error { return providerFailure }
		cases := CreateEnvConformance(EnvConformanceOptions{})
		if len(cases) == 0 {
			t.Fatal("no environment cases")
		}
		for index, testCase := range cases {
			if message, failed := failure(func(tb testing.TB) { runEnvConformanceCase(tb, failing, EnvConformanceRegisterOptions{}, index) }); !failed || !strings.Contains(message, "provider failed") {
				t.Fatalf("case %q with a failing provider: failed = %v, message %q", testCase.Name, failed, message)
			}
		}
	})
}
