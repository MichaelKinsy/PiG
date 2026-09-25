package subprocess

import (
	"slices"
	"testing"
)

func TestRPCCommandMetadataPreservesRegistrationOrderAndSourceInfo(t *testing.T) {
	sourceInfo := map[string]any{
		"path": "/extensions/demo", "source": "pkg:demo", "scope": "project",
		"origin": "package", "baseDir": "/packages/demo",
	}
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
	if ext.SourceInfo == nil || ext.Commands["z"].SourceInfo == nil || ext.Commands["a"].SourceInfo == nil {
		t.Fatalf("source info extension=%#v commands=%#v", ext.SourceInfo, ext.Commands)
	}
	if ext.Commands["z"].SourceInfo.(map[string]any)["path"] != "/extensions/demo" {
		t.Fatalf("source info=%#v", ext.Commands["z"].SourceInfo)
	}
}
