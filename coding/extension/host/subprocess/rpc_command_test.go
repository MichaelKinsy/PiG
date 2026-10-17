package subprocess

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestRPCCommandMetadataPreservesRegistrationOrderAndSourceInfo(t *testing.T) {
	sourceInfo := extension.SourceInfo{Path: "/extensions/demo", Source: "pkg:demo", Scope: "project", Origin: "package", BaseDir: "/packages/demo"}
	host := NewHost(t.TempDir())
	ext := host.buildExtension(&managedExt{config: ExtConfig{Name: "demo", Path: "/extensions/demo", SourceInfo: sourceInfo}}, &RegisterPayload{
		Name: "demo",
		Commands: []CommandDecl{
			{Name: "z", Description: "first"},
			{Name: "a", Description: "second"},
		},
	})
	if !slices.Equal(ext.CommandOrder, []string{"z", "a"}) {
		t.Fatalf("command order=%v", ext.CommandOrder)
	}
	if ext.SourceInfo == (extension.SourceInfo{}) || ext.Commands["z"].SourceInfo == (extension.SourceInfo{}) || ext.Commands["a"].SourceInfo == (extension.SourceInfo{}) {
		t.Fatalf("source info extension=%#v commands=%#v", ext.SourceInfo, ext.Commands)
	}
	if ext.Commands["z"].SourceInfo.Path != "/extensions/demo" {
		t.Fatalf("source info=%#v", ext.Commands["z"].SourceInfo)
	}
}
