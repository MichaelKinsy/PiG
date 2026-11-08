package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sync"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports apps/p2/plugins/mcp/credentials.ts: the MCP servers' OAuth credentials, `<P2_DATA_DIR>/mcp-auth.json` (private,
// 0600): per server, by name and URL as in pi, the registered client, the tokens and a pending sign-in's PKCE verifier. Only
// the server's manager reads and writes it; tokens never leave the server.

// McpCredentials is the credential file and the refresh locks of its servers.
type McpCredentials struct {
	file *p2runtime.LockedFile
	// refreshing is the refresh lock of each server key: one p2 server per data directory, so a lock in this process
	// suffices.
	mu         sync.Mutex
	refreshing map[string]chan struct{}
}

// NewMcpCredentials is the credential file of dataDir.
func NewMcpCredentials(dataDir string) *McpCredentials {
	return &McpCredentials{file: p2runtime.NewLockedFile(filepath.Join(dataDir, "mcp-auth.json")), refreshing: map[string]chan struct{}{}}
}

// keyOf is a server's key, as pi's: servers sharing a URL keep separate accounts.
func keyOf(name, url string) (string, error) {
	href, err := nodeurl.ParseHTTPHref(url)
	if err != nil {
		return "", err
	}
	return pimcp.McpNamespace(name) + "|" + href.Href, nil
}

// parseStates is the file's states (a trust boundary: an edited file that is not an object counts as none).
func parseStates(current *string) (*orderedjson.Object, error) {
	if current == nil || trimmed(*current) == "" {
		return orderedjson.New(), nil
	}
	text := p2runtime.StripBOM(*current)
	var probe json.RawMessage
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return nil, err
	}
	if !orderedjson.IsObject([]byte(text)) {
		return orderedjson.New(), nil
	}
	return orderedjson.Parse([]byte(text))
}

func trimmed(text string) string {
	start, end := 0, len(text)
	for start < end && isJSSpace(text[start]) {
		start++
	}
	for end > start && isJSSpace(text[end-1]) {
		end--
	}
	return text[start:end]
}

func isJSSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func serialize(states *orderedjson.Object) (*string, error) {
	raw, err := states.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return nil, err
	}
	text := indented.String() + "\n"
	return &text, nil
}

// serverStore is the state store of one server.
type serverStore struct {
	credentials *McpCredentials
	key         string
}

// ForServer is the state store of server name at url, for pi-mcp's sign-in and auth provider.
func (c *McpCredentials) ForServer(name, url string) (oauth.McpOAuthServerStore, error) {
	key, err := keyOf(name, url)
	if err != nil {
		return nil, err
	}
	return &serverStore{credentials: c, key: key}, nil
}

// Load is the stored state (nil: none).
func (s *serverStore) Load(ctx context.Context) (*oauth.McpOAuthState, error) {
	return p2runtime.WithLock(ctx, s.credentials.file, func(current *string) (p2runtime.LockResult[*oauth.McpOAuthState], error) {
		states, err := parseStates(current)
		if err != nil {
			return p2runtime.LockResult[*oauth.McpOAuthState]{}, err
		}
		raw, ok := states.Get(s.key)
		if !ok {
			return p2runtime.LockResult[*oauth.McpOAuthState]{}, nil
		}
		var state oauth.McpOAuthState
		if err := json.Unmarshal(raw, &state); err != nil {
			return p2runtime.LockResult[*oauth.McpOAuthState]{}, err
		}
		return p2runtime.LockResult[*oauth.McpOAuthState]{Result: &state}, nil
	})
}

// Save stores state.
func (s *serverStore) Save(ctx context.Context, state oauth.McpOAuthState) error {
	_, err := p2runtime.WithLock(ctx, s.credentials.file, func(current *string) (p2runtime.LockResult[struct{}], error) {
		states, err := parseStates(current)
		if err != nil {
			return p2runtime.LockResult[struct{}]{}, err
		}
		if err := states.SetValue(s.key, state); err != nil {
			return p2runtime.LockResult[struct{}]{}, err
		}
		next, err := serialize(states)
		return p2runtime.LockResult[struct{}]{Next: next}, err
	})
	return err
}

// WithRefreshLock runs fn after the server's previous refresh (not reentrant, as pi-mcp's lock).
func (s *serverStore) WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error {
	c := s.credentials
	c.mu.Lock()
	lock, ok := c.refreshing[s.key]
	if !ok {
		lock = make(chan struct{}, 1)
		c.refreshing[s.key] = lock
	}
	c.mu.Unlock()
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	defer func() { <-lock }()
	return fn(ctx)
}

// Remove forgets server name at url: its client, tokens and pending sign-in.
func (c *McpCredentials) Remove(ctx context.Context, name, url string) error {
	key, err := keyOf(name, url)
	if err != nil {
		return err
	}
	_, err = p2runtime.WithLock(ctx, c.file, func(current *string) (p2runtime.LockResult[struct{}], error) {
		states, err := parseStates(current)
		if err != nil {
			return p2runtime.LockResult[struct{}]{}, err
		}
		if !states.Has(key) {
			return p2runtime.LockResult[struct{}]{}, nil
		}
		states.Delete(key)
		next, err := serialize(states)
		return p2runtime.LockResult[struct{}]{Next: next}, err
	})
	return err
}

// Close waits for pending writes.
func (c *McpCredentials) Close() { c.file.Close() }
