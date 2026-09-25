package extensionconformance

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestNodeProjectTrustMultipleHandlers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node project-trust bridge test in short mode")
	}
	shortSockDir(t)

	fixture := filepath.Join(findModuleRoot(t), "tests", "extension-conformance", "testdata", "node-project-trust-fixture", "main.mjs")
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })

	built, err := subprocess.NewBuilder(t.TempDir()).Build("node-project-trust-fixture", fixture)
	if err != nil {
		t.Fatalf("build node launcher: %v", err)
	}
	loaded, err := host.Load(context.Background(), subprocess.ExtConfig{
		Name:    "node-project-trust-fixture",
		Path:    built.BinaryPath,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("load node fixture: %v", err)
	}

	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	result, handlerErrors, err := inproc.EmitProjectTrust(runner, context.Background(), extension.ProjectTrustEvent{
		Type: "project_trust",
		Cwd:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(handlerErrors) != 0 {
		t.Fatalf("handler errors = %+v", handlerErrors)
	}
	if result == nil || result.Trusted != extension.ProjectTrustYes || result.Remember == nil || !*result.Remember {
		t.Fatalf("result = %+v", result)
	}
}
