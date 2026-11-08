package extensionconformance

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// An isolated extension's own goroutine or thread that panics ends that extension's process only, and the extension stays stopped until a /reload. In a packed
// cell it ends the process that serves the other members as well, and neither language can recover another thread's panic; the host blames the member whose
// code the crash trace names, stops it as the isolated one stops, and starts the others again (D274). What the row pins is what both placements share: the
// blamed member is reported stopped with the log that holds its panic, the sibling answers a command, is never reported stopped, and a /reload brings both
// back. It does not pin that the sibling keeps its in-memory state, which a packed sibling does not.

func TestPackedCrashBlameAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping crash blame conformance in short mode (builds runner images)")
	}
	for _, language := range []string{"go", "lisp"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			want := map[string]bool{"culprit reported stopped": true, "log holds the panic": true, "sibling answers": true, "sibling reported stopped": false, "reload brings both back": true}
			isolated := observeCrashBlame(t, language, "isolated")
			if !maps.Equal(isolated, want) {
				t.Errorf("isolated = %v, want %v", isolated, want)
			}
			if packed := observeCrashBlame(t, language, "shared-ok"); !maps.Equal(packed, isolated) {
				t.Errorf("packed = %v, want the isolated %v", packed, isolated)
			}
		})
	}
}

func observeCrashBlame(t *testing.T, language, isolation string) map[string]bool {
	t.Helper()
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	messages := make(chan string, 16)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(newRecordingUI(new([]string), new([]string)))
	bridge.SetNotifyFunc(func(message, _ string) { messages <- message })
	h.SetUIBridge(bridge)
	var mu sync.Mutex
	byName := map[string][]string{}
	h.SetCrashHandler(func(name string, _ time.Duration, disabled bool, reason string) {
		mu.Lock()
		defer mu.Unlock()
		byName[name] = append(byName[name], fmt.Sprintf("disabled=%t: %s", disabled, reason))
	})
	configs := []subprocess.ExtConfig{crashFactory(t, language, "culprit", isolation), crashFactory(t, language, "sibling", isolation)}
	h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != 2 {
		t.Fatalf("loaded %d, errors %v", len(loaded), errs)
	}
	ping := func(name string) error {
		for _, ext := range h.Extensions() {
			if ext.Name != name {
				continue
			}
			if err := ext.Commands["ping"].Handler(ctx, ""); err != nil {
				return err
			}
			select {
			case got := <-messages:
				if got != "pong-"+name {
					return fmt.Errorf("%s answered %q", name, got)
				}
				return nil
			case <-time.After(10 * time.Second):
				return fmt.Errorf("%s never answered", name)
			}
		}
		return fmt.Errorf("%s is not loaded", name)
	}
	for _, name := range []string{"culprit", "sibling"} {
		if err := ping(name); err != nil {
			t.Fatal(err)
		}
	}
	var crash extension.Extension
	for _, ext := range loaded {
		if ext.Name == "culprit" {
			crash = ext
		}
	}
	// The crash can end the process before the command's reply, so the call's error says nothing.
	_ = crash.Commands["crash"].Handler(ctx, "")

	// The culprit is reported stopped, and the sibling answers again once the process it shared has been started anew.
	observed := map[string]bool{}
	deadline := time.Now().Add(2 * time.Minute)
	stoppedReport := func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		for _, report := range byName["culprit"] {
			if strings.HasPrefix(report, "disabled=true") {
				return report, true
			}
		}
		return "", false
	}
	var report string
	for {
		var ok bool
		if report, ok = stoppedReport(); ok {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("the culprit was not reported stopped: %q (quarantined %v)", byName, h.QuarantinedCells())
		}
		time.Sleep(50 * time.Millisecond)
	}
	observed["culprit reported stopped"] = true
	holds := false
	crashLog := ""
	if _, path, ok := strings.Cut(report, "(stderr: "); ok {
		path, _, _ = strings.Cut(path, ")")
		data, err := os.ReadFile(path)
		holds = err == nil && strings.Contains(string(data), "crash of culprit")
		crashLog = string(data)
		if !holds {
			t.Logf("the log %s holds %q (%v)", path, data, err)
		}
	}
	observed["log holds the panic"] = holds
	answered := false
	for !answered && time.Now().Before(deadline) {
		if answered = ping("sibling") == nil; !answered {
			time.Sleep(200 * time.Millisecond)
		}
	}
	observed["sibling answers"] = answered
	mu.Lock()
	stopped := false
	for _, r := range byName["sibling"] {
		if strings.HasPrefix(r, "disabled=true") || strings.Contains(r, "stopped") {
			stopped = true
		}
	}
	mu.Unlock()
	observed["sibling reported stopped"] = stopped
	if stopped {
		mu.Lock()
		t.Logf("%s: the sibling was reported stopped; reports %q\nthe process's log:\n%s", isolation, byName, crashLog)
		mu.Unlock()
	}

	// A /reload brings both back.
	_, err := h.Reload(ctx)
	observed["reload brings both back"] = err == nil && ping("culprit") == nil && ping("sibling") == nil
	return observed
}

