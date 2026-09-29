package inproc

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"testing"
)

func TestUpstreamRunnerFlags(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:758
	t.Run("collects flags from extensions", func(t *testing.T) {
		r := NewRunner([]extension.Extension{{Flags: map[string]extension.ExtensionFlag{"my-flag": {Name: "my-flag", Description: "My flag"}}}}, t.TempDir())
		if _, ok := r.Flags()["my-flag"]; !ok {
			t.Fatal("my-flag not collected")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:776
	t.Run("keeps first flag when two extensions register the same name", func(t *testing.T) {
		r := NewRunner([]extension.Extension{
			{Flags: map[string]extension.ExtensionFlag{"shared-flag": {Name: "shared-flag", Description: "first", Type: "boolean", Default: true}}},
			{Flags: map[string]extension.ExtensionFlag{"shared-flag": {Name: "shared-flag", Description: "second", Type: "boolean", Default: false}}},
		}, t.TempDir())
		if r.Flags()["shared-flag"].Description != "first" || r.GetFlagValues()["shared-flag"] != true {
			t.Fatalf("first flag lost: %#v / %#v", r.Flags(), r.GetFlagValues())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:826
	t.Run("can set flag values", func(t *testing.T) {
		r := NewRunner([]extension.Extension{{Flags: map[string]extension.ExtensionFlag{"test-flag": {Name: "test-flag", Description: "Test flag"}}}}, t.TempDir())
		r.SetFlagValue("--test-flag", true)
		if r.GetFlagValues()["--test-flag"] != true {
			t.Fatalf("runtime flag = %#v", r.GetFlagValues())
		}
		snapshot := r.GetFlagValues()
		snapshot["--test-flag"] = false
		if r.GetFlagValues()["--test-flag"] != true {
			t.Fatal("getFlagValues returned mutable runtime state")
		}
	})
}
