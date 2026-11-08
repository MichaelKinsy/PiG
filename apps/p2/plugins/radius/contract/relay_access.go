package contract

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/radius/relay-access.ts.

const (
	// RelaySecretHeader marks loopback requests the Radius relay client makes on behalf of a relayed browser.
	RelaySecretHeader = "x-p2-relay"
	// RelayOriginHeader is the brain's browser-facing origin (its cortex origin), e.g. `https://<label>.rdtun.com`.
	RelayOriginHeader = "x-p2-relay-origin"
	// RelayShellHeader is the Radius shell origin that frames the brain UI; absent for a Pi client of the owner, which no
	// page frames.
	RelayShellHeader = "x-p2-relay-shell"
	// RelayUserHeader is a Radius user other than the owner: base64url JSON `{ id, email }`. Absent for the owner.
	RelayUserHeader = "x-p2-relay-user"
)

// RelayedRequest is what a verified relayed request says about itself.
type RelayedRequest struct {
	PublicOrigin string
	ShellOrigin  string
	// HasShell is whether the request names a shell origin (ShellOrigin may not be empty then).
	HasShell bool
	User     *p2runtime.RelayUser
}

var originPattern = regexp.MustCompile(`^https?://[^/\s]+$`)

// RelayAccess distinguishes requests relayed through Radius from local and tunneled ones.
//
// Problem: the relay client (brain.go) serves relayed browsers by making loopback requests to this server, which would
// otherwise look like the device itself and receive localhost-only owner powers (minting owner tokens, the automatic owner
// cookie, provider login).
//
// The relay client marks its loopback requests with a per-process secret. A marked request comes from a browser Radius
// authenticated on the cortex origin, and is treated as remote, so every localhost-only path stays closed. Without the user
// header it is the brain's owner. With it, it is a person the owner shared sessions with by email: Radius vouches for their
// email, and p2's sharing decides what they may reach, as for a guest cookie on a tunnel host. Without the secret the marker
// headers mean nothing, so neither browsers nor other local processes can claim them. Without the shell header it is the
// owner's Pi client, the TUI launcher through Radius (brain.go, "Client channels").
type RelayAccess struct {
	secret string
}

// NewRelayAccess makes a relay access with a random secret, which the process logs redact.
func NewRelayAccess() *RelayAccess {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	access := &RelayAccess{secret: hex.EncodeToString(random)}
	p2runtime.ProcessLogs.Redactor.AddSecret(access.secret)
	return access
}

// Secret is the per-process secret that marks the brain's loopback requests.
func (a *RelayAccess) Secret() string { return a.secret }

// Verify is the relayed request request is, or nil when it is not one this brain made. A user header the brain never writes is
// an error: Pi's `JSON.parse` of it throws and the request fails.
func (a *RelayAccess) Verify(request *http.Request) (*RelayedRequest, error) {
	if !isLoopbackLocal(request) {
		return nil, nil
	}
	presented := request.Header.Get(RelaySecretHeader)
	if presented == "" || len(presented) != len(a.secret) || subtle.ConstantTimeCompare([]byte(presented), []byte(a.secret)) != 1 {
		return nil, nil
	}
	publicOrigin := request.Header.Get(RelayOriginHeader)
	if !originPattern.MatchString(publicOrigin) {
		return nil, nil
	}
	_, hasShell := request.Header[http.CanonicalHeaderKey(RelayShellHeader)]
	user, hasUser := request.Header[http.CanonicalHeaderKey(RelayUserHeader)]
	if !hasShell {
		// Clients are the owner's only; sharing admits people on the brain's UI.
		if hasUser {
			return nil, nil
		}
		return &RelayedRequest{PublicOrigin: publicOrigin}, nil
	}
	shell := request.Header.Get(RelayShellHeader)
	if !originPattern.MatchString(shell) {
		return nil, nil
	}
	if !hasUser {
		return &RelayedRequest{PublicOrigin: publicOrigin, ShellOrigin: shell, HasShell: true}, nil
	}
	decoded, err := decodeRelayUser(strings.Join(user, ", "))
	if err != nil {
		return nil, err
	}
	return &RelayedRequest{PublicOrigin: publicOrigin, ShellOrigin: shell, HasShell: true, User: decoded}, nil
}

// isLoopbackLocal is whether the connection's local address is 127.0.0.1.
func isLoopbackLocal(request *http.Request) bool {
	address, _ := request.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if address == nil {
		return false
	}
	tcp, ok := address.(*net.TCPAddr)
	return ok && tcp.IP.Equal(net.IPv4(127, 0, 0, 1)) && tcp.IP.To4() != nil
}

// EncodeRelayUser is the header value of a Radius user; the brain writes it, so it is plain ASCII whatever the name.
func EncodeRelayUser(user p2runtime.RelayUser) string {
	// JSON.stringify({ id, email }): the members in this order, `<`, `>` and `&` not escaped.
	encoded, _ := chordjson.Marshal(struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}{user.ID, user.Email})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// decodeRelayUser reads the header value as Node's `Buffer.from(value, "base64url")` does (characters outside the alphabet are
// skipped, padding is optional, `+` and `/` count as `-` and `_`), then as `JSON.parse` does.
func decodeRelayUser(value string) (*p2runtime.RelayUser, error) {
	letters := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c == '+':
			letters = append(letters, '-')
		case c == '/':
			letters = append(letters, '_')
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			letters = append(letters, c)
		}
	}
	if len(letters)%4 == 1 {
		letters = letters[:len(letters)-1]
	}
	raw, err := base64.RawURLEncoding.DecodeString(string(letters))
	if err != nil {
		return nil, err
	}
	var user struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(raw, &user); err != nil {
		return nil, err
	}
	return &p2runtime.RelayUser{ID: user.ID, Email: user.Email}, nil
}
