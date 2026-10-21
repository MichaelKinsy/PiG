package packagemanager

import (
	"fmt"
	"sync"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/src/core/package-manager.ts (updateConfiguredSources, updateNpmBatch, installNpmBatch).
const packageUpdateConcurrency = 4

type npmUpdateTarget struct {
	ref    sourceref.Ref
	source string
	local  bool
}

type npmUpdateBatch struct {
	root     string
	registry string
	local    bool
	sources  []npmUpdateTarget
}

func updateConfiguredSources(cwd string, sm *codingagent.SettingsManager, packages []ConfiguredPackage, progress ProgressCallback) error {
	if IsOfflineModeEnabled() || len(packages) == 0 {
		return nil
	}
	var npm []npmUpdateTarget
	var git []ConfiguredPackage
	for _, pkg := range packages {
		switch DetectSourceKind(pkg.Source.Source) {
		case "npm":
			if IsPinnedNpm(pkg.Source.Source) {
				continue
			}
			ref, err := ParseNpmInstallRef(pkg.Source.Source)
			if err != nil {
				return err
			}
			npm = append(npm, npmUpdateTarget{ref, pkg.Source.Source, pkg.Scope == "project"})
		case "git":
			git = append(git, pkg)
		}
	}
	checks := make([]func() error, len(npm))
	shouldUpdate := make([]bool, len(npm))
	for i, target := range npm {
		checks[i] = func() error { shouldUpdate[i] = shouldUpdateNpmSource(cwd, sm, target.ref, target.local); return nil }
	}
	if err := runPackageTasks(checks, nil); err != nil {
		return err
	}
	var batches []npmUpdateBatch
	// Pi starts user npm batches before project npm batches, then Git work.
	for _, local := range []bool{false, true} {
		for i, target := range npm {
			if !shouldUpdate[i] || target.local != local {
				continue
			}
			root := NpmInstallRoot(cwd, sm.AgentDir(), target.ref, local)
			index := -1
			for j, batch := range batches {
				if batch.root == root && batch.local == local {
					index = j
					break
				}
			}
			if index < 0 {
				index = len(batches)
				batches = append(batches, npmUpdateBatch{root: root, registry: target.ref.NPMRegistry, local: local})
			}
			batches[index].sources = append(batches[index].sources, target)
		}
	}
	var progressMu sync.Mutex
	report := func(event ProgressEvent) {
		progressMu.Lock()
		defer progressMu.Unlock()
		EmitProgress(progress, event)
	}
	var tasks []func() error
	for _, batch := range batches {
		label := "user npm packages"
		if batch.local {
			label = "project npm packages"
		}
		if len(batch.sources) == 1 {
			label = batch.sources[0].source
		}
		report(ProgressEvent{Type: "start", Action: "update", Source: label, Message: new(fmt.Sprintf("Updating %s...", label))})
		tasks = append(tasks, func() error {
			return FinishPackageProgress(report, "update", label, installNpmBatch(sm, batch))
		})
	}
	if len(git) > 0 {
		tasks = append(tasks, func() error {
			groups := groupGitUpdatesByCheckout(cwd, sm.AgentDir(), git)
			start := func(pkg ConfiguredPackage) {
				source := pkg.Source.Source
				report(ProgressEvent{Type: "start", Action: "update", Source: source, Message: new(fmt.Sprintf("Updating %s...", source))})
			}
			gitTasks := make([]func() error, len(groups))
			for i, group := range groups {
				gitTasks[i] = func() error {
					// Packages of one repository share a managed checkout and each moves it to its own ref, so they run one at a time in settings order and the last one decides the checked-out commit.
					var first error
					for j, pkg := range group {
						if j > 0 {
							start(pkg)
						}
						err := FinishPackageProgress(report, "update", pkg.Source.Source, InstallManagedGit(cwd, sm.AgentDir(), sm, pkg.Source.Source, pkg.Scope == "project"))
						if err != nil && first == nil {
							first = err
						}
					}
					return first
				}
			}
			return runPackageTasks(gitTasks, func(i int) { start(groups[i][0]) })
		})
	}
	return runPackageTasks(tasks, nil)
}

// groupGitUpdatesByCheckout collects Git packages that resolve to one managed checkout, in input order, with groups ordered by their first package. Pi's updateConfiguredSources gives each package its own task and so races two updates of one checkout (package-manager.ts:1171-1181, 1931-1945): both fetch into one FETCH_HEAD and both run `git reset --hard`, which fails on index.lock. A source whose checkout path cannot be computed stays alone, so InstallManagedGit reports its error for that package only.
func groupGitUpdatesByCheckout(cwd, agentDir string, git []ConfiguredPackage) [][]ConfiguredPackage {
	var groups [][]ConfiguredPackage
	byCheckout := map[string]int{}
	for _, pkg := range git {
		checkout, err := GitCheckoutPath(cwd, agentDir, pkg.Source.Source, pkg.Scope == "project")
		if err == nil {
			if i, shared := byCheckout[checkout]; shared {
				groups[i] = append(groups[i], pkg)
				continue
			}
			byCheckout[checkout] = len(groups)
		}
		groups = append(groups, []ConfiguredPackage{pkg})
	}
	return groups
}

func installNpmBatch(sm *codingagent.SettingsManager, batch npmUpdateBatch) error {
	if batch.local && !sm.IsProjectTrusted() {
		return fmt.Errorf("Project is not trusted; refusing to access project package storage")
	}
	if err := EnsureManagedPackageRoot(batch.root); err != nil {
		return err
	}
	specs := make([]string, len(batch.sources))
	for i, target := range batch.sources {
		specs[i] = target.ref.Locator
		if target.ref.NPMVer == "" {
			specs[i] = target.ref.NPMName + "@latest"
		}
	}
	command := DefaultNpmCommand(sm)
	args := append([]string{}, command[1:]...)
	// The single-source argument builder owns npm/pnpm/Bun options for both paths.
	manager, err := PackageManagerName(command)
	if err != nil {
		return err
	}
	installArgs := NpmInstallArgs(manager, specs[0], batch.root, batch.registry)
	args = append(args, installArgs[0])
	args = append(args, specs...)
	args = append(args, installArgs[2:]...)
	return RunPackageProcess("", command[0], args...)
}

// runPackageTasks joins every started operation and reports the first rejection. Start callbacks run in input order before work leaves the queue; metadata and Git pools use Pi's bounded concurrency.
func runPackageTasks(tasks []func() error, onStart func(int)) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	next := 0
	var firstErr error
	for range min(packageUpdateConcurrency, len(tasks)) {
		wg.Go(func() {
			for {
				mu.Lock()
				i := next
				if i >= len(tasks) {
					mu.Unlock()
					return
				}
				next++
				if onStart != nil {
					onStart(i)
				}
				mu.Unlock()
				if err := tasks[i](); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
			}
		})
	}
	wg.Wait()
	return firstErr
}
