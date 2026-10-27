package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// lispBusSiblingFixture is one of two Lisp extensions that share a packed cell and so one realm.
const lispBusSiblingFixture = `(defpackage %[1]q (:use "COMMON-LISP"))
(in-package %[1]q)

(defun new-extension ()
  (let ((e (pig:make-extension %[1]q)) (subs '()))
    (pig:command e "sub" "" (lambda (ctx args) (declare (ignore args))
                              (push (pig:events-on (pig:ctx-events ctx) "ch" (lambda (c data) (pig:ctx-notify c (format nil "~A|~A" %[1]q data)))) subs)))
    (pig:command e "unsub" "" (lambda (ctx args) (declare (ignore ctx args)) (funcall (pop subs))))
    (pig:command e "emit" "" (lambda (ctx args) (pig:events-emit (pig:ctx-events ctx) "ch" args)))
    e))
`

// newLispBusSiblingRig loads the fixtures into one packed Lisp cell (shared-ok) or one cell each (strict), as newPyAPIRig does for Python.
func newLispBusSiblingRig(t *testing.T, isolation string, names ...string) *pyAPIRig {
	t.Helper()
	root := findModuleRoot(t)
	notify, status := &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(bridge)
	t.Cleanup(func() { h.Shutdown("test complete") })
	var configs []subprocess.ExtConfig
	for _, name := range names {
		cfg := packedFlagFactory(t, root, "lisp", name, false)
		cfg.Isolation = isolation
		code := fmt.Sprintf(lispBusSiblingFixture, name)
		cfg.ContentHash = "lisp-bus-sibling-" + name + "-" + fmt.Sprint(len(code))
		if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".lisp"), []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, cfg)
	}
	var loaded []extension.Extension
	if isolation == "shared-ok" {
		h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
		var err error
		loaded, err = h.Reload(t.Context())
		if err != nil {
			t.Fatalf("reload: %v (%+v)", err, h.LastReloadReport())
		}
		if report := h.LastReloadReport(); report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-lisp") {
			t.Fatalf("expected one packed lisp cell: %+v", report)
		}
	} else {
		var failures []error
		loaded, failures = h.LoadAll(t.Context(), configs)
		if len(failures) != 0 {
			t.Fatalf("load: %v", failures)
		}
	}
	rig := &pyAPIRig{t: t, host: h, bridge: bridge, ui: ui, exts: map[string]extension.Extension{}}
	for _, ext := range loaded {
		rig.exts[ext.Name] = ext
	}
	if len(rig.exts) != len(names) {
		t.Fatalf("loaded %d of %d extensions", len(rig.exts), len(names))
	}
	rig.runner = inproc.NewRunner(loaded, t.TempDir(), h.Runtime())
	bridge.SetUIPromptScope(rig.runner)
	return rig
}

// The Host keys a pi.events listener by realm (one OS process) and handler ID, and a packed cell runs its extensions in one process. Each extension's unsubscribe removes only its own listener (event-bus.ts:27, EventEmitter.off), wherever the extensions run.
func TestLispSDKEventBusSiblingsInOneCellKeepTheirOwnListeners(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispBusSiblingRig(t, isolation, "lispapi-bus-a", "lispapi-bus-b")
		rig.command("lispapi-bus-a", "sub", "")
		rig.command("lispapi-bus-b", "sub", "")
		rig.command("lispapi-bus-a", "emit", "one")
		if got, want := rig.notifications(), []string{"lispapi-bus-a|one", "lispapi-bus-b|one"}; !slices.Equal(got, want) {
			t.Fatalf("after the first emit: %q, want %q", got, want)
		}
		rig.command("lispapi-bus-a", "unsub", "")
		rig.command("lispapi-bus-b", "emit", "two")
		if got, want := rig.notifications(), []string{"lispapi-bus-a|one", "lispapi-bus-b|one", "lispapi-bus-b|two"}; !slices.Equal(got, want) {
			t.Fatalf("after lispapi-bus-a unsubscribed: %q, want %q", got, want)
		}
	})
}
