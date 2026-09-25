package services

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// server.ts retains the sole rejection or constructs AggregateError(errors, message), without appending the causes to that message.
func TestServerDisposePreservesReleaseErrorTree(t *testing.T) {
	first, second := errors.New("first release failed"), errors.New("second release failed")
	for _, failures := range [][]error{{first}, {first, second}} {
		t.Run(failures[len(failures)-1].Error(), func(t *testing.T) {
			services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{List: func(context.Context) ([]SessionSummary, error) { return []SessionSummary{}, nil }})
			requireModelsOK(t, err)
			for _, failure := range failures {
				attachment, err := services.Host.AttachClient(t.Context(), testServerPresentation{})
				requireModelsOK(t, err)
				// A direct provider observer injects a disposal failure below the host's real endpoint. The endpoint has no access to this subscription.
				subscription, err := attachment.provider.Subscribe(SessionDirectoryDefinition.Id(), chord.ServiceSingleton, func(context.Context, chord.ServiceProviderUpdate) { panic(failure) })
				requireModelsOK(t, err)
				requireModelsOK(t, subscription.Activate())
			}
			err = services.Dispose()
			expected := first
			if len(failures) > 1 {
				expected = &AggregateError{Message: "Failed to release server service attachments", Errors: []any{first, second}}
			}
			checkModelsEqual(t, err, expected)
			for _, failure := range failures {
				if !errors.Is(err, failure) {
					t.Errorf("lost cause: %v", failure)
				}
			}
			checkModelsEqual(t, len(services.Host.attachments), 0)
			requireModelsOK(t, services.Dispose())
		})
	}
}
