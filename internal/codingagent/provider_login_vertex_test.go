//go:build !pig_strip_google_vertex

package codingagent

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestLoginVertexServiceAccountUsesProviderPrompts(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	m.opts.Model = &ai.Model{ID: "existing", ProviderMeta: ai.ProviderMetadata{ProviderID: "existing"}}
	done := make(chan error, 1)
	go func() {
		done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login google-vertex", nil)
	}()
	waitForRender(t, m.editorContainer, "Select Google Vertex AI authentication method:")
	deliverModalInput(t, m, []byte("\x1b[B"))
	deliverModalInput(t, m, []byte("\x1b[B"))
	deliverModalInput(t, m, []byte("\r"))
	for _, step := range []struct{ prompt, reply string }{{"Enter Google Cloud project ID", "project"}, {"Enter Google Cloud location", "us-central1"}, {"Enter service account credentials file path", "/credentials/service.json"}} {
		waitForRender(t, m.editorContainer, step.prompt)
		deliverModalInput(t, m, []byte(step.reply))
		deliverModalInput(t, m, []byte("\r"))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	credential, ok, err := store.Get("google-vertex")
	if err != nil || !ok || credential.Key != "" || credential.Env["GOOGLE_CLOUD_PROJECT"] != "project" || credential.Env["GOOGLE_CLOUD_LOCATION"] != "us-central1" || credential.Env["GOOGLE_APPLICATION_CREDENTIALS"] != "/credentials/service.json" {
		t.Fatalf("stored=%+v ok=%v err=%v", credential, ok, err)
	}
}
