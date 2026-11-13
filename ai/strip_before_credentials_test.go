package ai

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A stripped API reports the strip before any credential check: with a key and without one, the plain stream and the simple stream of each strippable API, and StreamSimple, end with the strip message. Strips are PiG's own (D92), so Pi has no order of these two errors to match; without a strip the missing key is reported as in Pi (TestCompatAPIStreamsMatchPi, the "without credentials" subtests). The APIs a pig_strip tag leaves in the build are stripped at run time, as Stock PiG strips them for a Piglet file.
func TestStrippedAPIReportsTheStripBeforeTheCredentialCheck(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	for _, api := range StrippableAPIs() {
		provider := strippedAPIProviders[string(api)]
		for _, key := range []string{"k", ""} {
			t.Run(fmt.Sprintf("%s/key=%q", api, key), func(t *testing.T) {
				if BuiltAPI(api) {
					t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(api)))
				}
				model := &Model{ID: "m1", DisplayName: "m1", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: api, ProviderID: provider, BaseURL: "http://127.0.0.1:9"}}
				want := strippedAPIMessage(provider, api)
				for name, stream := range map[string]func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error){
					"the API's stream":        apiStreams(api).Stream,
					"the API's simple stream": apiStreams(api).StreamSimple,
					"StreamSimple":            StreamSimple,
				} {
					message := ""
					if events, err := stream(t.Context(), model, transcript, StreamOptions{APIKey: key}); err != nil {
						message = err.Error()
					} else {
						message = events.Result().ErrorMessage
					}
					if message != want {
						t.Errorf("%s ended with %q, want the strip message %q", name, message, want)
					}
				}
			})
		}
	}
}
