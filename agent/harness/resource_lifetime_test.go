package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/env"
)

type gatedResourceEnv struct {
	*env.NodeExecutionEnv
	started chan struct{}
	release chan struct{}
}

func (e *gatedResourceEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	close(e.started)
	select {
	case <-e.release:
		return e.NodeExecutionEnv.ReadTextFile(ctx, path)
	case <-ctx.Done():
		return "", &harness.FileError{Code: harness.FileErrorAborted, Message: "cancelled read", Path: path, Cause: ctx.Err()}
	}
}

func TestHarnessSourceMapperErrorsStopAtTheFailingInput(t *testing.T) {
	_, e := resourceTestEnv(t, map[string]string{"one.md": "one", "two.md": "two", "one/SKILL.md": "---\ndescription: one\n---\none", "two/SKILL.md": "---\ndescription: two\n---\ntwo"})
	failure := errors.New("mapping failed")
	calls := 0
	_, _, err := harness.LoadSourcedPromptTemplates(t.Context(), e, []harness.SourcedPath[string]{{Path: "one.md", Source: "first"}, {Path: "two.md", Source: "second"}}, func(harness.PromptTemplate, string, context.Context) (any, error) { calls++; return nil, failure })
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("prompt mapping: calls=%d err=%v", calls, err)
	}
	calls = 0
	_, _, err = harness.LoadSourcedSkills(t.Context(), e, []harness.SourcedPath[string]{{Path: "one", Source: "first"}, {Path: "two", Source: "second"}}, func(harness.Skill, string, context.Context) (any, error) { calls++; return nil, failure })
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("skill mapping: calls=%d err=%v", calls, err)
	}
}

func TestHarnessResourceLoadsAwaitReadsAndRetainCancellation(t *testing.T) {
	for _, kind := range []string{"prompt", "skill"} {
		for _, abort := range []bool{false, true} {
			name := kind + "/complete"
			if abort {
				name = kind + "/cancel"
			}
			t.Run(name, func(t *testing.T) {
				path := "one.md"
				content := "body"
				if kind == "skill" {
					path = "one/SKILL.md"
					content = "---\ndescription: skill\n---\nbody"
				}
				_, base := resourceTestEnv(t, map[string]string{path: content})
				e := &gatedResourceEnv{NodeExecutionEnv: base, started: make(chan struct{}), release: make(chan struct{})}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				type result struct {
					body     string
					warnings []harness.PromptTemplateDiagnostic
				}
				done := make(chan result, 1)
				go func() {
					if kind == "prompt" {
						values, warnings := harness.LoadPromptTemplates(ctx, e, []string{path})
						body := ""
						if len(values) > 0 {
							body = values[0].Content
						}
						done <- result{body, warnings}
					} else {
						values, warnings := harness.LoadSkills(ctx, e, []string{"one"})
						body := ""
						if len(values) > 0 {
							body = values[0].Content
						}
						done <- result{body, warnings}
					}
				}()
				select {
				case <-e.started:
				case result := <-done:
					t.Fatalf("returned before read: %#v", result)
				}
				select {
				case result := <-done:
					t.Fatalf("returned while read was blocked: %#v", result)
				default:
				}
				if abort {
					cancel()
				} else {
					close(e.release)
				}
				got := <-done
				if abort {
					if got.body != "" || len(got.warnings) != 1 || got.warnings[0].Code != "read_failed" || got.warnings[0].Message != "cancelled read" {
						t.Fatalf("cancelled load=%#v", got)
					}
				} else if got.body != "body" || len(got.warnings) != 0 {
					t.Fatalf("completed load=%#v", got)
				}
			})
		}
	}
}
