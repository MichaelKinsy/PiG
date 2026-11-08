package host

// The launcher's connection settings and sign-in (p2-tui-port.md section 4.1): stored cookies, owner links, share links and the command line.
//
// pi: apps/p2/src/hosts/tui-config.ts, apps/p2/src/runtime/owner-link.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Credentials are what the launcher sends: only a cookie, and only by the launcher.
type Credentials struct {
	Cookie string `json:"cookie,omitempty"`
}

// Guest is a share-link guest of one origin (section 4.1): its own cookie, separate from an owner cookie.
type Guest struct {
	Cookie  string `json:"cookie"`
	Session string `json:"session,omitempty"`
}

// Connection is the stored sign-in of one origin.
type Connection struct {
	Credentials
	Guest *Guest `json:"guest,omitempty"`
}

// MarshalJSON writes the cookie before the guest, as the config file has them.
func (c Connection) MarshalJSON() ([]byte, error) {
	type plain struct {
		Cookie string `json:"cookie,omitempty"`
		Guest  *Guest `json:"guest,omitempty"`
	}
	return json.Marshal(plain{c.Cookie, c.Guest})
}

// Config is config.json.
type Config struct {
	Connections map[string]Connection `json:"connections"`
}

// Admission is who the launcher signed in as.
type Admission struct {
	Credentials
	Role    string // "owner" or "guest"
	Session string
}

