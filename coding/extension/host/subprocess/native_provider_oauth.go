package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// nativeOAuthProxy adapts the login picker to the native Provider auth surface.
// Request-time resolution uses the canonical credential-store path instead.
type nativeOAuthProxy struct {
	proxy *nativeProviderProxy
	host  *Host
	owner string
}

func (p *nativeOAuthProxy) ID() string               { return p.proxy.declaration.ID }
func (p *nativeOAuthProxy) Name() string             { return p.proxy.declaration.OAuth.Name }
func (p *nativeOAuthProxy) UsesCallbackServer() bool { return false }
func (p *nativeOAuthProxy) IsSubscription() bool     { return p.proxy.declaration.OAuth.IsSubscription }
func (p *nativeOAuthProxy) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return p.LoginContext(context.Background(), callbacks)
}
func (p *nativeOAuthProxy) LoginContext(ctx context.Context, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	p.host.setOAuthLoginSession(p.owner, callbacks)
	defer p.host.clearOAuthLoginSession(p.owner)
	data, err := p.proxy.objectCall(ctx, "auth.oauth.login", map[string]any{}, func(data json.RawMessage) (json.RawMessage, error) {
		var request struct {
			Method string `json:"method"`
			Params struct {
				Prompt json.RawMessage `json:"prompt"`
				Event  json.RawMessage `json:"event"`
			} `json:"params"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, err
		}
		payload := request.Params.Prompt
		if request.Method == "notify" {
			payload = request.Params.Event
		}
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &kind); err != nil {
			return nil, err
		}
		method := ""
		switch request.Method {
		case "prompt":
			method = CallOAuthOnPrompt
			if kind.Type == "select" {
				method = CallOAuthOnSelect
			}
			if kind.Type == "manual_code" {
				method = CallOAuthOnManualCodeInput
			}
		case "notify":
			method = CallOAuthOnProgress
			if kind.Type == "auth_url" {
				method = CallOAuthOnAuth
			}
			if kind.Type == "device_code" {
				method = CallOAuthOnDeviceCode
			}
		default:
			return nil, fmt.Errorf("unexpected login callback %s", request.Method)
		}
		result, err := p.host.handleOAuthCallback(ctx, p.owner, &CallPayload{Method: method, Args: payload})
		if err != nil {
			return nil, err
		}
		if result.Error != nil {
			return nil, result.Error.ToError()
		}
		if request.Method == "notify" {
			return json.RawMessage("null"), nil
		}
		var answer struct {
			Value  string `json:"value"`
			Cancel bool   `json:"cancel"`
		}
		if err := json.Unmarshal(result.Result, &answer); err != nil {
			return nil, err
		}
		if answer.Cancel {
			return nil, errors.New("Login cancelled")
		}
		return json.Marshal(answer.Value)
	})
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	var result ai.OAuthCredentials
	err = json.Unmarshal(data, &result)
	return result, err
}
func (p *nativeOAuthProxy) RefreshToken(credential ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return p.RefreshTokenContext(context.Background(), credential)
}
func (p *nativeOAuthProxy) RefreshTokenContext(ctx context.Context, credential ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ai.OAuthCredentials{}, err
	}
	fields["type"] = "oauth"
	data, err := p.proxy.objectCall(ctx, "auth.oauth.refresh", map[string]any{"credential": fields}, nil)
	if err != nil {
		return ai.OAuthCredentials{}, err
	}
	var result ai.OAuthCredentials
	err = json.Unmarshal(data, &result)
	return result, err
}
func (p *nativeOAuthProxy) GetAPIKey(credential ai.OAuthCredentials) string {
	key, _ := p.GetAPIKeyContext(context.Background(), credential)
	return key
}
func (p *nativeOAuthProxy) GetAPIKeyContext(ctx context.Context, credential ai.OAuthCredentials) (string, error) {
	data, err := p.proxy.objectCall(ctx, "auth.oauth.toAuth", map[string]any{"credential": credential}, nil)
	if err != nil {
		return "", err
	}
	var result struct {
		APIKey string `json:"apiKey"`
	}
	err = json.Unmarshal(data, &result)
	return result.APIKey, err
}
