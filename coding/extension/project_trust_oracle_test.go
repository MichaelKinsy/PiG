package extension_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type projectTrustHandlerScript struct {
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Remember *bool  `json:"remember"`
}

type projectTrustExtensionScript struct {
	Path     string                      `json:"path"`
	Handlers []projectTrustHandlerScript `json:"handlers"`
}

type projectTrustScenario struct {
	CWD        string                        `json:"cwd"`
	Extensions []projectTrustExtensionScript `json:"extensions"`
}

type projectTrustError struct {
	ExtensionPath string `json:"extensionPath"`
	Event         string `json:"event"`
	Error         string `json:"error"`
	HasStack      bool   `json:"hasStack"`
}

type projectTrustOut struct {
	Calls  []string `json:"calls"`
	Result *struct {
		Trusted  string `json:"trusted"`
		Remember *bool  `json:"remember"`
	} `json:"result"`
	Errors []projectTrustError `json:"errors"`
}

// stackedError is a handler failure that carries a stack, as a thrown Error does in Pi.
type stackedError struct{ message string }

func (e stackedError) Error() string      { return e.message }
func (e stackedError) ErrorStack() string { return "Error: " + e.message + "\n    at handler" }

func projectTrustScenarios() []projectTrustScenario {
	rng := rand.New(rand.NewPCG(7, 99))
	messages := []string{"boom", "", "日本語 failure", "line one\nline two", "denied"}
	kinds := []string{"yes", "no", "undecided", "undecided", "throwError", "throwString", "undefined"}
	var scenarios []projectTrustScenario
	for range 500 {
		scenario := projectTrustScenario{CWD: []string{"/project", "/a b/c", "/日本"}[rng.IntN(3)]}
		for e := range rng.IntN(5) {
			ext := projectTrustExtensionScript{Path: fmt.Sprintf("/ext/%d/index.ts", e), Handlers: []projectTrustHandlerScript{}}
			for range rng.IntN(4) {
				handler := projectTrustHandlerScript{Kind: kinds[rng.IntN(len(kinds))], Message: messages[rng.IntN(len(messages))]}
				switch rng.IntN(3) {
				case 0:
					handler.Remember = new(true)
				case 1:
					handler.Remember = new(false)
				}
				ext.Handlers = append(ext.Handlers, handler)
			}
			scenario.Extensions = append(scenario.Extensions, ext)
		}
		if scenario.Extensions == nil {
			scenario.Extensions = []projectTrustExtensionScript{}
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func runProjectTrustWithPig(t *testing.T, scenario projectTrustScenario) projectTrustOut {
	var calls []string
	exts := make([]extension.Extension, 0, len(scenario.Extensions))
	for e, script := range scenario.Extensions {
		var handlers []extension.HandlerFn
		for i, h := range script.Handlers {
			// A project_trust handler is an extension.ProjectTrustHandler: it receives the event, the dispatch context and the trust context.
			var handler extension.ProjectTrustHandler = func(_ context.Context, evt extension.ProjectTrustEvent, trust extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
				calls = append(calls, fmt.Sprintf("%d.%d:%s:%s", e, i, evt.Cwd, trust.Cwd))
				switch h.Kind {
				case "yes", "no", "undecided":
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustEventDecision(h.Kind), Remember: h.Remember}, nil
				case "throwError":
					return extension.ProjectTrustEventResult{}, stackedError{h.Message}
				default:
					return extension.ProjectTrustEventResult{}, errors.New(h.Message)
				}
			}
			handlers = append(handlers, func(args ...any) (any, error) {
				if h.Kind == "undefined" {
					calls = append(calls, fmt.Sprintf("%d.%d:%s:%s", e, i, args[0].(extension.ProjectTrustEvent).Cwd, args[2].(extension.ProjectTrustContext).Cwd))
					return nil, nil
				}
				return handler(args[1].(context.Context), args[0].(extension.ProjectTrustEvent), args[2].(extension.ProjectTrustContext))
			})
		}
		exts = append(exts, extension.Extension{Path: script.Path, ResolvedPath: script.Path, Handlers: map[string][]extension.HandlerFn{"project_trust": handlers}})
	}
	result, errs, err := inproc.EmitProjectTrust(inproc.NewRunner(exts, scenario.CWD), context.Background(),
		extension.ProjectTrustEvent{Type: "project_trust", Cwd: scenario.CWD}, extension.ProjectTrustContext{Cwd: scenario.CWD})
	if err != nil {
		t.Fatal(err)
	}
	out := projectTrustOut{Calls: append([]string{}, calls...), Errors: []projectTrustError{}}
	if result != nil {
		out.Result = &struct {
			Trusted  string `json:"trusted"`
			Remember *bool  `json:"remember"`
		}{string(result.Trusted), result.Remember}
	}
	for _, e := range errs {
		out.Errors = append(out.Errors, projectTrustError{e.ExtensionPath, e.Event, e.Error, e.Stack != ""})
	}
	return out
}

// emitProjectTrustEvent (core/extensions/runner.ts) against pinned Pi over 500 seeded extension sets: handlers run in extension then registration order with the event and trust context, the first yes/no result wins and ends the dispatch, undecided falls through, and a handler that throws is reported with the extension path, the event type, its message and (for an Error) a stack, then the dispatch continues with the next handler.
func TestProjectTrustHandlerDispatchMatchesPi(t *testing.T) {
	scenarios := projectTrustScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/project_trust.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var want []projectTrustOut
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(scenarios) {
		t.Fatalf("Pi returned %d results for %d scenarios", len(want), len(scenarios))
	}
	for i, scenario := range scenarios {
		got := runProjectTrustWithPig(t, scenario)
		if !reflect.DeepEqual(got, want[i]) {
			wantJSON, _ := json.Marshal(want[i])
			gotJSON, _ := json.Marshal(got)
			input, _ := json.Marshal(scenario)
			t.Fatalf("scenario %d\n%s\nPig: %s\nPi:  %s", i, input, gotJSON, wantJSON)
		}
	}
}
