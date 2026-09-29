package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	access := "header." + base64.StdEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account-parity"}}`)) + ".signature"
	polls := 0
	http.DefaultClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			return response(200, `{"device_auth_id":"device-auth-id","user_code":"ABCD-1234","interval":"0"}`), nil
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				return response(403, `{"error":"deviceauth_authorization_pending"}`), nil
			}
			return response(200, `{"authorization_code":"oauth-code","code_verifier":"device-code-verifier"}`), nil
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				return nil, err
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				return response(401, `{"error":{"message":"Could not validate your token. Please try signing in again.","type":"invalid_request_error"}}`), nil
			}
			return response(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-token","expires_in":3600}`, access)), nil
		}
		return nil, fmt.Errorf("unexpected URL %s", r.URL)
	})}
	var selection string
	var device ai.OAuthDeviceCodeInfo
	start := time.Now().UnixMilli()
	credentials, err := (ai.CodexOAuthProvider{}).LoginContext(context.Background(), ai.OAuthLoginCallbacks{OnSelect: func(prompt ai.OAuthSelectPrompt) (string, error) {
		selection = prompt.Message + "|" + prompt.Options[0].ID + "|" + prompt.Options[1].ID
		return "device_code", nil
	}, OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) { device = info }})
	if err != nil {
		return err
	}
	expiryOK := credentials.Expires >= start+3600000 && credentials.Expires <= time.Now().UnixMilli()+3600000
	_, refreshErr := (ai.CodexOAuthProvider{}).RefreshTokenContext(context.Background(), ai.OAuthCredentials{Refresh: "invalid-refresh-token"})
	if refreshErr == nil {
		return fmt.Errorf("refresh unexpectedly succeeded")
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Selection    string `json:"selection"`
		Device       string `json:"device"`
		Polls        int    `json:"polls"`
		Account      string `json:"account"`
		Tokens       bool   `json:"tokens"`
		Expiry       bool   `json:"expiry"`
		RefreshError string `json:"refreshError"`
	}{selection, fmt.Sprintf("%s|%s|%g|%g", device.UserCode, device.VerificationURI, device.IntervalSeconds, device.ExpiresInSeconds), polls, credentials.AccountID, credentials.Access == access && credentials.Refresh == "refresh-token", expiryOK, refreshErr.Error()})
}
