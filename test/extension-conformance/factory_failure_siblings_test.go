package extensionconformance

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi loads each extension's factory on its own and records a factory that throws as that extension's load error (loader.ts:623-629); the extensions
// before and after it stay loaded. A packed runtime cell shares one process among its members, so a factory that fails there must stay that member's
// load error, not take the process, and with it every sibling, down. The failing member is reported as an isolated one is, whatever the factory's
// language and whether it had called a host helper (parseFrontmatter, Date.parse, a pi.events call: which dials the host before the factory returns), and
// a /reload loads the siblings again. When two members of one cell fail, each load error carries its own cause.

// factoryFailure is how a test factory fails.
type factoryFailure string

const (
	factoryThrows        factoryFailure = "throws"         // the language's own failure: a panic, an exception, a condition
	factoryNothing       factoryFailure = "nothing"        // the factory returns no extension
	factoryHelperThrows  factoryFailure = "helper-throws"  // the factory calls a host helper, then throws
	factoryHelperNothing factoryFailure = "helper-nothing" // the factory calls a host helper, then returns no extension
)

// factoryFailureLanguages are the languages whose packed runner hosts a conventional factory, with the failures each can express: a Rust factory returns an
// Extension by value, so it can only panic.
var factoryFailureLanguages = map[string][]factoryFailure{
	"go":     {factoryThrows, factoryNothing, factoryHelperThrows, factoryHelperNothing},
	"python": {factoryThrows, factoryNothing, factoryHelperThrows, factoryHelperNothing},
	"rust":   {factoryThrows, factoryHelperThrows},
	"lisp":   {factoryThrows, factoryNothing, factoryHelperThrows, factoryHelperNothing},
}

func TestPackedFactoryFailureLeavesSiblingsLoaded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping factory failure conformance in short mode (builds runner images)")
	}
	root := findModuleRoot(t)
	for _, language := range []string{"go", "python", "rust", "lisp"} {
		for _, failure := range factoryFailureLanguages[language] {
			t.Run(language+"/"+string(failure), func(t *testing.T) {
				t.Parallel()
				// The isolated outcome is the contract; the packed one must equal it, so the isolated run comes first and its observation is the want.
				single := []string{"factory-fails"}
				want := observeFactoryFailure(t, root, language, failure, "isolated", single, false)
				for _, failingFirst := range []bool{true, false} {
					got := observeFactoryFailure(t, root, language, failure, "shared-ok", single, failingFirst)
					if !maps.Equal(got, want) {
						t.Errorf("packed (failing member first=%t) = %+v, want the isolated %+v", failingFirst, got, want)
					}
				}
				if failure != factoryThrows {
					return
				}
				// Two members of the cell fail, with the healthy one between them: each load error names its own cause, not the one the process wrote last.
				double := []string{"factory-fails-a", "factory-fails-b"}
				wantDouble := observeFactoryFailure(t, root, language, failure, "isolated", double, true)
				if gotDouble := observeFactoryFailure(t, root, language, failure, "shared-ok", double, true); !maps.Equal(gotDouble, wantDouble) {
					t.Errorf("packed, two failing members = %+v, want the isolated %+v", gotDouble, wantDouble)
				}
			})
		}
	}
}

// failureObservation is what the host reports for a failing extension, normalized across modes: the load error's phase and code, and its text without the
// path of the process's stderr log, which differs by mode.
type failureObservation struct {
	Phase, Code, Text string
}

var stderrLogPath = regexp.MustCompile(` \(stderr: [^)]*\)`)

// rustPanicPath is the directory a Rust panic names its source file by: the test's own temporary directory, which differs by mode.
var rustPanicPath = regexp.MustCompile(`(panicked at )\S*/src/lib\.rs`)

