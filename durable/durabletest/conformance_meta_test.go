package durabletest_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// recorder implements StorageConformanceAssertions (and so EnvConformanceAssertions): it counts checks and keeps the
// failed ones instead of stopping the test.
type recorder struct {
	checks   int
	failures []string
}

func (r *recorder) check(ok bool, message string) {
	r.checks++
	if !ok {
		r.failures = append(r.failures, message)
	}
}

func (r *recorder) Ok(value any, message ...string) {
	r.check(truthy(value), strings.Join(message, " "))
}
func (r *recorder) StrictEqual(actual, expected any) {
	r.check(reflect.DeepEqual(durabletest.Normalize(actual), durabletest.Normalize(expected)), "strictEqual")
}
func (r *recorder) DeepEqual(actual, expected any) {
	r.check(reflect.DeepEqual(durabletest.Normalize(actual), durabletest.Normalize(expected)), "deepEqual")
}
func (r *recorder) PartialDeepEqual(actual, expected any) {
	r.check(durabletest.MatchObject(durabletest.Normalize(actual), durabletest.Normalize(expected)), "partialDeepEqual")
}
func (r *recorder) GreaterThan(actual, expected float64) { r.check(actual > expected, "greaterThan") }
func (r *recorder) Rejects(err error, messageIncludes string) {
	r.check(err != nil && strings.Contains(err.Error(), messageIncludes), "rejects "+messageIncludes)
}

// failed runs a case; an error or a panic (a case dereferencing a row the broken subject never stored) is a failure.
func failed(run func() error) (failed bool) {
	defer func() {
		if recover() != nil {
			failed = true
		}
	}()
	return run() != nil
}

// brokenStorage forgets every commit: reads see nothing.
type brokenStorage struct{ durable.Storage }

func (brokenStorage) Commit(context.Context, []durable.StorageWrite) (durable.Seq, error) {
	return 0, nil
}

// TestStorageConformanceCasesPassForMemoryStorageAndFailForBrokenStorage is the negative control of the storage
// conformance suite (testing/storage-conformance.ts): the suite accepts a correct storage and rejects one that drops
// commits.
func TestStorageConformanceCasesPassForMemoryStorageAndFailForBrokenStorage(t *testing.T) {
	run := func(provider durabletest.StorageConformanceProvider) (cases []durabletest.StorageConformanceCase, rec *recorder, errs int) {
		rec = &recorder{}
		cases = durabletest.CreateStorageConformance(durabletest.StorageConformanceOptions{Assertions: rec, WithStorage: provider})
		for _, c := range cases {
			if failed(c.Run) {
				errs++
			}
		}
		return cases, rec, errs
	}
	good := func(use func(durable.Storage) error) error { return use(storage.NewMemoryStorage()) }
	cases, rec, errs := run(good)
	if len(cases) < 20 || errs != 0 || len(rec.failures) != 0 || rec.checks < len(cases) {
		t.Fatalf("memory storage: %d cases, %d errors, %d checks, failures %v", len(cases), errs, rec.checks, rec.failures)
	}
	_, rec, errs = run(func(use func(durable.Storage) error) error { return use(brokenStorage{storage.NewMemoryStorage()}) })
	if errs == 0 && len(rec.failures) == 0 {
		t.Fatalf("a storage that drops commits passed every case")
	}
}

// brokenEnv reads every file as empty.
type brokenEnv struct{ env.ExecutionEnv }

func (brokenEnv) ReadTextFile(context.Context, string) (string, error) { return "", nil }

// TestEnvConformanceCasesPassForNodeEnvAndFailForBrokenEnv is the negative control of the environment conformance suite.
func TestEnvConformanceCasesPassForNodeEnvAndFailForBrokenEnv(t *testing.T) {
	run := func(wrap func(env.ExecutionEnv) env.ExecutionEnv) (cases []durabletest.EnvConformanceCase, rec *recorder, errs int) {
		rec = &recorder{}
		provider := func(use func(env.ExecutionEnv) error) error {
			return use(wrap(envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()})))
		}
		cases = durabletest.CreateEnvConformance(durabletest.EnvConformanceOptions{Assertions: rec, WithEnv: provider})
		for _, c := range cases {
			if c.Timeout > 0 {
				continue // watch cases wait on file system latency; the passing and failing runs below are decided without them
			}
			if failed(c.Run) {
				errs++
			}
		}
		return cases, rec, errs
	}
	cases, rec, errs := run(func(e env.ExecutionEnv) env.ExecutionEnv { return e })
	if len(cases) < 20 || errs != 0 || len(rec.failures) != 0 || rec.checks < len(cases)/2 {
		t.Fatalf("node env: %d cases, %d errors, %d checks, failures %v", len(cases), errs, rec.checks, rec.failures)
	}
	_, rec, errs = run(func(e env.ExecutionEnv) env.ExecutionEnv { return brokenEnv{e} })
	if errs == 0 && len(rec.failures) == 0 {
		t.Fatalf("an environment that reads empty files passed every case")
	}
}

// truthy is JavaScript truthiness for the values the suites assert on: nil, false, a zero number and "" are falsy.
func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0 && typed == typed
	}
	return true
}