// A Lisp member prints a backtrace of its own (sb-debug:print-backtrace writes the header SBCL's own report of an unhandled condition has), and the shared
// process then ends another way than an unhandled condition: sb-ext:exit or a kill. No unhandled condition ended it, so the printed backtrace names no one and the
// whole cell stops until a /reload (D274); the member that printed it is not blamed, and no member restarts.
func TestPackedLispProcessEndedWithoutAnUnhandledConditionStopsTheWholeCell(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping crash blame conformance in short mode (builds runner images)")
	}
	for _, end := range []string{"exit", "kill"} {
		t.Run(end, func(t *testing.T) {
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(newRecordingUI(new([]string), new([]string)))
			h.SetUIBridge(bridge)
			var mu sync.Mutex
			var reports []string
			h.SetCrashHandler(func(name string, _ time.Duration, disabled bool, reason string) {
				mu.Lock()
				defer mu.Unlock()
				reports = append(reports, fmt.Sprintf("%s disabled=%t: %s", name, disabled, reason))
			})
			configs := []subprocess.ExtConfig{crashFactory(t, "lisp", "culprit", "shared-ok"), crashFactory(t, "lisp", "sibling", "shared-ok")}
			h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
			defer cancel()
			loaded, errs := h.LoadAll(ctx, configs)
			if len(errs) != 0 || len(loaded) != 2 {
				t.Fatalf("loaded %d, errors %v", len(loaded), errs)
			}
			run := func(name, command string) {
				for _, ext := range loaded {
					if ext.Name == name {
						_ = ext.Commands[command].Handler(ctx, "")
						return
					}
				}
				t.Fatalf("%s is not loaded", name)
			}
			run("sibling", "printbt")
			run("culprit", end)
			stoppedWithCell := func(name string) bool {
				mu.Lock()
				defer mu.Unlock()
				return slices.ContainsFunc(reports, func(report string) bool {
					return strings.HasPrefix(report, name+" disabled=true") && strings.Contains(report, "/reload restarts them")
				})
			}
			deadline := time.Now().Add(2 * time.Minute)
			for !stoppedWithCell("culprit") || !stoppedWithCell("sibling") {
				if time.Now().After(deadline) {
					mu.Lock()
					defer mu.Unlock()
					t.Fatalf("the whole cell was not reported stopped: %q (quarantined %v)", reports, h.QuarantinedCells())
				}
				time.Sleep(50 * time.Millisecond)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, report := range reports {
				if strings.Contains(report, "own code ended the shared process") || strings.Contains(report, "restarting") {
					t.Fatalf("a member was blamed or restarted: %q", reports)
				}
			}
		})
	}
}

// crashFactory writes a conventional factory of language with a command "ping" that notifies "pong-<name>" and a command "crash" that starts a goroutine
// (a thread) of its own that panics (signals an error) with "crash of <name>", which nothing handles; a Lisp one also has "printbt", "exit" and "kill".
func crashFactory(t testing.TB, language, name, isolation string) subprocess.ExtConfig {
	t.Helper()
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := subprocess.ExtConfig{Name: name, Source: dir, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: language, Isolation: isolation, EntrypointKind: "factory", ContentHash: "crash-blame-" + name}
	switch language {
	case "go":
		cfg.SDKName = "github.com/MichaelKinsy/PiG/extensions/sdk"
		cfg.ModulePath = "example.test/" + name
		cfg.Package = cfg.ModulePath
		cfg.Factory = "Extension"
		write("go.mod", fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", cfg.ModulePath))
		write("extension.go", fmt.Sprintf(`package crasher

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New(%[1]q)
	e.Command("ping", "", func(ctx sdk.Context, _ string) error { ctx.Notify("pong-"+%[1]q, "info"); return nil })
	e.Command("crash", "", func(ctx sdk.Context, _ string) error {
		go func() { panic("crash of " + %[1]q) }()
		return nil
	})
	return e
}
`, name))
	case "lisp":
		cfg.SDKName = "pig-sdk-lisp"
		cfg.Package = name
		cfg.Factory = "new-extension"
		write(name+".lisp", fmt.Sprintf(`(defpackage %[1]q (:use "COMMON-LISP"))
(in-package %[1]q)
;; SBCL on Windows has no sb-unix:unix-kill (a missing symbol is a reader error that fails the build), so it ends itself with TerminateProcess on its own pseudo handle.
(defun hard-kill ()
  #-win32 (sb-unix:unix-kill (sb-unix:unix-getpid) 9)
  #+win32 (sb-alien:alien-funcall
           (sb-alien:extern-alien "TerminateProcess" (function sb-alien:int sb-alien:system-area-pointer sb-alien:unsigned-int))
           (sb-alien:alien-funcall (sb-alien:extern-alien "GetCurrentProcess" (function sb-alien:system-area-pointer)))
           9))
(defun new-extension ()
  (let ((e (pig:make-extension %[1]q)))
    (pig:command e "ping" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-notify ctx "pong-%[1]s" "info")))
    (pig:command e "crash" "" (lambda (ctx args) (declare (ignore ctx args))
                                (sb-thread:make-thread (lambda () (error "crash of %[1]s")))
                                nil))
    (pig:command e "printbt" "" (lambda (ctx args) (declare (ignore ctx args))
                                  (sb-debug:print-backtrace :stream *error-output* :count 8)
                                  (finish-output *error-output*)
                                  nil))
    (pig:command e "exit" "" (lambda (ctx args) (declare (ignore ctx args)) (sb-ext:exit :code 3 :abort t)))
    (pig:command e "kill" "" (lambda (ctx args) (declare (ignore ctx args)) (hard-kill)))
    e))
`, name))
	}
	return cfg
}
