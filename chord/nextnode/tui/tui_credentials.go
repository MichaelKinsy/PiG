package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/node/tui/credentials.ts. The launcher's credentials (slice 11 §4, decision "The app kit, point 3"
// (c)): a device token the launcher keeps, never the sandbox. It is given, read from a token file (the owner token file on the
// server's machine, slice 13), or got by device pairing: the launcher asks for a code, shows it, and polls until the owner
// approved it in a browser; a paired token may be saved to a file (mode 0600) for the next start.

// CredentialsKind is where the launcher's token comes from.
type CredentialsKind string

const (
	// CredentialsToken is a token given.
	CredentialsToken CredentialsKind = "token"
	// CredentialsFile is a token file.
	CredentialsFile CredentialsKind = "file"
	// CredentialsPairing is pairing with the server.
	CredentialsPairing CredentialsKind = "pairing"
)

// TerminalCredentials is where the launcher's token comes from: Token for CredentialsToken, Path for CredentialsFile, and for
// CredentialsPairing the device's Label and the Save path the token is saved to (created, mode 0600) when not empty.
type TerminalCredentials struct {
	Kind  CredentialsKind
	Token string
	Path  string
	Label string
	Save  string
}

// pollInterval is how often a pairing is polled.
var pollInterval = 2000 * time.Millisecond

// plainCode is a pairing code the launcher shows: plain characters only.
var plainCode = regexp.MustCompile(`^[A-Za-z0-9-]{4,32}$`)

// TokenOf is the launcher's token for the server at base; pairing shows its code through say. client is the HTTP client (the
// default one when nil).
func TokenOf(ctx context.Context, credentials TerminalCredentials, base *url.URL, say func(line string), client *http.Client) (string, error) {
	switch credentials.Kind {
	case CredentialsToken:
		return credentials.Token, nil
	case CredentialsFile:
		text, err := os.ReadFile(credentials.Path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(text)), nil
	case CredentialsPairing:
		if client == nil {
			client = http.DefaultClient
		}
		return pair(ctx, credentials, base, say, client)
	}
	return "", fmt.Errorf("terminal: no such credentials %q", credentials.Kind)
}

// pair pairs this device: a code shown, the approval polled for, the token returned (and saved when asked).
func pair(ctx context.Context, credentials TerminalCredentials, base *url.URL, say func(string), client *http.Client) (string, error) {
	asked, err := post(ctx, client, base.JoinPath("/_chord/pair"), map[string]any{"label": credentials.Label})
	if err != nil {
		return "", err
	}
	if asked.status < 200 || asked.status > 299 {
		return "", fmt.Errorf("terminal: the server refused to pair (%d)", asked.status)
	}
	var answer struct {
		Code any `json:"code"`
		Poll any `json:"poll"`
	}
	if err := json.Unmarshal(asked.body, &answer); err != nil {
		return "", err
	}
	// A trust boundary: the server's answer is shown on the terminal and sent back.
	code, _ := answer.Code.(string)
	poll, pollOK := answer.Poll.(string)
	if !plainCode.MatchString(code) || !pollOK {
		return "", errors.New("terminal: the server's pairing answer is malformed")
	}
	say(fmt.Sprintf("Pairing code %s: approve it at %s (signed in as the owner).", code, base.JoinPath("/_chord/pair/"+code)))
	for {
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
		polled, err := post(ctx, client, base.JoinPath("/_chord/pair/poll"), map[string]any{"poll": poll})
		if err != nil {
			return "", err
		}
		if polled.status < 200 || polled.status > 299 {
			return "", errors.New("terminal: the pairing ended without approval")
		}
		var approval struct {
			Status string  `json:"status"`
			Token  *string `json:"token"`
		}
		if err := json.Unmarshal(polled.body, &approval); err != nil {
			return "", err
		}
		if approval.Status != "approved" || approval.Token == nil {
			continue
		}
		if credentials.Save != "" {
			if _, err := app.WriteSecret(credentials.Save, *approval.Token, app.SecretCreate); err != nil {
				return "", err
			}
		}
		return *approval.Token, nil
	}
}

// reply is an HTTP answer read whole.
type reply struct {
	status int
	body   []byte
}

// post is a JSON post.
func post(ctx context.Context, client *http.Client, target *url.URL, body any) (reply, error) {
	text, err := jsstring.MarshalJSON(body)
	if err != nil {
		return reply{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(text))
	if err != nil {
		return reply{}, err
	}
	request.Header.Set("content-type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = response.Body.Close() }()
	read, err := io.ReadAll(io.LimitReader(response.Body, maxBundle+1))
	return reply{status: response.StatusCode, body: read}, err
}
