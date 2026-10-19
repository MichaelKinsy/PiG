package durabletest

// Ports packages/durable/src/testing/env-conformance.ts
// Ports packages/durable/src/testing/types.ts (EnvConformance types)
// Ports packages/durable/src/testing/runner.ts (registerEnvConformance)
//
// Go mapping: upstream's cases await Promises; these block on the environment call. A failed check or an unexpected
// error ends the case. Cases that upstream gives a timeout (watch cases wait up to three seconds per step) carry it as
// EnvConformanceCase.Timeout, which a runner without a default timeout may ignore.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// EnvConformanceAssertions are the checks an environment conformance case makes.
type EnvConformanceAssertions = StorageConformanceAssertions

// EnvConformanceProvider supplies an environment whose Cwd is a fresh, empty, writable directory, calls use once with
// it, and then cleans up. It returns use's error.
type EnvConformanceProvider func(use func(executionEnv env.ExecutionEnv) error) error

// EnvConformanceOptions configures CreateEnvConformance.
type EnvConformanceOptions struct {
	Assertions EnvConformanceAssertions
	WithEnv    EnvConformanceProvider
	// Shell is the program and flag that run a POSIX shell script from the next argument; default {"sh", "-c"}.
	Shell []string
	// Symlinks is false when the shell's `ln -s` does not create symbolic links; nil means true (env-conformance.ts symlinks, default true).
	Symlinks *bool
	// ListFiles is whether the environment implements the optional env.FileLister, which the suite then checks; default false.
	ListFiles bool
}

// EnvConformanceCase is one named environment conformance case.
type EnvConformanceCase struct {
	Name string
	// Timeout is how long a case that waits for an environment's watch latency needs; zero uses the runner's default.
	Timeout time.Duration
	Run     func() error
}

// watchWait is how long a watch case waits for a change, as upstream's three seconds.
const watchWait = 3 * time.Second

func abortedConformanceContext() context.Context {
	ctx, cancel := context.WithCancel(conformanceContext)
	cancel()
	return ctx
}

// errorCode is the code of a FileError or ExecutionError; empty for success.
func errorCode(err error) string {
	if fileErr, ok := errors.AsType[*env.FileError](err); ok {
		return string(fileErr.Code)
	}
	if executionErr, ok := errors.AsType[*env.ExecutionError](err); ok {
		return string(executionErr.Code)
	}
	if err != nil {
		return "unknown"
	}
	return ""
}

// readAllPages reads a directory page by page.
func readAllPages(executionEnv env.ExecutionEnv, path string, maxEntries int) (pages [][]env.FileInfo, done bool) {
	reader := must(executionEnv.OpenDirReader(conformanceContext, path))
	defer func() { _ = reader.Close(conformanceContext) }()
	for range 1000 {
		next := must(reader.Next(conformanceContext, maxEntries))
		pages = append(pages, next.Entries)
		if next.Done {
			return pages, true
		}
	}
	return pages, false
}

// listedFiles is a listing as `<kind letter> <path>` lines (`f`, `d`, `l`), with its truncation.
type listedFiles struct {
	Entries   []string
	Truncated bool
}

// listed is env-conformance.ts listed: the environment's ListFiles result as lines.
func listed(executionEnv env.ExecutionEnv, path string, options *env.ListFilesOptions) listedFiles {
	listing, err := executionEnv.ListFiles(conformanceContext, path, options)
	if fileErr, ok := errors.AsType[*env.FileError](err); ok && fileErr.Code == env.FileErrorNotSupported {
		panic(caseFailure{err: errors.New("The environment does not implement listFiles")})
	}
	check(err)
	letters := map[env.FileKind]string{env.FileKindFile: "f", env.FileKindDirectory: "d", env.FileKindSymlink: "l"}
	result := listedFiles{Entries: []string{}, Truncated: listing.Truncated}
	for _, entry := range listing.Entries {
		result.Entries = append(result.Entries, letters[entry.Kind]+" "+entry.Path)
	}
	return result
}

