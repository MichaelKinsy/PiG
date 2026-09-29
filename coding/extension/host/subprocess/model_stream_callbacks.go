package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// modelStreamCallbacks retain the initiating connection and operation context.
// The provider awaits each callback; errors reject that same request, and no
// callback can address another connection's stream handle.
func modelStreamCallbacks(ctx context.Context, owner *Conn, request ModelStreamCall) extension.ModelStreamRequest {
	invoke := func(ctx context.Context, callback string, value any) (json.RawMessage, error) {
		args, err := json.Marshal(ModelStreamCallback{StreamID: request.StreamID, Callback: callback, Value: value})
		if err != nil {
			return nil, fmt.Errorf("marshal %s callback: %w", callback, err)
		}
		response, err := owner.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: MethodModelStreamCallback, Args: args}})
		if err != nil {
			return nil, err
		}
		if response.Response == nil {
			return nil, fmt.Errorf("%s callback returned no response", callback)
		}
		if response.Response.Error != nil {
			return nil, response.Response.Error.ToError()
		}
		return response.Response.Result, nil
	}
	callbacks := extension.ModelStreamRequest{API: request.APIRequest}
	if request.Fetch {
		// The remote fetch implementation owns redirects; its returned response must not trigger a second host-side redirect.
		callbacks.Fetch = &http.Client{Transport: newModelFetch(invoke), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if request.OnPayload {
		callbacks.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			result, err := invoke(ctx, "onPayload", payload)
			if err != nil {
				return nil, err
			}
			var returned struct {
				Defined bool            `json:"defined"`
				Value   json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal(result, &returned); err != nil {
				return nil, fmt.Errorf("decode onPayload callback: %w", err)
			}
			if !returned.Defined {
				return nil, nil
			}
			return returned.Value, nil
		}
	}
	if request.OnResponse {
		callbacks.OnResponse = func(ctx context.Context, response ai.ProviderResponse, _ *ai.Model) error {
			_, err := invoke(ctx, "onResponse", response)
			return err
		}
	}
	if request.TransformHeaders {
		callbacks.TransformHeaders = func(ctx context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
			result, err := invoke(ctx, "transformHeaders", headers)
			if err != nil {
				return nil, err
			}
			var transformed ai.ProviderHeaders
			if err := json.Unmarshal(result, &transformed); err != nil {
				return nil, fmt.Errorf("decode transformHeaders callback: %w", err)
			}
			return transformed, nil
		}
	}
	return callbacks
}
