package subprocess

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:806
func TestUpstreamRunnerRejectsInvalidFlagFactory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bad-flag-default.ts")
	write(t, path, `export default function(pi) { pi.registerFlag("safe-mode", {type:"boolean",default:"false"}); }`)
	h := NewHost(root)
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "bad-flag-default", Source: path, Enabled: true}})
	if len(loaded) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), `Invalid default for flag "safe-mode": expected boolean, got string`) {
		t.Fatalf("loaded=%#v errors=%v", loaded, errs)
	}
	if h.FlagDefault("bad-flag-default", "safe-mode") != nil {
		t.Fatal("failed factory committed its flag")
	}
}

func TestNodeFlagSnapshotClearsLastOverride(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "unset.mjs")
	write(t, path, `export default function(pi) {
 pi.registerFlag("only", {type:"string"});
 pi.registerCommand("probe", {handler:async(_,ctx)=>ctx.ui.notify(String(pi.getFlag("only")), "info")});
 }`)
	h := NewHost(root)
	t.Cleanup(func() { h.Shutdown("test done") })
	b := NewUIBridge(func() {})
	b.SetUIContext(newTestUIContext())
	notifications := make(chan string, 1)
	b.SetNotifyFunc(func(message, _ string) { notifications <- message })
	if !b.Snapshot(nil, 0, false).HasUI {
		t.Fatal("flag notification probe requires a bound UI, not only a notification observer")
	}
	h.SetUIBridge(b)
	ext, err := h.Load(t.Context(), ExtConfig{Name: "unset", Source: path, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, "configured", nil} {
		b.SetHostAction("getFlag", func(_, _ string) any { return value })
		if err := ext.Commands["probe"].Handler(context.Background(), ""); err != nil {
			t.Fatal(err)
		}
		want := "undefined"
		if value != nil {
			want = value.(string)
		}
		select {
		case got := <-notifications:
			if got != want {
				t.Fatalf("flag=%q, want %q", got, want)
			}
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}