// covers reports whether a change reports path: an overflow, or a reported path at or above it.
func covers(change env.WatchChange, path string) bool {
	switch typed := change.(type) {
	case env.WatchChangeOverflow:
		return true
	case env.WatchChangePaths:
		return slices.ContainsFunc(typed.Paths, func(reported string) bool {
			return path == reported || strings.HasPrefix(path, reported+"/") || strings.HasPrefix(path, reported+`\`)
		})
	}
	return false
}

// watchLog collects the changes a watcher reports.
type watchLog struct {
	mu      sync.Mutex
	changes []env.WatchChange
}

func (log *watchLog) add(change env.WatchChange) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.changes = append(log.changes, change)
}

func (log *watchLog) all() []env.WatchChange {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.changes)
}

type watchHelpers struct {
	executionEnv env.ExecutionEnv
	log          *watchLog
}

// expectChange waits up to three seconds for a change, reported after the call, that reports path.
func (helpers watchHelpers) expectChange(path string, change func()) {
	target := must(helpers.executionEnv.AbsolutePath(conformanceContext, path))
	from := len(helpers.log.all())
	change()
	deadline := time.Now().Add(watchWait)
	for !slices.ContainsFunc(helpers.log.all()[from:], func(entry env.WatchChange) bool { return covers(entry, target) }) {
		if time.Now().After(deadline) {
			panic(caseFailure{err: fmt.Errorf("No change reported %s; got %v", target, helpers.log.all()[from:])})
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (helpers watchHelpers) absolute(path string) string {
	return must(helpers.executionEnv.AbsolutePath(conformanceContext, path))
}

// watching watches targets while run changes files.
func watching(executionEnv env.ExecutionEnv, targets []env.WatchTarget, run func(helpers watchHelpers)) {
	log := &watchLog{}
	watcher := must(executionEnv.Watch(conformanceContext, targets, log.add))
	defer func() { _ = watcher.Close(conformanceContext) }()
	run(watchHelpers{executionEnv: executionEnv, log: log})
}

// decodeRange is new TextDecoder("utf-8", { ignoreBOM: from > 0 }).decode(data[from:to]): only a range that starts the
// file drops a byte-order mark.
func decodeRange(data []byte, from, to int64) string {
	text := jsstring.FromUTF8(data[from:to])
	if from == 0 {
		return strings.TrimPrefix(text, "\ufeff")
	}
	return text
}

// collected is the output of one command, by stream.
type collected struct {
	err            error
	exitCode       int
	stdout, stderr string
}

func execCollect(executionEnv env.ExecutionEnv, command any, cwd string) collected {
	var mu sync.Mutex
	var stdout, stderr strings.Builder
	result, err := executionEnv.Exec(conformanceContext, command, &env.ShellExecOptions{
		Cwd: cwd,
		OnOutput: func(_ context.Context, text string, info env.ShellOutputInfo) {
			mu.Lock()
			defer mu.Unlock()
			if info.Stream == env.ShellStderr {
				stderr.WriteString(text)
			} else {
				stdout.WriteString(text)
			}
		},
	})
	mu.Lock()
	defer mu.Unlock()
	return collected{err: err, exitCode: result.ExitCode, stdout: stdout.String(), stderr: stderr.String()}
}

// EnvConformanceRegisterOptions is Pick<EnvConformanceOptions, "shell" | "symlinks" | "listFiles">, the options of RegisterEnvConformance.
type EnvConformanceRegisterOptions struct {
	// Shell is the program and flag that run a POSIX shell script from the next argument; default {"sh", "-c"}.
	Shell []string
	// Symlinks is false when the shell's `ln -s` does not create symbolic links; nil means true.
	Symlinks *bool
	// ListFiles is whether the environment implements the optional env.FileLister, which the suite then checks; default false.
	ListFiles bool
}

// conformanceOptions is `{ ...options, assertions, withEnv }` (testing/runner.ts:34).
func (options EnvConformanceRegisterOptions) conformanceOptions(assertions EnvConformanceAssertions, withEnv EnvConformanceProvider) EnvConformanceOptions {
	return EnvConformanceOptions{Assertions: assertions, WithEnv: withEnv, Shell: options.Shell, Symlinks: options.Symlinks, ListFiles: options.ListFiles}
}

// RegisterEnvConformance runs every environment conformance case as a subtest of a test named name (runner-independent
// cases registered with the Go test runner, testing/runner.ts:28).
func RegisterEnvConformance(t *testing.T, name string, withEnv EnvConformanceProvider, options EnvConformanceRegisterOptions) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		conformanceOptions := func(t *testing.T) EnvConformanceOptions {
			return options.conformanceOptions(CreateTestingAssertions(t), withEnv)
		}
		for index, testCase := range CreateEnvConformance(conformanceOptions(t)) {
			t.Run(testCase.Name, func(t *testing.T) {
				if err := CreateEnvConformance(conformanceOptions(t))[index].Run(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

// CreateEnvConformance creates runner-independent cases for an ExecutionEnv. WithEnv must call its callback exactly
// once per case.
func CreateEnvConformance(options EnvConformanceOptions) []EnvConformanceCase {
	ctx := conformanceContext
	assert := options.Assertions
	shell := options.Shell
	if shell == nil {
		shell = []string{"sh", "-c"}
	}
	argv := func(script string, args ...string) []string {
		return append(append(slices.Clone(shell), script), args...)
	}
	type envTest func(executionEnv env.ExecutionEnv)
	createCase := func(name string, timeout time.Duration, test envTest) EnvConformanceCase {
		return EnvConformanceCase{Name: name, Timeout: timeout, Run: func() error {
			return options.WithEnv(func(executionEnv env.ExecutionEnv) (err error) {
				defer func() {
					if recovered := recover(); recovered != nil {
						failure, ok := recovered.(caseFailure)
						if !ok {
							panic(recovered)
						}
						err = failure.err
					}
				}()
				test(executionEnv)
				return nil
			})
		}}
	}
	plain := func(name string, test envTest) EnvConformanceCase { return createCase(name, 0, test) }
	// Watch cases wait up to three seconds per step, longer than test runners allow by default.
	watchCase := func(name string, test envTest) EnvConformanceCase { return createCase(name, 30*time.Second, test) }
	text := func(data []byte) string { return string(data) }

	cases := []EnvConformanceCase{
		plain("binary reader reads byte ranges of the opened file", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "data.txt", "hello world"))
			reader := must(executionEnv.OpenBinaryReader(ctx, "data.txt", nil))
			info := must(reader.Info(ctx))
			assert.PartialDeepEqual(info, map[string]any{"name": "data.txt", "kind": "file", "size": 11})
			assert.StrictEqual(text(must(reader.Read(ctx, 0, 5))), "hello")
			assert.StrictEqual(text(must(reader.Read(ctx, 6, 100))), "world")
			assert.StrictEqual(len(must(reader.Read(ctx, 11, 4))), 0)
			assert.StrictEqual(len(must(reader.Read(ctx, 50, 1))), 0)
			assert.StrictEqual(len(must(reader.Read(ctx, 3, 0))), 0)
			_, err := reader.Read(ctx, -1, 1)
			assert.StrictEqual(errorCode(err), "invalid")
			// A fractional length (1.5) cannot be passed to an int64, so the type rejects it.
			_, err = reader.Read(abortedConformanceContext(), 0, 1)
			assert.StrictEqual(errorCode(err), "aborted")
			check(reader.Close(ctx))
			check(reader.Close(ctx))
			_, err = reader.Read(ctx, 0, 1)
			assert.StrictEqual(errorCode(err), "invalid")
			_, err = reader.Info(ctx)
			assert.StrictEqual(errorCode(err), "invalid")
		}),

		plain("binary reader scans lines like decoding the whole file", func(executionEnv env.ExecutionEnv) {
			// A byte-order mark, an invalid sequence before a newline, an empty line, a later U+FEFF, and no final newline.
			data := []byte{0xef, 0xbb, 0xbf, 0x61, 0x0a, 0xe2, 0x82, 0x0a, 0x0a, 0xef, 0xbb, 0xbf, 0x62, 0x0a, 0xc3, 0xa9}
			check(executionEnv.WriteFile(ctx, "lines.txt", data))
			lines := strings.Split(strings.TrimPrefix(jsstring.FromUTF8(data), "\ufeff"), "\n")
			reader := must(executionEnv.OpenBinaryReader(ctx, "lines.txt", nil))
			defer func() { _ = reader.Close(ctx) }()
			type span struct {
				start int64
				end   *int64
			}
			for _, selection := range []span{{0, nil}, {0, new(int64(1))}, {1, new(int64(3))}, {2, new(int64(3))}, {3, nil}, {4, new(int64(9))}} {
				scan := must(reader.ScanLines(ctx, env.ScanLinesOptions{StartLine: selection.start, EndLine: selection.end}))
				first := min(selection.start, int64(len(lines)))
				last := int64(len(lines))
				if selection.end != nil {
					last = min(*selection.end, last)
				}
				joined := strings.Join(lines[first:last], "\n")
				assert.StrictEqual(scan.Newlines, len(lines)-1)
				assert.StrictEqual(decodeRange(data, scan.Start, scan.End), joined)
				assert.StrictEqual(scan.SelectedBytes, len(joined))
				assert.StrictEqual(decodeRange(data, scan.Start, scan.FirstLineEnd), lines[selection.start])
				assert.StrictEqual(scan.FirstLineBytes, len(lines[selection.start]))
			}
			past := must(reader.ScanLines(ctx, env.ScanLinesOptions{StartLine: 9}))
			assert.StrictEqual(past.Start, len(data))
			assert.StrictEqual(past.End, len(data))
			assert.StrictEqual(past.SelectedBytes, 0)
			_, err := reader.ScanLines(ctx, env.ScanLinesOptions{StartLine: 2, EndLine: new(int64(2))})
			assert.StrictEqual(errorCode(err), "invalid")
		}),

		plain("binary reader keeps reading the file it opened after a rename", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "a.txt", "one"))
			reader := must(executionEnv.OpenBinaryReader(ctx, "a.txt", nil))
			defer func() { _ = reader.Close(ctx) }()
			check(executionEnv.RenameFile(ctx, "a.txt", "b.txt"))
			check(executionEnv.WriteFile(ctx, "a.txt", "two"))
			assert.StrictEqual(text(must(reader.Read(ctx, 0, 10))), "one")
		}),

		plain("binary reader refuses directories, missing files and aborted opens", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.CreateDir(ctx, "dir", nil))
			check(executionEnv.WriteFile(ctx, "file.txt", "x"))
			_, err := executionEnv.OpenBinaryReader(ctx, "dir", nil)
			assert.StrictEqual(errorCode(err), "is_directory")
			_, err = executionEnv.OpenBinaryReader(ctx, "missing.txt", nil)
			assert.StrictEqual(errorCode(err), "not_found")
			_, err = executionEnv.OpenBinaryReader(abortedConformanceContext(), "file.txt", nil)
			assert.StrictEqual(errorCode(err), "aborted")
		}),

		plain("directory reader pages every entry exactly once", func(executionEnv env.ExecutionEnv) {
			names := []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"}
			for _, name := range names {
				check(executionEnv.WriteFile(ctx, name, name))
			}
			check(executionEnv.CreateDir(ctx, "sub", nil))
			pages, done := readAllPages(executionEnv, ".", 2)
			assert.Ok(done, "directory reader reached the end")
			var entries []env.FileInfo
			for _, page := range pages {
				assert.Ok(len(page) <= 2, "page within maxEntries")
				entries = append(entries, page...)
			}
			entryNames := make([]string, len(entries))
			for index, entry := range entries {
				entryNames[index] = entry.Name
			}
			slices.Sort(entryNames)
			expected := append(slices.Clone(names), "sub")
			slices.Sort(expected)
			assert.DeepEqual(entryNames, expected)
			for _, entry := range entries {
				switch entry.Name {
				case "sub":
					assert.StrictEqual(entry.Kind, env.FileKindDirectory)
				case "a.txt":
					assert.PartialDeepEqual(entry, map[string]any{"kind": "file", "size": 5})
				}
			}
		}),

		plain("directory reader reports the end and refuses use after close", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.CreateDir(ctx, "empty", nil))
			reader := must(executionEnv.OpenDirReader(ctx, "empty"))
			for range 2 {
				page := must(reader.Next(ctx, 10))
				assert.StrictEqual(len(page.Entries), 0)
				assert.StrictEqual(page.Done, true)
			}
			_, err := reader.Next(ctx, 0)
			assert.StrictEqual(errorCode(err), "invalid")
			_, err = reader.Next(abortedConformanceContext(), 1)
			assert.StrictEqual(errorCode(err), "aborted")
			check(reader.Close(ctx))
			check(reader.Close(ctx))
			_, err = reader.Next(ctx, 1)
			assert.StrictEqual(errorCode(err), "invalid")
		}),

		plain("directory reader refuses missing paths and files", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "file.txt", "x"))
			_, err := executionEnv.OpenDirReader(ctx, "missing")
			assert.StrictEqual(errorCode(err), "not_found")
			_, err = executionEnv.OpenDirReader(ctx, "file.txt")
			assert.StrictEqual(errorCode(err), "not_directory")
			_, err = executionEnv.OpenDirReader(abortedConformanceContext(), ".")
			assert.StrictEqual(errorCode(err), "aborted")
		}),

		plain("directory reader skips entries removed during enumeration", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.CreateDir(ctx, "dir", nil))
			for _, name := range []string{"x", "y", "z"} {
				check(executionEnv.WriteFile(ctx, "dir/"+name, name))
			}
			reader := must(executionEnv.OpenDirReader(ctx, "dir"))
			defer func() { _ = reader.Close(ctx) }()
			for _, name := range []string{"x", "y", "z"} {
				check(executionEnv.Remove(ctx, "dir/"+name, nil))
			}
			entries := []env.FileInfo{}
			for range 10 {
				next := must(reader.Next(ctx, 10))
				entries = append(entries, next.Entries...)
				if next.Done {
					break
				}
			}
			assert.StrictEqual(len(entries), 0)
		}),

		watchCase("watch reports a missing file's creation, changes, replacement and removal", func(executionEnv env.ExecutionEnv) {
			watching(executionEnv, []env.WatchTarget{{Path: "AGENTS.md"}}, func(helpers watchHelpers) {
				helpers.expectChange("AGENTS.md", func() { check(executionEnv.WriteFile(ctx, "AGENTS.md", "one")) })
				helpers.expectChange("AGENTS.md", func() { check(executionEnv.WriteFile(ctx, "AGENTS.md", "two!")) })
				// Editors replace a file by renaming a new one over it.
				helpers.expectChange("AGENTS.md", func() {
					check(executionEnv.WriteFile(ctx, "AGENTS.md.tmp", "three"))
					check(executionEnv.RenameFile(ctx, "AGENTS.md.tmp", "AGENTS.md"))
				})
				helpers.expectChange("AGENTS.md", func() { check(executionEnv.WriteFile(ctx, "AGENTS.md", "four")) })
				helpers.expectChange("AGENTS.md", func() { check(executionEnv.Remove(ctx, "AGENTS.md", nil)) })
			})
		}),

		watchCase("watch reports a missing target whose ancestors are created", func(executionEnv env.ExecutionEnv) {
			watching(executionEnv, []env.WatchTarget{{Path: "a/b/c/AGENTS.md"}}, func(helpers watchHelpers) {
				helpers.expectChange("a/b/c/AGENTS.md", func() { check(executionEnv.WriteFile(ctx, "a/b/c/AGENTS.md", "x")) })
			})
		}),

		watchCase("watch follows directories created together with their contents", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.CreateDir(ctx, "skills", nil))
			watching(executionEnv, []env.WatchTarget{{Path: "skills", Recursive: true}}, func(helpers watchHelpers) {
				// Written before any watcher on the new directories can exist.
				helpers.expectChange("skills/a/b/SKILL.md", func() { check(executionEnv.WriteFile(ctx, "skills/a/b/SKILL.md", "one")) })
				helpers.expectChange("skills/a/b/SKILL.md", func() { check(executionEnv.WriteFile(ctx, "skills/a/b/SKILL.md", "two!")) })
				helpers.expectChange("skills/a/b/c/SKILL.md", func() { check(executionEnv.WriteFile(ctx, "skills/a/b/c/SKILL.md", "deeper")) })
			})
		}),

		watchCase("watch keeps watching a path whose parent is renamed and recreated", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "proj/.pi/skills/x.md", "x"))
			watching(executionEnv, []env.WatchTarget{{Path: "proj/.pi/skills", Recursive: true}}, func(helpers watchHelpers) {
				helpers.expectChange("proj/.pi/skills", func() { check(executionEnv.RenameFile(ctx, "proj/.pi", "proj/old")) })
				helpers.expectChange("proj/.pi/skills/y.md", func() { check(executionEnv.WriteFile(ctx, "proj/.pi/skills/y.md", "y")) })
				helpers.expectChange("proj/.pi/skills/y.md", func() { check(executionEnv.WriteFile(ctx, "proj/.pi/skills/y.md", "yy")) })
			})
		}),

		watchCase("watch skips excluded entries and reports a rename out of them", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.CreateDir(ctx, "skills", nil))
			targets := []env.WatchTarget{{Path: "skills", Recursive: true, Exclude: &env.WatchExclude{Hidden: true, Names: []string{"node_modules"}}}}
			watching(executionEnv, targets, func(helpers watchHelpers) {
				check(executionEnv.WriteFile(ctx, "skills/node_modules/dep/SKILL.md", "dep"))
				check(executionEnv.WriteFile(ctx, "skills/.SKILL.md.tmp", "draft"))
				helpers.expectChange("skills/SKILL.md", func() { check(executionEnv.RenameFile(ctx, "skills/.SKILL.md.tmp", "skills/SKILL.md")) })
				hidden := []string{helpers.absolute("skills/node_modules"), helpers.absolute("skills/.SKILL.md.tmp")}
				for _, change := range helpers.log.all() {
					paths, ok := change.(env.WatchChangePaths)
					if !ok {
						continue
					}
					for _, path := range paths.Paths {
						assert.Ok(!slices.ContainsFunc(hidden, func(excluded string) bool { return strings.HasPrefix(path, excluded) }), "excluded "+path)
					}
				}
			})
		}),

		watchCase("watch keeps recursive coverage where a non-recursive target overlaps", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "skills/a/one.md", "one"))
			targets := []env.WatchTarget{{Path: "skills"}, {Path: "skills", Recursive: true}}
			watching(executionEnv, targets, func(helpers watchHelpers) {
				helpers.expectChange("skills/a/two.md", func() { check(executionEnv.WriteFile(ctx, "skills/a/two.md", "two")) })
			})
		}),

		watchCase("watch follows a directory replaced at the same path", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "skills/a/x.md", "x"))
			watching(executionEnv, []env.WatchTarget{{Path: "skills", Recursive: true}}, func(helpers watchHelpers) {
				helpers.expectChange("skills/a", func() {
					check(executionEnv.RenameFile(ctx, "skills/a", "skills-old"))
					check(executionEnv.CreateDir(ctx, "skills/a", nil))
				})
				helpers.expectChange("skills/a/y.md", func() { check(executionEnv.WriteFile(ctx, "skills/a/y.md", "y")) })
				helpers.expectChange("skills/a/y.md", func() { check(executionEnv.WriteFile(ctx, "skills/a/y.md", "yy")) })
			})
		}),

		watchCase("watch stops reporting once closed", func(executionEnv env.ExecutionEnv) {
			log := &watchLog{}
			watcher := must(executionEnv.Watch(ctx, []env.WatchTarget{{Path: "file.txt"}}, log.add))
			assert.Ok(watcher.Mode() == env.WatchNative || watcher.Mode() == env.WatchPolling, "watcher reports its mode")
			check(watcher.Close(ctx))
			check(watcher.Close(ctx))
			check(executionEnv.WriteFile(ctx, "file.txt", "x"))
			time.Sleep(300 * time.Millisecond)
			assert.StrictEqual(len(log.all()), 0)
		}),

		plain("argv exec passes arguments to the program without shell parsing", func(executionEnv env.ExecutionEnv) {
			hostile := "it's $(touch pwned) `touch pwned` *; touch pwned"
			result := execCollect(executionEnv, argv(`printf "%s|%s" "$1" "$2"`, "argv0", hostile, "a b"), "")
			check(result.err)
			assert.StrictEqual(result.exitCode, 0)
			assert.StrictEqual(result.stdout, hostile+"|a b")
			assert.StrictEqual(must(executionEnv.Exists(ctx, "pwned")), false)
		}),

		plain("exec reports the stream of every chunk in both forms", func(executionEnv env.ExecutionEnv) {
			script := "printf out; printf err >&2; printf more"
			for _, command := range []any{argv(script), script} {
				result := execCollect(executionEnv, command, "")
				check(result.err)
				assert.StrictEqual(result.exitCode, 0)
				assert.StrictEqual(result.stdout, "outmore")
				assert.StrictEqual(result.stderr, "err")
			}
		}),

		plain("argv exec honors cwd and exit codes", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.CreateDir(ctx, "sub", nil))
			made := execCollect(executionEnv, argv("printf x > made.txt; exit 3"), "sub")
			check(made.err)
			assert.StrictEqual(made.exitCode, 3)
			assert.StrictEqual(must(executionEnv.ReadTextFile(ctx, "sub/made.txt")), "x")
		}),

		plain("argv exec reports missing programs and empty argv as spawn errors", func(executionEnv env.ExecutionEnv) {
			_, err := executionEnv.Exec(ctx, []string{"pi-durable-conformance-missing-program"}, nil)
			assert.StrictEqual(errorCode(err), "spawn_error")
			_, err = executionEnv.Exec(ctx, []string{}, nil)
			assert.StrictEqual(errorCode(err), "spawn_error")
		}),

		plain("windowed exec keeps the exact tail and counts what it skips", func(executionEnv env.ExecutionEnv) {
			const lines = 2000
			window := env.ShellOutputWindow{MaxBytes: 200, MaxLines: 5, MinIntervalMs: 0, BytesPerSecond: 1_000_000_000}
			var mu sync.Mutex
			byteCount, newlines := 0, 0
			skipNotFollowedByAWindow := false
			var tail strings.Builder
			result, err := executionEnv.Exec(ctx, argv(fmt.Sprintf("i=0; while [ $i -lt %d ]; do echo line-$i; i=$((i+1)); done", lines)), &env.ShellExecOptions{
				Window: &window,
				OnOutput: func(_ context.Context, chunk string, info env.ShellOutputInfo) {
					mu.Lock()
					defer mu.Unlock()
					if info.Skipped != nil {
						byteCount += info.Skipped.Bytes
						newlines += info.Skipped.Newlines
						// The check runs after the command: a failed assertion must not end the test from this goroutine.
						if len(chunk) <= window.MaxBytes && strings.Count(chunk, "\n") <= window.MaxLines {
							skipNotFollowedByAWindow = true
						}
						tail.Reset()
					}
					byteCount += len(chunk)
					newlines += strings.Count(chunk, "\n")
					tail.WriteString(chunk)
				},
			})
			check(err)
			assert.StrictEqual(result.ExitCode, 0)
			expected := make([]string, lines)
			for index := range expected {
				expected[index] = fmt.Sprintf("line-%d\n", index)
			}
			mu.Lock()
			defer mu.Unlock()
			assert.Ok(!skipNotFollowedByAWindow, "a skip is followed by more than the window")
			assert.StrictEqual(byteCount, len(strings.Join(expected, "")))
			assert.StrictEqual(newlines, lines)
			assert.Ok(strings.HasSuffix(tail.String(), strings.Join(expected[lines-window.MaxLines:], "")), "the delivered output ends with the tail")
		}),

		plain("argv exec distinguishes timeout from abort", func(executionEnv env.ExecutionEnv) {
			timeout := 0.1
			_, err := executionEnv.Exec(ctx, argv("sleep 2"), &env.ShellExecOptions{Timeout: &timeout})
			assert.StrictEqual(errorCode(err), "timeout")
			abortCtx, abort := context.WithCancel(ctx)
			defer abort()
			time.AfterFunc(100*time.Millisecond, abort)
			_, err = executionEnv.Exec(abortCtx, argv("sleep 2"), nil)
			assert.StrictEqual(errorCode(err), "aborted")
		}),
	}

	if options.ListFiles {
		writeAll := func(executionEnv env.ExecutionEnv, files ...string) {
			for _, file := range files {
				check(executionEnv.WriteFile(ctx, file, ""))
			}
		}
		listFiles := func(executionEnv env.ExecutionEnv, path string, listOptions *env.ListFilesOptions) error {
			_, err := executionEnv.ListFiles(ctx, path, listOptions)
			return err
		}
		cases = append(cases,
			plain("listFiles lists files and directories depth first in name order", func(executionEnv env.ExecutionEnv) {
				writeAll(executionEnv, "b.txt", "a/z.txt", "a/b/c.txt", ".hidden/x", "A.txt")
				check(executionEnv.CreateDir(ctx, "empty", nil))
				assert.DeepEqual(listed(executionEnv, ".", nil), listedFiles{
					Entries:   []string{"d .hidden", "f .hidden/x", "f A.txt", "d a", "d a/b", "f a/b/c.txt", "f a/z.txt", "f b.txt", "d empty"},
					Truncated: false,
				})
				assert.DeepEqual(listed(executionEnv, ".", &env.ListFilesOptions{Hidden: new(false)}).Entries,
					[]string{"f A.txt", "d a", "d a/b", "f a/b/c.txt", "f a/z.txt", "f b.txt", "d empty"})
				expected := listedFiles{Entries: []string{"d b", "f b/c.txt", "f z.txt"}, Truncated: false}
				assert.DeepEqual(listed(executionEnv, "a", nil), expected)
				assert.DeepEqual(listed(executionEnv, must(executionEnv.AbsolutePath(ctx, "a")), nil), expected)
				assert.DeepEqual(listed(executionEnv, "empty", nil), listedFiles{Entries: []string{}, Truncated: false})
			}),

			plain("listFiles skips what git ignores in work trees, .git, and excluded names", func(executionEnv env.ExecutionEnv) {
				writeAll(executionEnv, "keep.txt", "out/x.js", "sub/keep.txt", "sub/drop.log", "node_modules/m/i.js")
				check(executionEnv.WriteFile(ctx, ".gitignore", "out/\n*.log\n"))
				check(executionEnv.WriteFile(ctx, "sub/.gitignore", "!keep.txt\n"))
				// Not a work tree yet: `.gitignore` files have no effect.
				assert.Ok(slices.Contains(listed(executionEnv, ".", nil).Entries, "f out/x.js"))
				check(executionEnv.WriteFile(ctx, ".git/HEAD", "ref: refs/heads/main\n"))
				check(executionEnv.WriteFile(ctx, ".git/info/exclude", "keep.txt\n"))
				assert.DeepEqual(listed(executionEnv, ".", &env.ListFilesOptions{Exclude: []string{"node_modules"}}).Entries,
					[]string{"f .gitignore", "d sub", "f sub/.gitignore", "f sub/keep.txt"})
				// A directory inside the work tree applies its parents' rules.
				assert.DeepEqual(listed(executionEnv, "sub", nil).Entries, []string{"f .gitignore", "f keep.txt"})
				all := listed(executionEnv, ".", &env.ListFilesOptions{Gitignore: new(false)}).Entries
				assert.Ok(slices.Contains(all, "f out/x.js") && slices.Contains(all, "f node_modules/m/i.js") && slices.Contains(all, "f keep.txt"))
				assert.Ok(!slices.ContainsFunc(all, func(entry string) bool { return strings.Contains(entry, ".git/") || entry == "d .git" }))
			}),

			plain("listFiles stops at its bounds and reports truncated", func(executionEnv env.ExecutionEnv) {
				writeAll(executionEnv, "a", "b", "c")
				assert.DeepEqual(listed(executionEnv, ".", &env.ListFilesOptions{MaxEntries: new(2.0)}), listedFiles{Entries: []string{"f a", "f b"}, Truncated: true})
				assert.DeepEqual(listed(executionEnv, ".", &env.ListFilesOptions{MaxEntries: new(3.0)}), listedFiles{Entries: []string{"f a", "f b", "f c"}, Truncated: false})
				// Each entry counts its path plus two bytes.
				assert.DeepEqual(listed(executionEnv, ".", &env.ListFilesOptions{MaxBytes: new(6.0)}), listedFiles{Entries: []string{"f a", "f b"}, Truncated: true})
				assert.DeepEqual(listed(executionEnv, ".", &env.ListFilesOptions{MaxEntries: new(0.0)}), listedFiles{Entries: []string{}, Truncated: true})
				assert.StrictEqual(errorCode(listFiles(executionEnv, ".", &env.ListFilesOptions{MaxEntries: new(-1.0)})), "invalid")
				assert.StrictEqual(errorCode(listFiles(executionEnv, ".", &env.ListFilesOptions{TimeoutMs: new(1.5)})), "invalid")
			}),

			plain("listFiles fails on missing directories, files, and aborts", func(executionEnv env.ExecutionEnv) {
				check(executionEnv.WriteFile(ctx, "file.txt", ""))
				assert.StrictEqual(errorCode(listFiles(executionEnv, "missing", nil)), "not_found")
				assert.StrictEqual(errorCode(listFiles(executionEnv, "file.txt", nil)), "not_directory")
				_, err := executionEnv.ListFiles(abortedConformanceContext(), ".", nil)
				assert.StrictEqual(errorCode(err), "aborted")
			}),
		)
	}

	if options.ListFiles && (options.Symlinks == nil || *options.Symlinks) {
		cases = append(cases, plain("listFiles lists symbolic links without following them", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "dir/inner.txt", ""))
			linked, err := executionEnv.Exec(ctx, argv("ln -s dir link && ln -s missing dangling && ln -s .. dir/loop"), nil)
			check(err)
			assert.StrictEqual(linked.ExitCode, 0)
			assert.DeepEqual(listed(executionEnv, ".", nil).Entries, []string{"l dangling", "d dir", "f dir/inner.txt", "l dir/loop", "l link"})
			// The listed directory itself may be a link.
			assert.DeepEqual(listed(executionEnv, "link", nil).Entries, []string{"f inner.txt", "l loop"})
		}))
	}

	if options.Symlinks == nil || *options.Symlinks {
		cases = append(cases, watchCase("watch reports changes to the file a watched symbolic link points to", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "data/real.md", "one"))
			check(executionEnv.CreateDir(ctx, "config", nil))
			linked, err := executionEnv.Exec(ctx, argv("ln -s ../data/real.md config/AGENTS.md"), nil)
			check(err)
			assert.StrictEqual(linked.ExitCode, 0)
			watching(executionEnv, []env.WatchTarget{{Path: "config/AGENTS.md"}}, func(helpers watchHelpers) {
				helpers.expectChange("config/AGENTS.md", func() { check(executionEnv.WriteFile(ctx, "data/real.md", "two!")) })
			})
		}), plain("binary reader follows symlinks unless noFollow refuses the final one", func(executionEnv env.ExecutionEnv) {
			check(executionEnv.WriteFile(ctx, "target.txt", "target"))
			check(executionEnv.CreateDir(ctx, "sub", nil))
			check(executionEnv.WriteFile(ctx, "sub/inner.txt", "inner"))
			linked, err := executionEnv.Exec(ctx, argv("ln -s target.txt link.txt && ln -s sub dirlink"), nil)
			check(err)
			assert.StrictEqual(linked.ExitCode, 0)

			followed := must(executionEnv.OpenBinaryReader(ctx, "link.txt", nil))
			assert.StrictEqual(text(must(followed.Read(ctx, 0, 10))), "target")
			check(followed.Close(ctx))

			_, err = executionEnv.OpenBinaryReader(ctx, "link.txt", &env.OpenBinaryReaderOptions{NoFollow: true})
			assert.StrictEqual(errorCode(err), "invalid")

			// Only the final component is refused; earlier symlinked directories still resolve.
			inner := must(executionEnv.OpenBinaryReader(ctx, "dirlink/inner.txt", &env.OpenBinaryReaderOptions{NoFollow: true}))
			assert.StrictEqual(text(must(inner.Read(ctx, 0, 10))), "inner")
			check(inner.Close(ctx))
		}))
	}
	return cases
}