// observeFactoryFailure loads the failing factories of failing beside one healthy factory ("factory-ok"), and returns each failing extension's observation.
// failingFirst puts the failing ones before the healthy one; with several, the healthy one sits between the first and the last.
func observeFactoryFailure(t *testing.T, root, language string, failure factoryFailure, isolation string, failing []string, failingFirst bool) map[string]failureObservation {
	t.Helper()
	const healthy = "factory-ok"
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	messages := make(chan string, 8)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(newRecordingUI(new([]string), new([]string)))
	bridge.SetNotifyFunc(func(message, _ string) { messages <- message })
	h.SetUIBridge(bridge)
	var configs []subprocess.ExtConfig
	for _, name := range failing {
		configs = append(configs, failureFactory(t, root, language, name, failure, isolation))
	}
	ok := failureFactory(t, root, language, healthy, "", isolation)
	switch {
	case len(failing) > 1:
		configs = slices.Insert(configs, 1, ok)
	case failingFirst:
		configs = append(configs, ok)
	default:
		configs = slices.Insert(configs, 0, ok)
	}
	h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	observed := checkFailingLoadErrors(t, failure, failing, errs)
	for name, observation := range observed {
		for _, other := range failing {
			if other != name && strings.Contains(observation.Text, "boom "+other) {
				t.Errorf("%s: the load error of %s carries the cause of %s: %s", isolation, name, other, observation.Text)
			}
		}
	}
	checkSiblingLoaded(t, "load", healthy, loaded, h, messages)

	// A /reload loads the failing factories again, with the same outcome, and the sibling again: the report lists each failing extension as an issue that
	// reads as the load's error does.
	reloaded, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	report := h.LastReloadReport()
	if report == nil || len(report.Issues) != len(failing) {
		t.Fatalf("reload report = %+v, want exactly the %d failing extensions as issues (quarantined cells: %v)", report, len(failing), h.QuarantinedCells())
	}
	for name, observation := range observed {
		prefix := observation.Phase + ":" + observation.Code
		found := false
		for _, issue := range report.Issues {
			if strings.Contains(issue, prefix) && strings.Contains(issue, boomOf(failure, name)) {
				found = true
			}
		}
		if !found {
			t.Errorf("reload issues %q hold none that reads as the load error of %s: %s", report.Issues, name, observation.Text)
		}
	}
	checkSiblingLoaded(t, "reload", healthy, reloaded, h, messages)
	return observed
}

// boomOf is the text a failing factory's load error carries: the factory's own message, or what the runner says of a factory that returned nothing.
func boomOf(failure factoryFailure, name string) string {
	if failure == factoryNothing || failure == factoryHelperNothing {
		return "factory returned"
	}
	return "factory boom " + name
}

// checkFailingLoadErrors asserts the failing extensions are the load's only errors, each carries its factory's failure, and returns how each reads.
func checkFailingLoadErrors(t *testing.T, failure factoryFailure, failing []string, errs []error) map[string]failureObservation {
	t.Helper()
	if len(errs) != len(failing) {
		t.Fatalf("load: errors = %v, want exactly the %d failing extensions'", errs, len(failing))
	}
	observed := map[string]failureObservation{}
	for _, err := range errs {
		loadErr, ok := errors.AsType[*subprocess.LoadError](err)
		if !ok || !slices.Contains(failing, loadErr.Extension) {
			t.Fatalf("load: error = %#v, want a load error of one of %v", err, failing)
		}
		text := rustPanicPath.ReplaceAllString(stderrLogPath.ReplaceAllString(loadErr.Error(), ""), "${1}src/lib.rs")
		if !strings.Contains(text, boomOf(failure, loadErr.Extension)) {
			t.Errorf("the load error of %s does not carry the factory's failure %q: %s", loadErr.Extension, boomOf(failure, loadErr.Extension), text)
		}
		observed[loadErr.Extension] = failureObservation{Phase: loadErr.Phase, Code: loadErr.Code, Text: text}
	}
	if len(observed) != len(failing) {
		t.Fatalf("load: errors = %v, want one for each of %v", errs, failing)
	}
	return observed
}

