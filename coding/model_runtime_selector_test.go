package coding

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// model-selector.ts:162-168, 202 and model-catalog-refresh.ts: the selector reads the runtime's available snapshot, one model by identity, the load
// error and refreshes the catalogs through ModelRuntime. The real ModelRuntime is passed to Pi's constructor, as interactive-mode.ts does.
func TestModelSelectorConstructorTakesTheRealModelRuntime(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	const modelsJSON = `{"providers":{"login-custom":{"baseUrl":"https://custom.invalid/v1","apiKey":"$LOGIN_TEST_API_KEY","api":"openai-completions","models":[{"id":"custom-model"}]}}}`
	var runtime *ModelRuntime
	runtime, _ = loginTestRuntime(t, modelsJSON)
	if _, err := runtime.Login(t.Context(), "login-custom", ai.CredentialAPIKey, dummyKeyInteraction("dummy-key")); err != nil {
		t.Fatal(err)
	}
	custom := runtime.GetModel("login-custom", "custom-model")
	if custom == nil {
		t.Fatal("the runtime does not know login-custom/custom-model")
	}
	if runtime.GetModel("login-custom", "missing") != nil {
		t.Fatal("GetModel returns a model for an unknown id; Pi returns undefined")
	}

	ui := tui.NewWithOutput(&bytes.Buffer{}, 120, 30)
	posted := make(chan func(), 4)
	ui.SetOwnerDispatcher(func(_ context.Context, fn func()) error { posted <- fn; return nil })
	selector := tui.NewModelSelectorComponent(ui, custom, runtime, []tui.ScopedModelItem{{Model: custom}}, func(*ai.Model) {}, func() {}, "", nil, nil)
	t.Cleanup(selector.Dispose)
	rows := strings.Join(selector.Render(120), "\n")
	if !strings.Contains(rows, "custom-model") || !strings.Contains(rows, "Refreshing model catalogs") {
		t.Fatalf("selector rows before the refresh:\n%s", rows)
	}
	select {
	case fn := <-posted:
		fn()
	case <-time.After(30 * time.Second):
		t.Fatal("the real runtime's refresh never reached the owner loop")
	}
	rows = strings.Join(selector.Render(120), "\n")
	if strings.Contains(rows, "Refreshing model catalogs") || !strings.Contains(rows, "custom-model") {
		t.Fatalf("selector rows after the refresh:\n%s", rows)
	}
}
