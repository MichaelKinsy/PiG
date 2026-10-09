package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A child Piglet inherits its base's frontend member, which resolves against
// the base's directory; it replaces the member by naming its own, and removes
// it with extends.remove.slots so PiG's own renderer applies again.
func TestFrontendSlotInheritsReplacesAndRemoves(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"base/fe", "mine"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writePigletSource(t, filepath.Join(root, "base"), "base", "name: base\nslots:\n  frontend:\n    member: ./fe\n")
	writePigletSource(t, root, "plain", "name: plain\n")
	baseFrontend := canonicalPath(filepath.Join(root, "base", "fe"))
	mine := canonicalPath(filepath.Join(root, "mine"))

	for _, tc := range []struct {
		name, body, want, err string
	}{
		{name: "inherits", body: "extends:\n  source: local:./base/base.yaml\n", want: baseFrontend},
		{name: "replaces", body: "extends:\n  source: local:./base/base.yaml\nslots:\n  frontend:\n    member: ./mine\n", want: mine},
		{name: "removes", body: "extends:\n  source: local:./base/base.yaml\n  remove:\n    slots: [frontend]\n", want: ""},
		{name: "removes then names its own", body: "extends:\n  source: local:./base/base.yaml\n  remove:\n    slots: [frontend]\nslots:\n  frontend:\n    member: ./mine\n", want: mine},
		{name: "removes an absent member", body: "extends:\n  source: local:./plain.yaml\n  remove:\n    slots: [frontend]\n", err: `remove.slots: "frontend" is absent`},
		{name: "adds one to a plain base", body: "extends:\n  source: local:./plain.yaml\nslots:\n  frontend:\n    member: ./mine\n", want: mine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := writePigletSource(t, root, "child", "name: child\n"+tc.body)
			resolved, err := ResolveEffective(child)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("ResolveEffective error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolved.Piglet.FrontendDir()
			if err != nil || got != tc.want {
				t.Fatalf("FrontendDir() = %q, %v; want %q", got, err, tc.want)
			}
			if resolved.Piglet.HasFrontend() != (tc.want != "") {
				t.Fatalf("HasFrontend() = %v, want %v", resolved.Piglet.HasFrontend(), tc.want != "")
			}
		})
	}

	t.Run("through a middle Piglet", func(t *testing.T) {
		writePigletSource(t, root, "middle", "name: middle\nextends:\n  source: local:./base/base.yaml\n")
		child := writePigletSource(t, root, "grandchild", "name: grandchild\nextends:\n  source: local:./middle.yaml\n")
		resolved, err := ResolveEffective(child)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := resolved.Piglet.FrontendDir(); err != nil || got != baseFrontend {
			t.Fatalf("FrontendDir() = %q, %v; want %q", got, err, baseFrontend)
		}
	})

	t.Run("a clone keeps its own member", func(t *testing.T) {
		resolved, err := ResolveEffective(filepath.Join(root, "base", "base.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		clone := Clone(resolved.Piglet)
		clone.Slots.Frontend.Member = "./other"
		if got := resolved.Piglet.Slots.Frontend.Member; got != "./fe" {
			t.Fatalf("original member after editing the clone = %q", got)
		}
	})
}
