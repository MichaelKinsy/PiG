package subprocess

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// maxGroupedNames bounds the extension names one grouped failure line lists.
// pig additive (D20): PiG compiles extensions, which Pi does not.
const maxGroupedNames = 4

// BuildFailureGroup is several extensions whose builds failed for one root
// cause, reported as one error rather than once each. Thirteen extensions
// failing on a mismatched toolchain are one problem.
//
// pig additive (D20): Pi loads extension source in process and never compiles.
type BuildFailureGroup struct {
	// Names are the failed extensions, in load order.
	Names []string
	// Failure is the first member's failure. Its Cause is shared by every member.
	Failure *runtimecell.BuildFailure
}

func (g *BuildFailureGroup) Error() string {
	names := g.Names
	lead := strings.Join(names, ", ")
	if len(names) > maxGroupedNames {
		lead = strings.Join(names[:maxGroupedNames], ", ") + fmt.Sprintf(" and %d more", len(names)-maxGroupedNames)
	}
	prefix := ""
	if g.Failure.Cached {
		prefix = "cached build failure (inputs unchanged): "
	}
	return fmt.Sprintf("%d extensions failed to build: %s%s%s", len(g.Names), prefix, g.Failure.Cause, g.Failure.Detail(lead))
}

// namedBuildFailure is one extension's build failure.
type namedBuildFailure struct {
	name    string
	failure *runtimecell.BuildFailure
}

// groupBuildFailures folds failures with equal Cause into one group each, in the
// order of each cause's first failure. A cause with a single extension yields no
// group: the caller keeps that failure as it is.
func groupBuildFailures(failures []namedBuildFailure) (groups []*BuildFailureGroup, single []namedBuildFailure) {
	byCause := map[string]*BuildFailureGroup{}
	var order []*BuildFailureGroup
	for _, item := range failures {
		group, ok := byCause[item.failure.Cause]
		if !ok {
			group = &BuildFailureGroup{Failure: item.failure}
			byCause[item.failure.Cause] = group
			order = append(order, group)
		}
		group.Names = append(group.Names, item.name)
	}
	for _, group := range order {
		if len(group.Names) > 1 {
			groups = append(groups, group)
			continue
		}
		single = append(single, namedBuildFailure{group.Names[0], group.Failure})
	}
	return groups, single
}

// GroupLoadErrors replaces the load errors of extensions that failed to build for
// one root cause with one BuildFailureGroup each. Every other error, and a build
// failure no other extension shares, is returned unchanged and in place.
func GroupLoadErrors(errs []error) []error {
	var failures []namedBuildFailure
	members := make(map[int]bool)
	for i, err := range errs {
		loadErr, ok := errors.AsType[*ExtensionLoadError](err)
		if !ok {
			continue
		}
		if failure, ok := errors.AsType[*runtimecell.BuildFailure](loadErr.Err); ok {
			failures = append(failures, namedBuildFailure{loadErr.Name, failure})
			members[i] = true
		}
	}
	groups, _ := groupBuildFailures(failures)
	if len(groups) == 0 {
		return errs
	}
	groupOf := map[string]*BuildFailureGroup{}
	for _, group := range groups {
		groupOf[group.Failure.Cause] = group
	}
	out := make([]error, 0, len(errs))
	emitted := map[*BuildFailureGroup]bool{}
	for i, err := range errs {
		if !members[i] {
			out = append(out, err)
			continue
		}
		loadErr, _ := errors.AsType[*ExtensionLoadError](err)
		failure, _ := errors.AsType[*runtimecell.BuildFailure](loadErr.Err)
		group, grouped := groupOf[failure.Cause]
		switch {
		case !grouped:
			out = append(out, err)
		case !emitted[group]:
			emitted[group] = true
			out = append(out, group)
		}
	}
	return out
}

// buildFailureIssues formats the reload issues of the extensions that failed
// in one load pass, in load order. Build failures that share a root cause with
// another extension's are one line, placed where the first of them failed; every
// other failure is its own reloadIssue line in its own place.
func buildFailureIssues(failures []reloadBuildFailure) []string {
	var named []namedBuildFailure
	for _, item := range failures {
		if build, ok := errors.AsType[*runtimecell.BuildFailure](item.err); ok {
			named = append(named, namedBuildFailure{item.cfg.Name, build})
		}
	}
	groups, _ := groupBuildFailures(named)
	grouped := map[string]*BuildFailureGroup{}
	for _, group := range groups {
		grouped[group.Failure.Cause] = group
	}
	issues := make([]string, 0, len(failures))
	emitted := map[*BuildFailureGroup]bool{}
	for _, item := range failures {
		build, isBuild := errors.AsType[*runtimecell.BuildFailure](item.err)
		if !isBuild {
			issues = append(issues, reloadIssue(item.cfg, item.err))
			continue
		}
		group, isGroup := grouped[build.Cause]
		switch {
		case !isGroup:
			issues = append(issues, fmt.Sprintf("%s: Failed to load extension: %s", extConfigOrigin(item.cfg), build.Error()))
		case !emitted[group]:
			emitted[group] = true
			issues = append(issues, group.Error())
		}
	}
	return issues
}

// reloadBuildFailure is one extension that failed to load during a reload,
// whether or not its build failed.
type reloadBuildFailure struct {
	cfg ExtConfig
	err error
}
