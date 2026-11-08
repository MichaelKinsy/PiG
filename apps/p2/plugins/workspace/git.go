package workspace

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// gitRun is a captured git command.
type gitRun struct {
	exitCode  int
	stdout    string
	stderr    string
	truncated bool
}

// filterFuture is the filter overrides of one request or of the watch's lifetime.
type filterFuture struct {
	done  chan struct{}
	value []string
	err   error
}

// git runs git without a shell, against untrusted repository configuration: no optional locks (never blocks the agent's
// git), no network fetches, no prompts, no pager, no system config, no fsmonitor, literal pathspecs and every configured
// clean/smudge filter disabled. Stdout and stderr are captured separately; stdout beyond `limit` cancels the command and
// is reported as truncated. cwd defaults to the root.
func (w *Workspace) git(ctx context.Context, args []string, limit int, cwd string) (*gitRun, error) {
	return w.gitWith(ctx, w.filterOverrides(ctx), args, limit, cwd)
}

// gitWith is git with `filters` fetched by this request already.
func (w *Workspace) gitWith(ctx context.Context, filters *filterFuture, args []string, limit int, cwd string) (*gitRun, error) {
	select {
	case <-filters.done:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	if filters.err != nil {
		return nil, filters.err
	}
	argv := []string{"git", "--no-optional-locks"}
	// check-ignore rejects the flag; it matches its arguments literally anyway. grep gives each pathspec explicit magic
	// for its file globs.
	if args[0] != "check-ignore" && args[0] != "grep" {
		argv = append(argv, "--literal-pathspecs")
	}
	argv = append(argv, "-c", "core.fsmonitor=false", "-c", "core.quotepath=off", "-c", "core.pager=cat")
	argv = append(argv, filters.value...)
	argv = append(argv, args...)
	return w.capture(ctx, argv, limit, cwd)
}

var filterDriver = regexp.MustCompile(`^filter\.(.+)\.[^.]+$`)

// filterOverrides: clean/process filters run on stat-dirty files during status and diff. Override every configured driver
// so repository config cannot run programs from the panel. Kept between requests only while a watcher would report a
// configuration change; otherwise only requests in flight together share them.
func (w *Workspace) filterOverrides(ctx context.Context) *filterFuture {
	w.mu.Lock()
	if w.filters != nil {
		future := w.filters
		w.mu.Unlock()
		return future
	}
	future := &filterFuture{done: make(chan struct{})}
	w.filters = future
	w.mu.Unlock()
	// The first caller's goroutine reads them; later callers wait on `done`.
	func() {
		defer close(future.done)
		run, err := w.capture(ctx, []string{"git", "config", "--name-only", "--get-regexp", "^filter\\."}, 64*1024, "")
		if err != nil {
			future.err = err
			return
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(run.stdout, "\n") {
			if match := filterDriver.FindStringSubmatch(strings.TrimSpace(line)); match != nil && !seen[match[1]] {
				seen[match[1]] = true
				name := match[1]
				future.value = append(future.value,
					"-c", "filter."+name+".clean=", "-c", "filter."+name+".smudge=",
					"-c", "filter."+name+".process=", "-c", "filter."+name+".required=false")
			}
		}
	}()
	w.mu.Lock()
	if w.filters == future && (future.err != nil || w.watchers == 0) {
		w.filters = nil
	}
	w.mu.Unlock()
	return future
}

// capture runs argv in the environment with the output bounded.
func (w *Workspace) capture(ctx context.Context, argv []string, limit int, cwd string) (*gitRun, error) {
	if cwd == "" {
		root, err := w.canonicalRoot(ctx)
		if err != nil {
			return nil, err
		}
		cwd = root
	}
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var mu sync.Mutex
	var stdout, stderr strings.Builder
	stdoutUnits := 0
	truncated := false
	timeout := gitTimeout
	result, err := w.Env.Exec(run, argv, &durableenv.ShellExecOptions{
		Cwd: cwd,
		Env: map[string]string{
			"GIT_OPTIONAL_LOCKS": "0", "GIT_NO_LAZY_FETCH": "1", "GIT_TERMINAL_PROMPT": "0",
			"GIT_CONFIG_NOSYSTEM": "1", "GIT_PAGER": "cat", "LC_ALL": "C",
		},
		Timeout: &timeout,
		OnOutput: func(_ context.Context, text string, info durableenv.ShellOutputInfo) {
			mu.Lock()
			defer mu.Unlock()
			if info.Stream == durableenv.ShellStderr {
				if stderr.Len() < 16*1024 {
					stderr.WriteString(text)
				}
				return
			}
			if truncated {
				return
			}
			units := utf16Units(text)
			if stdoutUnits+units > limit {
				text = truncateUnits(text, limit-stdoutUnits)
				stdout.WriteString(text)
				truncated = true
				cancel(errors.New("Output limit reached"))
				return
			}
			stdout.WriteString(text)
			stdoutUnits += units
		},
	})
	mu.Lock()
	defer mu.Unlock()
	if err == nil {
		return &gitRun{exitCode: result.ExitCode, stdout: stdout.String(), stderr: stderr.String(), truncated: truncated}, nil
	}
	if truncated {
		return &gitRun{exitCode: 0, stdout: stdout.String(), stderr: stderr.String(), truncated: true}, nil
	}
	var failure *durableenv.ExecutionError
	if errors.As(err, &failure) {
		switch failure.Code {
		case durableenv.ExecutionErrorTimeout:
			return nil, errors.New("Git timed out")
		case durableenv.ExecutionErrorSpawnError:
			return nil, errors.New("Git is not installed in this environment")
		}
	}
	return nil, err
}

func utf16Units(text string) int { return p2runtime.JSLength(text) }
