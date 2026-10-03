package evals

// Ports packages/evals/test/plan.test.ts.

import (
	"reflect"
	"strings"
	"testing"
)

func discoveredFixture() []any {
	return []any{map[string]any{"name": "Add model > adds the model", "file": "evals/models.docs.eval.ts"}}
}

func requireErrorContaining(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one containing %q", err, want)
	}
}

// TestPlanUpstream ports packages/evals/test/plan.test.ts. Each subtest names one upstream case.
func TestPlanUpstream(t *testing.T) {
	t.Run("parseDiscoveredCases › derives stable case identity from ordinary Vitest names", func(t *testing.T) {
		// upstream: packages/evals/test/plan.test.ts:7
		cases, err := ParseDiscoveredCases(discoveredFixture())
		if err != nil {
			t.Fatal(err)
		}
		want := []DiscoveredEvalCase{{
			FullName: "Add model > adds the model",
			EvalSet:  "Add model",
			CaseID:   "adds the model",
			File:     "evals/models.docs.eval.ts",
		}}
		if !reflect.DeepEqual(cases, want) {
			t.Fatalf("cases = %#v, want %#v", cases, want)
		}
	})

	t.Run("parseDiscoveredCases › rejects ambiguous and duplicate identities", func(t *testing.T) {
		// upstream: packages/evals/test/plan.test.ts:18
		_, err := ParseDiscoveredCases([]any{map[string]any{"name": "adds the model", "file": "model.ts"}})
		requireErrorContaining(t, err, "<eval set> > <case>")
		_, err = ParseDiscoveredCases(append(discoveredFixture(), discoveredFixture()...))
		requireErrorContaining(t, err, "Duplicate eval case identity")
	})

	t.Run("createTaskPlan › creates one isolated task per case, variant, model, and repetition", func(t *testing.T) {
		// upstream: packages/evals/test/plan.test.ts:25
		cases, err := ParseDiscoveredCases(discoveredFixture())
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := CreateTaskPlan(cases, "fixture/model", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 4 {
			t.Fatalf("tasks = %d, want 4", len(tasks))
		}
		type variantRun struct {
			variant   DocumentationVariant
			runNumber int
		}
		var got []variantRun
		for _, task := range tasks {
			got = append(got, variantRun{task.Variant, task.RunNumber})
		}
		want := []variantRun{{"without_docs", 1}, {"with_docs", 1}, {"with_docs", 2}, {"without_docs", 2}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("variant order = %v, want %v", got, want)
		}
	})

	t.Run("createTaskPlan › rejects invalid model identities and repetitions", func(t *testing.T) {
		// upstream: packages/evals/test/plan.test.ts:37
		cases, err := ParseDiscoveredCases(discoveredFixture())
		if err != nil {
			t.Fatal(err)
		}
		_, err = CreateTaskPlan(cases, "model", 1)
		requireErrorContaining(t, err, "provider and model")
		_, err = CreateTaskPlan(cases, "fixture/model", 0)
		requireErrorContaining(t, err, "positive integer")
	})
}