// checkSiblingLoaded asserts the healthy extension is the only one loaded, answers a command, and sits in no quarantined cell.
func checkSiblingLoaded(t *testing.T, step, healthy string, loaded []extension.Extension, h *subprocess.Host, messages chan string) {
	t.Helper()
	var names []string
	for _, ext := range loaded {
		names = append(names, ext.Name)
	}
	if !slices.Equal(names, []string{healthy}) {
		t.Fatalf("%s: loaded extensions = %v, want only %q (quarantined cells: %v)", step, names, healthy, h.QuarantinedCells())
	}
	if cells := h.QuarantinedCells(); len(cells) != 0 {
		t.Fatalf("%s: a failing factory quarantined cells %v", step, cells)
	}
	if err := loaded[0].Commands["ping"].Handler(context.Background(), ""); err != nil {
		t.Fatalf("%s: the sibling's command failed: %v", step, err)
	}
	select {
	case got := <-messages:
		if got != "pong" {
			t.Fatalf("%s: the sibling answered %q, want pong", step, got)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("%s: the sibling never answered: its process is gone (quarantined cells: %v)", step, h.QuarantinedCells())
	}
}

// failureFactory writes one conventional factory of language: a healthy one when failure is empty (it registers a command "ping" that notifies "pong"),
// else one that fails as failure says, with "factory boom <name>" for a throw. Every factory is its own package, so every language packs them into one
// runner when isolation is shared-ok.
func failureFactory(t testing.TB, root, language, name string, failure factoryFailure, isolation string) subprocess.ExtConfig {
	t.Helper()
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	helper := failure == factoryHelperThrows || failure == factoryHelperNothing
	nothing := failure == factoryNothing || failure == factoryHelperNothing
	cfg := subprocess.ExtConfig{Name: name, Source: dir, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: language, Isolation: isolation, EntrypointKind: "factory", ContentHash: fmt.Sprintf("factory-failure-%s-%s", name, failure)}
	switch language {
	case "go":
		cfg.SDKName = "github.com/MichaelKinsy/PiG/extensions/sdk"
		cfg.ModulePath = "example.test/" + name
		cfg.Package = cfg.ModulePath
		cfg.Factory = "Extension"
		body := fmt.Sprintf(`e := sdk.New(%q)
 e.Command("ping","",func(ctx sdk.Context,_ string)error{ctx.Notify("pong","info");return nil})
 return e`, name)
		if failure != "" {
			body = fmt.Sprintf("panic(%q)", "factory boom "+name)
			if nothing {
				body = "return nil"
			}
			if helper {
				body = "_, _, _ = sdk.ParseDate(\"2020-01-01\")\n " + body
			}
		}
		write(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", cfg.ModulePath))
		write(filepath.Join(dir, "extension.go"), fmt.Sprintf("package failing\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension {\n %s\n}\n", body))
	case "python":
		cfg.SDKName = "pig-sdk-py"
		cfg.Package = strings.ReplaceAll(name, "-", "_")
		cfg.Factory = "new_extension"
		body := fmt.Sprintf("e = pig_sdk.Extension(%q)\n    e.command(\"ping\", \"\", lambda ctx, args: ctx.notify(\"pong\", \"info\"))\n    return e", name)
		if failure != "" {
			body = fmt.Sprintf("raise RuntimeError(%q)", "factory boom "+name)
			if nothing {
				body = "return None"
			}
			if helper {
				body = "pig_sdk.parse_date(\"2020-01-01\")\n    " + body
			}
		}
		write(filepath.Join(dir, cfg.Package+".py"), "import pig_sdk\ndef new_extension():\n    "+body+"\n")
	case "lisp":
		cfg.SDKName = "pig-sdk-lisp"
		cfg.Package = name
		cfg.Factory = "new-extension"
		body := fmt.Sprintf(`(let ((e (pig:make-extension %q)))
    (pig:command e "ping" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-notify ctx "pong" "info")))
    e)`, name)
		if failure != "" {
			body = fmt.Sprintf("(error %q)", "factory boom "+name)
			if nothing {
				body = "nil"
			}
			if helper {
				body = "(progn (pig:parse-date \"2020-01-01\") " + body + ")"
			}
		}
		write(filepath.Join(dir, name+".lisp"), fmt.Sprintf("(defpackage %q (:use \"COMMON-LISP\"))\n(in-package %q)\n(defun new-extension ()\n  %s)\n", name, name, body))
	case "rust":
		cfg.SDKName = "pig-sdk"
		cfg.Package = name
		cfg.Factory = "new_extension"
		body := fmt.Sprintf("let mut e=Extension::new(%q);\n e.command(\"ping\",\"\",|ctx,_args| {ctx.notify(\"pong\",\"info\");CommandResult::Ok});\n e", name)
		if failure != "" {
			body = fmt.Sprintf("panic!(%q)", "factory boom "+name)
			if helper {
				body = "let _ = pig_sdk::parse_date(\"2020-01-01\");\n " + body
			}
		}
		write(filepath.Join(dir, "Cargo.toml"), fmt.Sprintf("[package]\nname=%q\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		write(filepath.Join(dir, "src", "lib.rs"), fmt.Sprintf("use pig_sdk::{Extension,CommandResult};\npub fn new_extension()->Extension {\n %s\n}\n", body))
	}
	return cfg
}