var (
	sessionID    = lazyregexp.New(`^[\w-]{1,128}$`)
	guestCookie  = lazyregexp.New(`^p2_guest_\d+=`)
	hashToken    = lazyregexp.New(`^#[a-f0-9]{64}$`)
	joinPath     = lazyregexp.New(`^/join/([\w-]{43})$`)
	serverID     = lazyregexp.New(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// validCookie is `^[\x21-\x7e]{1,4096}$`.
func validCookie(cookie string) bool {
	if len(cookie) == 0 || len(cookie) > 4096 {
		return false
	}
	for i := 0; i < len(cookie); i++ {
		if cookie[i] < 0x21 || cookie[i] > 0x7e {
			return false
		}
	}
	return true
}

// ReadConfig reads config.json from directory; a missing file is an empty config.
func ReadConfig(directory string) (Config, error) {
	bad := errors.New("Cannot read p2-tui config.json")
	data, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{Connections: map[string]Connection{}}, nil
	}
	if err != nil {
		return Config{}, bad
	}
	var raw struct {
		Connections map[string]json.RawMessage `json:"connections"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Connections == nil {
		return Config{}, bad
	}
	config := Config{Connections: map[string]Connection{}}
	for origin, value := range raw.Connections {
		var entry struct {
			Cookie *json.RawMessage `json:"cookie"`
			Guest  json.RawMessage  `json:"guest"`
		}
		if json.Unmarshal(value, &entry) != nil {
			return Config{}, bad
		}
		var connection Connection
		if entry.Cookie != nil && string(*entry.Cookie) != "null" {
			var cookie string
			if json.Unmarshal(*entry.Cookie, &cookie) != nil {
				return Config{}, bad
			}
			// Only cookies survive. Old proxy passwords are neither used nor saved again.
			connection.Cookie = cookie
		}
		var guest map[string]any
		if json.Unmarshal(entry.Guest, &guest) == nil && guest != nil {
			if cookie, ok := guest["cookie"].(string); ok {
				if !validCookie(cookie) {
					return Config{}, bad
				}
				connection.Guest = &Guest{Cookie: cookie}
				if session, ok := guest["session"].(string); ok && sessionID.MatchString(session) {
					connection.Guest.Session = session
				}
			}
		}
		config.Connections[origin] = connection
	}
	return config, nil
}

// SaveConfig writes config.json (0600) into directory (0700).
func SaveConfig(config Config, directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "\t")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(config); err != nil {
		return err
	}
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// OriginOf is the WHATWG origin of u: scheme and host, without the scheme's default port.
func OriginOf(u *url.URL) string {
	host := u.Host
	if port := u.Port(); (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		host = strings.TrimSuffix(host, ":"+port)
	}
	return u.Scheme + "://" + strings.ToLower(host)
}

func isLoopbackHost(u *url.URL) bool {
	name := u.Hostname()
	return name == "127.0.0.1" || name == "localhost" || name == "::1"
}

// ConnectionHeaders are the exact Origin and the cookie, if any.
func ConnectionHeaders(u *url.URL, credentials Credentials) http.Header {
	headers := http.Header{"Origin": {OriginOf(u)}}
	if credentials.Cookie != "" {
		headers.Set("Cookie", credentials.Cookie)
	}
	return headers
}

// httpClient refuses redirects: a redirect is an error, never followed with credentials.
var httpClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
}

func fixedRequest(client *http.Client, ctx context.Context, method string, target *url.URL, path string, headers http.Header, body []byte) (*http.Response, error) {
	next, err := target.Parse(path)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, next.String(), reader)
	if err != nil {
		return nil, err
	}
	for name, values := range headers {
		request.Header[name] = values
	}
	if client == nil {
		client = httpClient
	}
	return client.Do(request)
}

func setCookies(response *http.Response) []string {
	var pairs []string
	for _, header := range response.Header.Values("Set-Cookie") {
		pairs = append(pairs, strings.SplitN(header, ";", 2)[0])
	}
	return pairs
}

// OwnerOrigin checks the origin of an owner or share link: shared by the shell CLI and the TUI; credentials and an invitation never appear in a
// path or query.
func OwnerOrigin(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return nil, errors.New("Invalid URL; use an http(s) origin")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.HasSuffix(value, "#") {
		return nil, errors.New("Use an http(s) origin URL without credentials, path or query")
	}
	if u.Scheme != "https" && !isLoopbackHost(u) {
		return nil, errors.New("Remote connections require HTTPS")
	}
	u.Path = "/"
	return u, nil
}

// OwnerLinkToken is the token in an owner link `<origin>/owner#<token>` for origin.
func OwnerLinkToken(value string, origin *url.URL) (string, error) {
	link, err := url.Parse(strings.TrimSpace(value))
	if err != nil || link.Host == "" {
		return "", errors.New("Invalid owner link")
	}
	if OriginOf(link) != OriginOf(origin) || link.User != nil || link.Path != "/owner" || link.RawQuery != "" || link.ForceQuery || !hashToken.MatchString("#"+link.Fragment) {
		return "", errors.New("Use an owner link for this origin with its token in the fragment")
	}
	return link.Fragment, nil
}

// ClaimOwner redeems an owner link's token (POST /owner/claim).
func ClaimOwner(client *http.Client, ctx context.Context, target *url.URL, token string, credentials Credentials) (Credentials, error) {
	body, _ := json.Marshal(map[string]string{"token": token})
	headers := ConnectionHeaders(target, credentials)
	headers.Set("Content-Type", "application/json")
	response, err := fixedRequest(client, ctx, http.MethodPost, target, "/owner/claim", headers, body)
	if err != nil {
		return Credentials{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Credentials{}, errors.New("Owner claim failed; generate a fresh link with npm run owner-link on the host")
	}
	cookie := strings.Join(setCookies(response), "; ")
	if cookie == "" {
		return Credentials{}, errors.New("Owner claim did not return a cookie")
	}
	return Credentials{Cookie: cookie}, nil
}

// JoinLinkToken is the token of a share link `<origin>/join/<token>` (plugins/sharing). Its origin must be the TUI's.
func JoinLinkToken(value string, origin *url.URL) (string, error) {
	link, err := url.Parse(strings.TrimSpace(value))
	if err != nil || link.Host == "" {
		return "", errors.New("Invalid share link")
	}
	match := joinPath.FindStringSubmatch(link.Path)
	if OriginOf(link) != OriginOf(origin) || link.User != nil || link.RawQuery != "" || link.ForceQuery || link.Fragment != "" || match == nil {
		return "", errors.New("Use a share link for this origin (https://host/join/<token>)")
	}
	return match[1], nil
}

// JoinLinkOrigin is the origin of a share link, for `--join` without a URL argument.
func JoinLinkOrigin(value string) (*url.URL, error) {
	link, err := url.Parse(strings.TrimSpace(value))
	if err != nil || link.Host == "" {
		return nil, errors.New("Invalid share link")
	}
	return OwnerOrigin(OriginOf(link))
}

// GuestName is a display name as the join page accepts it: one line, at most 40 characters, not verified by anyone.
func GuestName(value string) (string, error) {
	var b strings.Builder
	for _, r := range value {
		if r > 0x1f && (r < 0x7f || r > 0x9f) {
			b.WriteRune(r)
		}
	}
	name := strings.TrimSpace(b.String())
	if runes := []rune(name); len(runes) > 40 {
		name = string(runes[:40])
	}
	if name == "" {
		return "", errors.New("Enter a name")
	}
	return name, nil
}

func readLimited(response *http.Response, limit int) (string, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", errors.New("Join response exceeds its size limit")
	}
	return string(data), nil
}

// RedeemJoin redeems a share link (POST /join): exact Origin, no redirects, bounded body; keeps an existing guest identity.
func RedeemJoin(client *http.Client, ctx context.Context, target *url.URL, token, name string, guest *Guest) (Guest, error) {
	credentials := Credentials{}
	if guest != nil {
		credentials.Cookie = guest.Cookie
	}
	body, _ := json.Marshal(map[string]string{"token": token, "name": name})
	headers := ConnectionHeaders(target, credentials)
	headers.Set("Content-Type", "application/json")
	response, err := fixedRequest(client, ctx, http.MethodPost, target, "/join", headers, body)
	if err != nil {
		return Guest{}, err
	}
	defer response.Body.Close()
	text, err := readLimited(response, 2048)
	if err != nil {
		return Guest{}, err
	}
	var value map[string]any
	if json.Unmarshal([]byte(text), &value) != nil || value == nil {
		return Guest{}, errors.New("Invalid join response")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		// The server's own short message ("This share has ended", "You were removed ..."), as one plain line.
		if message, ok := value["error"].(string); ok {
			line, err := GuestName(message)
			if err != nil {
				return Guest{}, err
			}
			return Guest{}, errors.New(line)
		}
		return Guest{}, errors.New("Could not join this session")
	}
	session, ok := value["session"].(string)
	if !ok || !sessionID.MatchString(session) {
		return Guest{}, errors.New("Invalid join response")
	}
	cookie := ""
	for _, pair := range setCookies(response) {
		if guestCookie.MatchString(pair) {
			cookie = pair
			break
		}
	}
	if cookie == "" && guest != nil {
		cookie = guest.Cookie
	}
	if cookie == "" || !validCookie(cookie) {
		return Guest{}, errors.New("Join did not return a guest cookie")
	}
	return Guest{Cookie: cookie, Session: session}, nil
}

// CheckGuest is whether a guest cookie is still admitted: `/__p2_tui` serves admitted guests only (401/403 when it ended).
func CheckGuest(client *http.Client, ctx context.Context, target *url.URL, guest Guest) (bool, error) {
	response, err := fixedRequest(client, ctx, http.MethodHead, target, "/__p2_tui", ConnectionHeaders(target, Credentials{Cookie: guest.Cookie}), nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		return true, nil
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return false, nil
	}
	return false, errors.New("Cannot check guest access")
}

// Prompt asks the visitor for a "link" or a "name".
type Prompt func(kind string) (string, error)

// AuthOptions are the sign-in arguments.
type AuthOptions struct {
	URL       *url.URL
	OwnerLink string
	JoinLink  string
	// HTTP is the client of the sign-in requests (a test's); nil refuses redirects and times out after 10 s.
	HTTP *http.Client
}

// Authenticate is the sign-in (section 4.1). An expired or remotely revoked cookie leads back to sign-in, not an endless unauthenticated reconnect.
// Loopback is the device owner unless `--join` explicitly asks for a guest view; a stored guest cookie never downgrades it. Remote origins use a valid
// owner cookie, else a valid guest cookie, else a prompt for either link.
func Authenticate(ctx context.Context, options AuthOptions, config *Config, prompt Prompt) (Admission, error) {
	target := options.URL
	origin := OriginOf(target)
	loopback := target.Scheme == "http" && isLoopbackHost(target)
	stored := config.Connections[origin]
	join := func(link string) (Admission, error) {
		token, err := JoinLinkToken(link, target)
		if err != nil {
			return Admission{}, err
		}
		name := ""
		for name == "" {
			answer, err := prompt("name")
			if err != nil {
				return Admission{}, err
			}
			if name, err = GuestName(answer); err != nil && err.Error() != "Enter a name" {
				return Admission{}, err
			}
		}
		guest, err := RedeemJoin(options.HTTP, ctx, target, token, name, stored.Guest)
		if err != nil {
			return Admission{}, err
		}
		connection := config.Connections[origin]
		connection.Guest = &guest
		config.Connections[origin] = connection
		return Admission{Credentials: Credentials{Cookie: guest.Cookie}, Role: "guest", Session: guest.Session}, nil
	}
	if options.JoinLink != "" {
		return join(options.JoinLink)
	}
	// Direct local access is the device owner, not an associated remote cookie principal.
	credentials := Credentials{Cookie: stored.Cookie}
	if loopback && options.OwnerLink == "" {
		credentials = Credentials{}
	}
	if options.OwnerLink == "" && (credentials.Cookie != "" || loopback) {
		// The existing core owner-only endpoint also works before the Devices protocol is upgraded.
		path := "/owner/check"
		if loopback {
			path = "/__p2_dev"
		}
		response, err := fixedRequest(options.HTTP, ctx, http.MethodGet, target, path, ConnectionHeaders(target, credentials), nil)
		if err != nil {
			return Admission{}, err
		}
		defer response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode <= 299 {
			if loopback {
				_, _ = io.Copy(io.Discard, response.Body)
				return Admission{Role: "owner"}, nil
			}
			var value struct {
				Owner bool `json:"owner"`
			}
			if json.NewDecoder(response.Body).Decode(&value) == nil && value.Owner {
				return Admission{Credentials: credentials, Role: "owner"}, nil
			}
			return Admission{}, errors.New("Invalid owner-access response")
		}
		_, _ = io.Copy(io.Discard, response.Body)
		if response.StatusCode != 401 && response.StatusCode != 403 {
			return Admission{}, errors.New("Cannot check owner access")
		}
	}
	if options.OwnerLink == "" && !loopback && stored.Guest != nil {
		ok, err := CheckGuest(options.HTTP, ctx, target, *stored.Guest)
		if err != nil {
			return Admission{}, err
		}
		if ok {
			return Admission{Credentials: Credentials{Cookie: stored.Guest.Cookie}, Role: "guest", Session: stored.Guest.Session}, nil
		}
		// Reset or removed share, or an archived session: forget the cookie and ask for a new link.
		connection := config.Connections[origin]
		connection.Guest = nil
		config.Connections[origin] = connection
	}
	link := options.OwnerLink
	if link == "" {
		var err error
		if link, err = prompt("link"); err != nil {
			return Admission{}, err
		}
		if strings.Contains(link, "/join/") {
			return join(link)
		}
	}
	token, err := OwnerLinkToken(link, target)
	if err != nil {
		return Admission{}, err
	}
	credentials, err = ClaimOwner(options.HTTP, ctx, target, token, credentials)
	if err != nil {
		return Admission{}, err
	}
	connection := config.Connections[origin]
	connection.Credentials = credentials
	config.Connections[origin] = connection
	return Admission{Credentials: credentials, Role: "owner"}, nil
}

// ForgetGuest forgets a guest cookie the server no longer admits (revocation while running).
func ForgetGuest(config *Config, origin string) {
	if connection, ok := config.Connections[origin]; ok && connection.Guest != nil {
		connection.Guest = nil
		config.Connections[origin] = connection
	}
}

// EndedNotice is the launcher's own notice when the server stops admitting the stored cookie (the web page's wording).
func EndedNotice(role string) string {
	if role == "guest" {
		return "This share has ended."
	}
	return "Signed out on this device."
}

// Resumed is what the visitor chose after a refused reconnect.
type Resumed struct{ JoinLink, OwnerLink string }

// ResumeAfterEnded forgets the cookie kind the launcher stored (it knows which; the worker's role is not consulted), then offers to continue with a
// new link for this origin. It returns nil when the visitor quits (an empty line).
func ResumeAfterEnded(role string, target *url.URL, config *Config, ask func(question string) (string, error)) (*Resumed, error) {
	origin := OriginOf(target)
	if role == "guest" {
		ForgetGuest(config, origin)
	} else {
		connection := config.Connections[origin]
		connection.Cookie = ""
		config.Connections[origin] = connection
	}
	question := "Owner link to sign in again (npm run owner-link on the host; Enter to quit): "
	if role == "guest" {
		question = "Paste a new share link to join again (Enter to quit): "
	}
	for {
		answer, err := ask(question)
		if err != nil {
			return nil, err
		}
		link := strings.TrimSpace(answer)
		if link == "" {
			return nil, nil
		}
		var problem error
		if strings.Contains(link, "/join/") {
			if _, problem = JoinLinkToken(link, target); problem == nil {
				return &Resumed{JoinLink: link}, nil
			}
		} else if _, problem = OwnerLinkToken(link, target); problem == nil {
			return &Resumed{OwnerLink: link}, nil
		}
		question = problem.Error() + ". Try again (Enter to quit): "
	}
}

// Args is the parsed command line.
type Args struct {
	URL       *url.URL
	OwnerLink string
	JoinLink  string
	Theme     string
	NoThemes  bool
	// Radius is set by `--radius`; RadiusServer is its optional server id.
	Radius       bool
	RadiusServer string
}

// ParseArgs parses the TUI's command line; env looks up an environment variable.
func ParseArgs(args []string, env func(string) string) (Args, error) {
	var urlText string
	hasURL := false
	ownerLink := env("P2_TUI_OWNER_LINK")
	explicitOwner := false
	var parsed Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() string {
			i++
			if i < len(args) {
				return args[i]
			}
			return ""
		}
		switch {
		case arg == "--radius":
			// Optionally followed by the server's ID; without it, the user's only online server.
			parsed.Radius = true
			if i+1 < len(args) && serverID.MatchString(args[i+1]) {
				parsed.RadiusServer = next()
			}
		case arg == "--owner-link":
			explicitOwner = true
			if ownerLink = next(); ownerLink == "" {
				return Args{}, errors.New("--owner-link needs a value")
			}
		case arg == "--join":
			if parsed.JoinLink = next(); parsed.JoinLink == "" {
				return Args{}, errors.New("--join needs a share link")
			}
		case arg == "--theme":
			// pi's --theme: this run's theme setting (a name or "light/dark" pair).
			if parsed.Theme = next(); parsed.Theme == "" {
				return Args{}, errors.New("--theme needs a value")
			}
		case arg == "--no-themes":
			parsed.NoThemes = true
		case strings.HasPrefix(arg, "-"):
			return Args{}, fmt.Errorf("Unknown TUI option: %s", arg)
		default:
			urlText, hasURL = arg, true
		}
	}
	if parsed.JoinLink != "" && ownerLink != "" && explicitOwner {
		return Args{}, errors.New("Use either --join or --owner-link")
	}
	if parsed.Radius && (hasURL || parsed.JoinLink != "" || explicitOwner) {
		return Args{}, errors.New("--radius signs in through Radius: no URL, owner link or share link")
	}
	if parsed.JoinLink != "" || parsed.Radius {
		ownerLink = ""
	}
	var err error
	switch {
	case !hasURL && parsed.JoinLink != "":
		parsed.URL, err = JoinLinkOrigin(parsed.JoinLink)
	default:
		if !hasURL {
			port := env("PORT")
			if port == "" {
				port = "8788"
			}
			urlText = "http://127.0.0.1:" + port
		}
		parsed.URL, err = OwnerOrigin(urlText)
	}
	if err != nil {
		return Args{}, err
	}
	if ownerLink != "" {
		if _, err := OwnerLinkToken(ownerLink, parsed.URL); err != nil {
			return Args{}, err
		}
	}
	if parsed.JoinLink != "" {
		if _, err := JoinLinkToken(parsed.JoinLink, parsed.URL); err != nil {
			return Args{}, err
		}
	}
	parsed.OwnerLink = ownerLink
	return parsed, nil
}

// sortedOrigins is the config's origins, alphabetical (for stable listings).
func (c Config) sortedOrigins() []string {
	origins := make([]string, 0, len(c.Connections))
	for origin := range c.Connections {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	return origins
}
