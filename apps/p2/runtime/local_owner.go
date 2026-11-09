package runtime

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Ports apps/p2/src/runtime/local-owner.ts: local owner sign-in, how a client on the p2 host (the TUI, `p2 owner-link`)
// proves that it runs as the server's user. Reading the private `<data>/owner-key` (0600, in the 0700 data directory) is
// the proof, but the key never leaves the client: it asks the server for a one-time challenge and answers with an HMAC of
// the challenge and the port. Whatever listens on that port learns nothing it could use elsewhere: a squatter on 8788
// cannot pass the answer to a p2 on another port, and a restarted p2 forgot the challenge. The answer earns a local owner
// session cookie (`plugins/devices`, `access: "local"`), which the client then sends like a browser.

// LocalChallengePath is the route of `POST`: a one-time challenge, `{ challenge }`.
const LocalChallengePath = "/owner/local/challenge"

// LocalClaimPath is the route of `POST { challenge, proof }`: a local owner session cookie (`Set-Cookie`).
const LocalClaimPath = "/owner/local/claim"

// LocalSignInKind is the outcome of a local sign-in.
type LocalSignInKind string

const (
	// LocalSignedIn: the cookie pair to send.
	LocalSignedIn LocalSignInKind = "signed-in"
	// LocalUnavailable: why the sign-in is unavailable (no readable owner key, a server that offers no local sign-in, or one
	// with another data directory).
	LocalUnavailable LocalSignInKind = "unavailable"
)

// LocalSignIn is a local sign-in: the cookie pair to send (Kind LocalSignedIn), or why it is unavailable (Message).
type LocalSignIn struct {
	Kind    LocalSignInKind
	Cookie  string
	Message string
}

// DataDirectory is the data directory the server uses by default: `P2_DATA_DIR`, else `~/.pi/p2`. env reads the
// environment.
func DataDirectory(env func(string) string) (string, error) {
	if dir := env("P2_DATA_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "p2"), nil
}

// LocalOwnerProof is the answer to a challenge: HMAC-SHA256 under the owner key of the server's port and the challenge.
func LocalOwnerProof(key string, port int, challenge string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("p2-local-owner:" + strconv.Itoa(port) + ":" + challenge))
	return hex.EncodeToString(mac.Sum(nil))
}

// localClient refuses redirects (a redirect is an error, never followed) and ignores proxy settings: the p2 on loopback is
// reached directly.
var localClient = &http.Client{
	Timeout:       10 * time.Second,
	Transport:     &http.Transport{Proxy: nil},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// LocalOwnerSignIn signs in to the p2 at the loopback origin target as its local owner, with the key of dataDir; an
// error when it is unreachable. A nil client refuses redirects and times out after 10 s.
func LocalOwnerSignIn(ctx context.Context, client *http.Client, target *url.URL, dataDir string) (LocalSignIn, error) {
	if client == nil {
		client = localClient
	}
	origin := target.Scheme + "://" + target.Host
	path := filepath.Join(dataDir, "owner-key")
	content, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
			return LocalSignIn{}, err
		}
		return LocalSignIn{Kind: LocalUnavailable, Message: "Cannot read " + path + ": local sign-in needs the user running the p2 server, with its P2_DATA_DIR"}, nil
	}
	key := strings.TrimSpace(string(content))
	port := 80
	if text := target.Port(); text != "" {
		if port, err = strconv.Atoi(text); err != nil {
			return LocalSignIn{}, err
		}
	}
	challenged, err := localPost(ctx, client, target, LocalChallengePath, map[string]any{})
	if err != nil {
		return LocalSignIn{}, err
	}
	defer challenged.Body.Close()
	var value struct {
		Challenge any `json:"challenge"`
	}
	if challenged.StatusCode >= 200 && challenged.StatusCode <= 299 {
		body, _ := io.ReadAll(io.LimitReader(challenged.Body, 1<<16))
		_ = json.Unmarshal(body, &value)
	}
	challenge, isString := value.Challenge.(string)
	if !isString {
		return LocalSignIn{Kind: LocalUnavailable, Message: fmt.Sprintf("The server at %s offers no local sign-in (%d)", origin, challenged.StatusCode)}, nil
	}
	claimed, err := localPost(ctx, client, target, LocalClaimPath, map[string]any{"challenge": challenge, "proof": LocalOwnerProof(key, port, challenge)})
	if err != nil {
		return LocalSignIn{}, err
	}
	defer claimed.Body.Close()
	_, _ = io.Copy(io.Discard, claimed.Body)
	if claimed.StatusCode == http.StatusForbidden {
		return LocalSignIn{Kind: LocalUnavailable, Message: "The p2 server at " + origin + " does not use the data directory " + dataDir + ": set P2_DATA_DIR to its data directory, or sign in with an owner link"}, nil
	}
	cookie := ""
	if claimed.StatusCode >= 200 && claimed.StatusCode <= 299 {
		var pairs []string
		for _, header := range claimed.Header.Values("Set-Cookie") {
			pairs = append(pairs, strings.SplitN(header, ";", 2)[0])
		}
		cookie = strings.Join(pairs, "; ")
	}
	if cookie == "" {
		return LocalSignIn{}, fmt.Errorf("Local sign-in at %s failed (%d)", origin, claimed.StatusCode)
	}
	return LocalSignIn{Kind: LocalSignedIn, Cookie: cookie}, nil
}

// localPost is a same-origin JSON POST without redirects; an unreachable server is one clear error.
func localPost(ctx context.Context, client *http.Client, target *url.URL, path string, body map[string]any) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	next, err := target.Parse(path)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, next.String(), bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Origin", target.Scheme+"://"+target.Host)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Cannot reach p2 at %s://%s: %w", target.Scheme, target.Host, err)
	}
	return response, nil
}
