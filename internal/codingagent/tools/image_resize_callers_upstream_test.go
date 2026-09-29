package tools

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestUpstreamImageResizeReadCallers(t *testing.T) {
	const image = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
	fixture := func(t *testing.T) (string, json.RawMessage) {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.png")
		data, err := base64.StdEncoding.DecodeString(image)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		args, err := json.Marshal(map[string]string{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		return dir, args
	}
	// .upstream/v0.87.1/packages/coding-agent/test/image-resize-callers.test.ts:34
	t.Run("read tool returns text-only output when auto-resize cannot produce a safe image", func(t *testing.T) {
		dir, args := fixture(t)
		old := processReadImage
		t.Cleanup(func() { processReadImage = old })
		processReadImage = func(_ []byte, _ string, autoResize bool, _ *ai.ModelImageResizeOptions) ([]byte, string, string, error) {
			if !autoResize {
				t.Error("default read tool did not request the mocked resize")
			}
			return nil, "", "", errors.New("[Image omitted: could not be resized below the inline image size limit.]")
		}
		result, err := (&ReadTool{CWD: dir}).Execute(t.Context(), "test-read-image", args, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Content) != 1 || len(result.Images()) != 0 || !strings.Contains(result.Text(), "Image omitted") {
			t.Fatalf("result=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/image-resize-callers.test.ts:56
	t.Run("passes the current model resize profile to the read tool", func(t *testing.T) {
		dir, args := fixture(t)
		old := processReadImage
		t.Cleanup(func() { processReadImage = old })
		resize := &ai.ModelImageResizeOptions{MaxWidth: 1234, MaxHeight: 1000, MaxBytes: 500000, JPEGQuality: 70}
		called := false
		processReadImage = func(data []byte, mime string, autoResize bool, options *ai.ModelImageResizeOptions) ([]byte, string, string, error) {
			called = true
			if len(data) == 0 || mime != "image/png" || !autoResize || !reflect.DeepEqual(options, resize) {
				t.Errorf("processing arguments data=%v MIME=%q resize=%v profile=%#v", data, mime, autoResize, options)
			}
			return nil, "", "", errors.New("[Image omitted: could not be resized below the inline image size limit.]")
		}
		ctx := agent.WithToolEnvironment(t.Context(), agent.ToolEnvironment{InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: resize}}})
		if _, err := (&ReadTool{CWD: dir}).Execute(ctx, "test-read-model-profile", args, nil); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("read tool did not process the image")
		}
	})
}
