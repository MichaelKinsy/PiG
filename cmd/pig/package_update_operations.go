package main

import (
	"fmt"
	"sync"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
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

func updateConfiguredSources(cwd string, sm *codingagent.SettingsManager, packages []packagemanager.ConfiguredPackage, progress packagemanager.ProgressCallback) error {
	if packagemanager.IsOfflineModeEnabled() || len(packages) == 0 {
		return nil
	}
	var npm []npmUpdateTarget
	var git []packagemanager.ConfiguredPackage
	for _, pkg := range packages {
		switch packagemanager.DetectSourceKind(pkg.Source.Source) {
		case "npm":
			if isPinnedNpm(pkg.Source.Source) {
				continue
			}
			ref, err := packagemanager.ParseNpmInstallRef(pkg.Source.Source)
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
			root := packagemanager.NpmInstallRoot(cwd, sm.AgentDir(), target.ref, local)
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
	report := func(event packagemanager.ProgressEvent) {
		progressMu.Lock()
		defer progressMu.Unlock()
		packagemanager.EmitProgress(progress, event)
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
		report(packagemanager.ProgressEvent{Type: "start", Action: "update", Source: label, Message: new(fmt.Sprintf("Updating %s...", label))})
		tasks = append(tasks, func() error {
			return packagemanager.FinishPackageProgress(report, "update", label, installNpmBatch(sm, batch))
		})
	}
	if len(git) > 0 {
		tasks = append(tasks, func() error {
			gitTasks := make([]func() error, len(git))
			for i, pkg := range git {
				gitTasks[i] = func() error {
					return packagemanager.FinishPackageProgress(report, "update", pkg.Source.Source, packagemanager.InstallManagedGit(cwd, sm.AgentDir(), sm, pkg.Source.Source, pkg.Scope == "project"))
				}
			}
			return runPackageTasks(gitTasks, func(i int) {
				source := git[i].Source.Source
				report(packagemanager.ProgressEvent{Type: "start", Action: "update", Source: source, Message: new(fmt.Sprintf("Updating %s...", source))})
			})
		})
	}
	return runPackageTasks(tasks, nil)
}

func installNpmBatch(sm *codingagent.SettingsManager, batch npmUpdateBatch) error {
	if batch.local && !sm.IsProjectTrusted() {
		return fmt.Errorf("Project is not trusted; refusing to access project package storage")
	}
	if err := packagemanager.EnsureManagedPackageRoot(batch.root); err != nil {
		return err
	}
	specs := make([]string, len(batch.sources))
	for i, target := range batch.sources {
		specs[i] = target.ref.Locator
		if target.ref.NPMVer == "" {
			specs[i] = target.ref.NPMName + "@latest"
		}
	}
	command := packagemanager.DefaultNpmCommand(sm)
	args := append([]string{}, command[1:]...)
	// The single-source argument builder owns npm/pnpm/Bun options for both paths.
	installArgs := packagemanager.NpmInstallArgs(packagemanager.NpmCommandName(command), specs[0], batch.root, batch.registry)
	args = append(args, installArgs[0])
	args = append(args, specs...)
	args = append(args, installArgs[2:]...)
	return packagemanager.RunPackageProcess("", command[0], args...)
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
