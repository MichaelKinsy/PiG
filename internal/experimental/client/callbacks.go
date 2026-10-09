package client

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// ServiceCatalogueCallback preserves the synchronous request-admission prefix needed by the connection service source. Its completion remains client-owned and WaitClosed drains it even if the source is disposed first.
func (client *Client) ServiceCatalogueCallback(ctx context.Context, target protocol.RpcTarget, complete func([]chord.ServiceCatalogueEntry, error)) {
	pending, err := client.beginRequest(ctx, target, chord.CreateServiceCatalogueCall(), nil)
	if err != nil {
		complete(nil, err)
		return
	}
	done := make(chan struct{})
	client.connection.ownLifetime(done)
	go func() {
		defer close(done)
		<-pending.done
		if pending.err != nil {
			complete(nil, pending.err)
			return
		}
		entries, err := chord.ParseServiceCatalogue(pending.value.(json.RawMessage))
		if err != nil {
			failure := protocol.NewProtocolValidationError(err.Error())
			client.connection.Fail(failure)
			complete(nil, failure)
			return
		}
		complete(entries, nil)
	}()
}
