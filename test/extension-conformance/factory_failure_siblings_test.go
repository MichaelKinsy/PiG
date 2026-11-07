package extensionconformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// language, and a /reload loads the siblings again.

// factoryFailure is how a test factory fails.
type factoryFailure string

const (
	factoryThrows  factoryFailure = "throws"  // the language's own failure: a panic, an exception, a condition
	factoryNothing factoryFailure = "nothing" // the factory returns no extension
)

// factoryFailureLanguages are the languages whose packed runner hosts a conventional factory, with the failures each can express: a Rust factory returns an
// Extension by value, so it can only panic.
var factoryFailureLanguages = map[string][]factoryFailure{
	"go":     {factoryThrows, factoryNothing},
	"python": {factoryThrows, factoryNothing},
	"rust":   {factoryThrows},
	"lisp":   {factoryThrows, factoryNothing},
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
				want := observeFactoryFailure(t, root, language, failure, "isolated", false)
				for _, failingFirst := range []bool{true, false} {
					got := observeFactoryFailure(t, root, language, failure, "shared-ok", failingFirst)
					if got != want {
						t.Errorf("packed (failing member first=%t) = %+v, want the isolated %+v", failingFirst, got, want)
					}
				}
			})
		}
	}
}

// failureObservation is what the host reports for the failing extension, normalized across modes: the load error's phase and code, and whether it
// carries the factory's own message.
type failureObservation struct {
	Phase, Code string
	Cause       bool
}

func observeFactoryFailure(t *testing.T, root, language string, failure factoryFailure, isolation string, failingFirst bool) failureObservation {
	t.Helper()
	const failing, healthy = "factory-fails", "factory-ok"
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	messages := make(chan string, 8)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(newRecordingUI(new([]string), new([]string)))
	bridge.SetNotifyFunc(func(message, _ string) { messages <- message })
	h.SetUIBridge(bridge)
	configs := []subprocess.ExtConfig{
		failureFactory(t, root, language, failing, failure, isolation),
		failureFactory(t, root, language, healthy, "", isolation),
	}
	if !failingFirst {
		slices.Reverse(configs)
	}
	h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	observed := checkFailingLoadError(t, failing, errs)
	if !observed.Cause {
		t.Errorf("%s: the load error does not carry the factory's failure: %s", isolation, observed.text)
	}
	checkSiblingLoaded(t, "load", healthy, loaded, h, messages)

	// A /reload loads the failing factory again, with the same outcome, and the sibling again: the report lists the failing extension as an issue that reads as the load's error does.
	reloaded, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	report := h.LastReloadReport()
	if report == nil || len(report.Issues) != 1 {
		t.Fatalf("reload report = %+v, want exactly the failing extension as an issue (quarantined cells: %v)", report, h.QuarantinedCells())
	}
	prefix := observed.Phase + ":" + observed.Code
	if !strings.Contains(report.Issues[0], prefix) || strings.Contains(report.Issues[0], "factory boom") != strings.Contains(observed.text, "factory boom") {
		t.Errorf("reload issue = %q, the load error = %q", report.Issues[0], observed.text)
	}
	checkSiblingLoaded(t, "reload", healthy, reloaded, h, messages)
	return observed.failureObservation
}

type observedFailure struct {
	failureObservation
	text string
}

// checkFailingLoadError asserts the failing extension is the load's one error and returns how it reads.
func checkFailingLoadError(t *testing.T, failing string, errs []error) observedFailure {
	t.Helper()
	if len(errs) != 1 {
		t.Fatalf("load: errors = %v, want exactly the failing extension's", errs)
	}
	loadErr, ok := errors.AsType[*subprocess.LoadError](errs[0])
	if !ok || loadErr.Extension != failing {
		t.Fatalf("load: error = %#v, want a load error of %q", errs[0], failing)
	}
	text := loadErr.Error()
	cause := strings.Contains(text, "factory boom") || strings.Contains(text, "factory returned")
	return observedFailure{failureObservation{Phase: loadErr.Phase, Code: loadErr.Code, Cause: cause}, text}
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
// else one that fails as failure says. Every factory is its own package, so every language packs them into one runner when isolation is shared-ok.
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
		switch failure {
		case factoryThrows:
			body = `panic("factory boom")`
		case factoryNothing:
			body = `return nil`
		}
		write(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", cfg.ModulePath))
		write(filepath.Join(dir, "extension.go"), fmt.Sprintf("package failing\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension {\n %s\n}\n", body))
	case "python":
		cfg.SDKName = "pig-sdk-py"
		cfg.Package = strings.ReplaceAll(name, "-", "_")
		cfg.Factory = "new_extension"
		body := fmt.Sprintf("e = pig_sdk.Extension(%q)\n    e.command(\"ping\", \"\", lambda ctx, args: ctx.notify(\"pong\", \"info\"))\n    return e", name)
		switch failure {
		case factoryThrows:
			body = `raise RuntimeError("factory boom")`
		case factoryNothing:
			body = `return None`
		}
		write(filepath.Join(dir, cfg.Package+".py"), "import pig_sdk\ndef new_extension():\n    "+body+"\n")
	case "lisp":
		cfg.SDKName = "pig-sdk-lisp"
		cfg.Package = name
		cfg.Factory = "new-extension"
		body := fmt.Sprintf(`(let ((e (pig:make-extension %q)))
    (pig:command e "ping" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-notify ctx "pong" "info")))
    e)`, name)
		switch failure {
		case factoryThrows:
			body = `(error "factory boom")`
		case factoryNothing:
			body = `nil`
		}
		write(filepath.Join(dir, name+".lisp"), fmt.Sprintf("(defpackage %q (:use \"COMMON-LISP\"))\n(in-package %q)\n(defun new-extension ()\n  %s)\n", name, name, body))
	case "rust":
		cfg.SDKName = "pig-sdk"
		cfg.Package = name
		cfg.Factory = "new_extension"
		body := fmt.Sprintf("let mut e=Extension::new(%q);\n e.command(\"ping\",\"\",|ctx,_args| {ctx.notify(\"pong\",\"info\");CommandResult::Ok});\n e", name)
		if failure == factoryThrows {
			body = `panic!("factory boom")`
		}
		write(filepath.Join(dir, "Cargo.toml"), fmt.Sprintf("[package]\nname=%q\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		write(filepath.Join(dir, "src", "lib.rs"), fmt.Sprintf("use pig_sdk::{Extension,CommandResult};\npub fn new_extension()->Extension {\n %s\n}\n", body))
	}
	return cfg
}
