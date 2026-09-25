package pigletbuild

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeBuilder struct {
	name       string
	readiness  BuilderReadiness
	result     BuilderResult
	err        error
	probeCalls int
	buildCalls int
}

func (b *fakeBuilder) Name() string { return b.name }
func (b *fakeBuilder) Probe(context.Context, BuilderRequest) BuilderReadiness {
	b.probeCalls++
	result := b.readiness
	result.Builder = b.name
	return result
}
func (b *fakeBuilder) Build(context.Context, BuilderRequest) (BuilderResult, error) {
	b.buildCalls++
	return b.result, b.err
}

func TestBuilderReadinessJSONHasNoFormatVersion(t *testing.T) {
	encoded, err := json.Marshal(BuilderReadiness{Builder: "native", Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"version"`) {
		t.Fatalf("readiness exposed a Pig-owned format version: %s", encoded)
	}
}

func TestSelectBuilderAutoUsesFirstReadyWithoutExecuting(t *testing.T) {
	first := &fakeBuilder{name: "container", readiness: BuilderReadiness{Ready: false, Code: "unavailable", Remedy: "start engine"}}
	second := &fakeBuilder{name: "native", readiness: BuilderReadiness{Ready: true}}
	selected, readiness, err := selectBuilder(context.Background(), []BuilderBackend{first, second}, "auto", BuilderRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Name() != "native" || readiness.Builder != "native" || first.probeCalls != 1 || second.probeCalls != 1 || first.buildCalls != 0 || second.buildCalls != 0 {
		t.Fatalf("selected=%v readiness=%+v first=%+v second=%+v", selected.Name(), readiness, first, second)
	}
}

func TestSelectBuilderExplicitNeverFallsBack(t *testing.T) {
	first := &fakeBuilder{name: "container", readiness: BuilderReadiness{Ready: false, Code: "login-required", Remedy: "log in"}}
	second := &fakeBuilder{name: "native", readiness: BuilderReadiness{Ready: true}}
	_, _, err := selectBuilder(context.Background(), []BuilderBackend{first, second}, "container", BuilderRequest{})
	if err == nil || !strings.Contains(err.Error(), "login-required") || !strings.Contains(err.Error(), "remedy: log in") {
		t.Fatalf("error = %v", err)
	}
	if second.probeCalls != 0 || first.buildCalls != 0 || second.buildCalls != 0 {
		t.Fatalf("explicit selection fell back: first=%+v second=%+v", first, second)
	}
}

func TestExecuteBuildHonorsCancellationBeforeExecution(t *testing.T) {
	builder := &fakeBuilder{name: "native", readiness: BuilderReadiness{Ready: true}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executeBuild(ctx, []BuilderBackend{builder}, "native", BuilderRequest{})
	if !errors.Is(err, context.Canceled) || builder.buildCalls != 0 {
		t.Fatalf("error=%v builder=%+v", err, builder)
	}
}

func TestExecuteBuildDoesNotFallbackAfterExecutionFailure(t *testing.T) {
	first := &fakeBuilder{name: "container", readiness: BuilderReadiness{Ready: true}, err: errors.New("build failed")}
	second := &fakeBuilder{name: "native", readiness: BuilderReadiness{Ready: true}, result: BuilderResult{Artifact: "unexpected"}}
	_, err := executeBuild(context.Background(), []BuilderBackend{first, second}, "auto", BuilderRequest{})
	if err == nil || !strings.Contains(err.Error(), "no fallback was attempted") {
		t.Fatalf("error = %v", err)
	}
	if first.buildCalls != 1 || second.probeCalls != 0 || second.buildCalls != 0 {
		t.Fatalf("execution fell back: first=%+v second=%+v", first, second)
	}
}

func TestSelectBuilderReportsAllAutoRemediation(t *testing.T) {
	container := &fakeBuilder{name: "container", readiness: BuilderReadiness{Code: "engine", Message: "engine stopped", Remedy: "start engine"}}
	native := &fakeBuilder{name: "native", readiness: BuilderReadiness{Code: "source", Message: "source missing", Remedy: "set PIG_SOURCE_ROOT"}}
	_, _, err := selectBuilder(context.Background(), []BuilderBackend{container, native}, "auto", BuilderRequest{})
	if err == nil || !strings.Contains(err.Error(), "start engine") || !strings.Contains(err.Error(), "set PIG_SOURCE_ROOT") {
		t.Fatalf("error = %v", err)
	}
}
