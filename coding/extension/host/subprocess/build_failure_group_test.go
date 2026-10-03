package subprocess

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func loadError(name string, err error) error {
	return &ExtensionLoadError{Name: name, Path: "/pkg/" + name, Err: fmt.Errorf("build packed cell go-%s: %w", name, err)}
}

// Thirteen extensions that fail for the toolchain are one line; a different
// failure and an unrelated error keep their own places.
func TestGroupLoadErrorsFoldsOneRootCause(t *testing.T) {
	const cause = "Go toolchain mismatch: the compiler is go1.26.7 but the go command is go1.26.1 (/opt/go/bin/go); reinstall that Go release"
	var errs []error
	for i := range 13 {
		errs = append(errs, loadError(fmt.Sprintf("ext%02d", i), &runtimecell.BuildFailure{Summary: cause, Cause: cause, Log: "/cache/logs/build-a.log"}))
		if i == 2 {
			errs = append(errs, loadError("odd", &runtimecell.BuildFailure{Summary: "go build: odd.go:1:1: syntax error", Cause: "go build: syntax error"}))
			errs = append(errs, errors.New("embedded cell failed"))
		}
	}
	got := GroupLoadErrors(errs)
	if len(got) != 3 {
		t.Fatalf("%d errors, want the group, the odd failure and the unrelated error:\n%v", len(got), got)
	}
	group, ok := errors.AsType[*BuildFailureGroup](got[0])
	if !ok || len(group.Names) != 13 {
		t.Fatalf("first = %T %v", got[0], got[0])
	}
	line := got[0].Error()
	if strings.Contains(line, "\n") || !strings.HasPrefix(line, "13 extensions failed to build: Go toolchain mismatch") ||
		!strings.Contains(line, "ext00, ext01, ext02, ext03 and 9 more") || !strings.Contains(line, "details: /cache/logs/build-a.log") || strings.Count(line, "go1.26.7") != 1 {
		t.Fatalf("group line = %q", line)
	}
	if _, isLoad := errors.AsType[*ExtensionLoadError](got[1]); !isLoad || !strings.Contains(got[1].Error(), "odd") {
		t.Fatalf("second = %v", got[1])
	}
	if got[2].Error() != "embedded cell failed" {
		t.Fatalf("third = %v", got[2])
	}
}

func TestGroupLoadErrorsLeavesDistinctFailuresAlone(t *testing.T) {
	errs := []error{
		loadError("a", &runtimecell.BuildFailure{Summary: "go build: a.go:1:1: x", Cause: "go build: x"}),
		loadError("b", &runtimecell.BuildFailure{Summary: "go build: b.go:1:1: y", Cause: "go build: y"}),
		errors.New("other"),
	}
	got := GroupLoadErrors(errs)
	if len(got) != 3 || !errors.Is(got[0], errs[0]) || !errors.Is(got[1], errs[1]) {
		t.Fatalf("errors changed: %v", got)
	}
}

func TestGroupedCachedFailureNamesTheRetryCommand(t *testing.T) {
	failure := &runtimecell.BuildFailure{Summary: "s", Cause: "boom", Log: "/l.log", Cached: true}
	group := &BuildFailureGroup{Names: []string{"a", "b"}, Failure: failure}
	want := "2 extensions failed to build: cached build failure (inputs unchanged): boom (a, b; details: /l.log; retry without changing an input: " + runtimecell.RetryCachedBuildCommand + ")"
	if group.Error() != want {
		t.Fatalf("got  %q\nwant %q", group.Error(), want)
	}
}

// A shared build failure is one line where its first member failed; a single
// build failure and any other load error keep their own places.
func TestBuildFailureIssuesForReload(t *testing.T) {
	shared := &runtimecell.BuildFailure{Summary: "mismatch", Cause: "mismatch", Log: "/l.log"}
	issues := buildFailureIssues([]reloadBuildFailure{
		{ExtConfig{Name: "early"}, errors.New("socket closed")},
		{ExtConfig{Name: "a"}, fmt.Errorf("build packed cell a: %w", shared)},
		{ExtConfig{Name: "solo"}, &runtimecell.BuildFailure{Summary: "solo failed", Cause: "solo"}},
		{ExtConfig{Name: "middle"}, errors.New("register timeout")},
		{ExtConfig{Name: "b"}, shared},
	})
	want := []string{
		reloadIssue(ExtConfig{Name: "early"}, errors.New("socket closed")),
		"2 extensions failed to build: mismatch (a, b; details: /l.log)",
		extConfigOrigin(ExtConfig{Name: "solo"}) + ": Failed to load extension: solo failed",
		reloadIssue(ExtConfig{Name: "middle"}, errors.New("register timeout")),
	}
	if !slices.Equal(issues, want) {
		t.Fatalf("issues = %q\nwant     %q", issues, want)
	}
}

// The startup hint recognizes a group by its text, so every form of the group's
// message must match and an ordinary load error must not.
func TestIsBuildFailureGroupMessage(t *testing.T) {
	failure := &runtimecell.BuildFailure{Summary: "s", Cause: "boom", Log: "/l.log"}
	cached := &runtimecell.BuildFailure{Summary: "s", Cause: "boom", Log: "/l.log", Cached: true}
	for _, group := range []*BuildFailureGroup{
		{Names: []string{"a", "b"}, Failure: failure},
		{Names: []string{"a", "b"}, Failure: cached},
		{Names: []string{"a", "b", "c", "d", "e", "f"}, Failure: failure},
	} {
		if !IsBuildFailureGroupMessage(group.Error()) {
			t.Errorf("IsBuildFailureGroupMessage(%q) = false", group.Error())
		}
	}
	for _, message := range []string{
		`Failed to load extension "/x": Failed to load extension: go build: boom (details: /l.log)`,
		`Extension "/x" error: 2 extensions failed to build: boom`,
		"extensions failed to build: boom",
	} {
		if IsBuildFailureGroupMessage(message) {
			t.Errorf("IsBuildFailureGroupMessage(%q) = true", message)
		}
	}
}
