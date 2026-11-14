package ai

// pi: packages/ai/src/compat.ts

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// streamSimple resolves the api of the model in the API registry before anything else and throws "No API provider registered for api: <api>" (compat.ts:244-247, :290-293):
// a missing key is not reported first, because the key is asserted inside the provider the lookup never reaches.
func TestStreamSimpleRefusesAnUnknownAPINameWithPiText(t *testing.T) {
	for _, name := range []string{"nosuchapi", "constructor", "__proto__", "toString"} {
		for _, key := range []string{"k", ""} {
			t.Run(name+"/key="+key, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusBadRequest)
				}))
				defer server.Close()
				model := &Model{ID: "m", ProviderMeta: ProviderMetadata{ProviderID: "odd", API: API(name), BaseURL: server.URL + "/v1"}, Input: []string{"text"}}
				stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{APIKey: key})
				if err == nil {
					t.Fatalf("the stream started: %+v", stream.Result())
				}
				if want := "No API provider registered for api: " + name; err.Error() != want {
					t.Errorf("error = %q, want %q", err, want)
				}
				if got := requests.Load(); got != 0 {
					t.Errorf("requests at the endpoint = %d, want 0", got)
				}
			})
		}
	}
}
