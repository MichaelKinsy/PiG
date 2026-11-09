package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports apps/p2/src/runtime/owner-link.ts and apps/p2/scripts/owner-link.ts: owner links (`/owner#<token>`) and the origins they are for, checked by the
// owner-link CLI and the TUI launcher. URLs parse as WHATWG's `new URL` does (internal/nodeurl).

// OwnerOrigin parses value as the http(s) origin an owner link is for: no credentials, path, query or fragment, and plain
// http only for loopback. Shared by the shell CLI and the TUI; an invitation never goes in a path or query.
func OwnerOrigin(value string) (nodeurl.URL, error) {
	url, err := nodeurl.ParseURL(value)
	if err != nil {
		return nodeurl.URL{}, errors.New("Invalid URL; use an http(s) origin")
	}
	if !slices.Contains([]string{"http:", "https:"}, url.Protocol) || url.Username != "" || url.Password != "" ||
		url.Pathname != "/" || url.Query != "" || url.Hash != "" {
		return nodeurl.URL{}, errors.New("Use an http(s) origin URL without credentials, path or query")
	}
	if url.Protocol != "https:" && !slices.Contains([]string{"127.0.0.1", "localhost", "[::1]"}, url.Hostname) {
		return nodeurl.URL{}, errors.New("Remote connections require HTTPS")
	}
	return url, nil
}

var ownerFragment = regexp.MustCompile(`^#[a-f0-9]{64}$`)

var tokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// OwnerLinkToken is the token of the owner link value for origin: the fragment of `<origin>/owner#<64 hex digits>`.
func OwnerLinkToken(value string, origin nodeurl.URL) (string, error) {
	link, err := nodeurl.ParseURL(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("Invalid owner link")
	}
	// A query, even an empty one, is refused as Pi's `link.search` is: only an empty one is "" there too, so the parsed
	// query decides.
	if link.Origin() != origin.Origin() || link.Username != "" || link.Password != "" || link.Pathname != "/owner" ||
		link.Query != "" || !ownerFragment.MatchString(link.Hash) {
		return "", errors.New("Use an owner link for this origin with its token in the fragment")
	}
	return link.Hash[1:], nil
}

// MintOwnerToken is a fresh one-time owner-link token, minted by the local owner of the p2 at loopback server (signed in
// with the key of dataDir). A nil client refuses redirects and times out after 10 s.
func MintOwnerToken(ctx context.Context, client *http.Client, server *url.URL, dataDir string) (string, error) {
	signIn, err := LocalOwnerSignIn(ctx, client, server, dataDir)
	if err != nil {
		return "", err
	}
	if signIn.Kind == LocalUnavailable {
		return "", errors.New(signIn.Message)
	}
	if client == nil {
		client = localClient
	}
	next, err := server.Parse("/owner-token")
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Cookie", signIn.Cookie)
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Cannot mint an owner link: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", fmt.Errorf("Cannot mint an owner link (%d)", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	var value struct {
		Token any `json:"token"`
	}
	if err != nil || json.Unmarshal(body, &value) != nil {
		return "", errors.New("Invalid owner-token response")
	}
	token, _ := value.Token.(string)
	if !tokenPattern.MatchString(token) {
		return "", errors.New("Invalid owner-token response")
	}
	return token, nil
}

// MintOwnerLink is a fresh one-time owner link for base, minted by the local owner of the p2 at loopback server (signed in
// with the key of dataDir). The link is a credential: print it to the invoking user only.
func MintOwnerLink(ctx context.Context, client *http.Client, server *url.URL, dataDir string, base nodeurl.URL) (string, error) {
	token, err := MintOwnerToken(ctx, client, server, dataDir)
	if err != nil {
		return "", err
	}
	return base.Origin() + "/owner#" + token, nil
}

// OwnerLink is `p2 owner-link [base-url]`: it signs in to the running server on loopback (`PORT`, 8788) as its local owner
// with the owner key of the data directory (`P2_DATA_DIR`), asks it for an invitation and returns the link to open on
// another device. env reads the environment (`P2_PUBLIC_URL`, `PORT`, `P2_DATA_DIR`).
func OwnerLink(ctx context.Context, args []string, env func(string) string) (string, error) {
	if len(args) > 1 {
		return "", errors.New("Usage: p2 owner-link [base-url]")
	}
	portText := env("PORT")
	if portText == "" {
		portText = "8788"
	}
	base := "http://127.0.0.1:" + portText
	if public := env("P2_PUBLIC_URL"); public != "" {
		base = public
	}
	if len(args) == 1 {
		base = args[0]
	}
	origin, err := OwnerOrigin(base)
	if err != nil {
		return "", err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("Invalid PORT")
	}
	dataDir, err := DataDirectory(env)
	if err != nil {
		return "", err
	}
	// Direct loopback, signed in as the local owner with the private data directory's key (the server's user only).
	return MintOwnerLink(ctx, nil, &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}, dataDir, origin)
}
