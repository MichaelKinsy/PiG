package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
)

// Host calls for Pi's pure helpers that need the host's parsers: parseFrontmatter (the host's YAML parser, the one that reads skills and prompt templates)
// and Date.parse (the host's port of V8's). They read no session state, so the host serves them before an extension registers, and a helper called while
// the factory runs (before Run) dials the host itself: the first call opens the connection and Run later reuses it. A call from a handler uses the
// handler's own connection (Context.ParseFrontmatter, Context.ParseDate).
const (
	callParseFrontmatter = "frontmatter.parse"
	callParseDate        = "date.parse"
)

// ParsedFrontmatter is the result of ParseFrontmatter: Pi's `{ frontmatter, body }`. Frontmatter is the JSON of `parse(yaml) ?? {}`, with each object's keys
// in the order a JavaScript object enumerates them (array-index keys first, ascending, then insertion order), which a Go map would lose; it is a JSON object
// for the usual mapping header and the array, string, number or boolean for any other YAML document.
type ParsedFrontmatter struct {
	Frontmatter json.RawMessage
	Body        string
}

// HostHelpers are the host's parsers for a fused extension, which runs in the host's own process and has no connection before Run. The code that builds
// the fused binary installs them with SetHostHelpers before it runs any factory.
type HostHelpers struct {
	ParseFrontmatter func(content string) (ParsedFrontmatter, error)
	// ParseDate is Date.parse: the time value in milliseconds, false for NaN.
	ParseDate func(text string) (float64, bool)
}

var helpers struct {
	mu   sync.Mutex
	hook *HostHelpers
	// dialled holds the connections helpers opened before Run, by socket path; Run takes its own.
	dialled map[string]*conn
	// running are the connections of the extensions that are running (a packed cell runs several), with the socket path each serves (empty for one that has none), for a helper called from
	// code that has no Context and no socket of its own: the first that is still running answers.
	running []runningConn
}

// SetHostHelpers installs the host's parsers for a fused extension; a nil value removes them.
func SetHostHelpers(h *HostHelpers) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.hook = h
}

// ParseFrontmatter is Pi's parseFrontmatter (utils/frontmatter.ts, exported from index.ts), answered by the host's parser. A header the YAML parser
// refuses is the error Pi throws. It may be called before Run: the first call connects to the host, and Run reuses the connection.
func ParseFrontmatter(content string) (ParsedFrontmatter, error) {
	if hook := installedHelpers(); hook != nil && hook.ParseFrontmatter != nil {
		return hook.ParseFrontmatter(content)
	}
	c, err := helperConnection()
	if err != nil {
		return ParsedFrontmatter{}, err
	}
	result, err := c.call(callParseFrontmatter, map[string]string{"content": content})
	return decodeFrontmatter(result, err)
}

// StripFrontmatter is Pi's stripFrontmatter: the body of ParseFrontmatter.
func StripFrontmatter(content string) (string, error) {
	parsed, err := ParseFrontmatter(content)
	return parsed.Body, err
}

// ParseDate is `Date.parse(text)` (`new Date(text).getTime()` for a string), V8's parser answered by the host: the time value in milliseconds, and false when it
// is NaN. It may be called before Run, like ParseFrontmatter.
func ParseDate(text string) (float64, bool, error) {
	if hook := installedHelpers(); hook != nil && hook.ParseDate != nil {
		ms, ok := hook.ParseDate(text)
		return ms, ok, nil
	}
	c, err := helperConnection()
	if err != nil {
		return 0, false, err
	}
	result, err := c.call(callParseDate, map[string]string{"text": text})
	return decodeDate(result, err)
}

// ParseFrontmatter is ParseFrontmatter over the handler's own connection.
func (c Context) ParseFrontmatter(content string) (ParsedFrontmatter, error) {
	result, err := c.callHost(callParseFrontmatter, map[string]string{"content": content})
	return decodeFrontmatter(result, err)
}

// ParseDate is ParseDate over the handler's own connection.
func (c Context) ParseDate(text string) (float64, bool, error) {
	result, err := c.callHost(callParseDate, map[string]string{"text": text})
	return decodeDate(result, err)
}

func decodeFrontmatter(result *callResultMsg, err error) (ParsedFrontmatter, error) {
	if err != nil {
		return ParsedFrontmatter{}, err
	}
	var reply struct {
		Frontmatter json.RawMessage `json:"frontmatter"`
		Body        string          `json:"body"`
	}
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return ParsedFrontmatter{}, fmt.Errorf("decode parseFrontmatter: %w", err)
	}
	return ParsedFrontmatter{Frontmatter: reply.Frontmatter, Body: reply.Body}, nil
}

func decodeDate(result *callResultMsg, err error) (float64, bool, error) {
	if err != nil {
		return 0, false, err
	}
	var reply struct {
		Ms *float64 `json:"ms"`
	}
	if err := json.Unmarshal(result.Result, &reply); err != nil {
		return 0, false, fmt.Errorf("decode Date.parse: %w", err)
	}
	if reply.Ms == nil {
		return 0, false, nil
	}
	return *reply.Ms, true, nil
}

func installedHelpers() *HostHelpers {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	return helpers.hook
}

// runningConn is the connection of a running extension and the socket it serves.
type runningConn struct {
	c    *conn
	path string
}

// helperConnection is the connection a helper called outside a handler uses: the running extension's, the one an earlier helper opened to this socket, or a
// new one, which Run reuses. A runner that names a socket (PIG_EXT_SOCKET, set to the member's own while its factory runs) means the extension that socket
// belongs to, so a helper called by one member's factory never rides another member's connection, which the host serves only after it has accepted this
// member's: it uses the running extension on that socket, and dials the socket when this member has not connected. With no socket named, the first extension
// that is still running answers.
func helperConnection() (*conn, error) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	path := helperSocketPath()
	for _, running := range helpers.running {
		if path == "" || running.path == "" || running.path == path {
			return running.c, nil
		}
	}
	if c := helpers.dialled[path]; c != nil {
		return c, nil
	}
	if path == "" {
		return nil, errors.New("not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig")
	}
	nc, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("connect to host: %w", err)
	}
	c := newConn(nc)
	c.start()
	if helpers.dialled == nil {
		helpers.dialled = map[string]*conn{}
	}
	helpers.dialled[path] = c
	return c, nil
}

// helperSocketPath is the socket a helper dials: PIG_EXT_SOCKET, which a packed cell's runner sets to the member's socket while the member's factory runs.
func helperSocketPath() string { return os.Getenv("PIG_EXT_SOCKET") }

// takeHelperConn hands Run the connection a helper opened to sockPath, once.
func takeHelperConn(sockPath string) *conn {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	c := helpers.dialled[sockPath]
	delete(helpers.dialled, sockPath)
	return c
}

// adoptHelperConn makes the connection of a running extension the one helpers use when they run outside a factory and a Context, so such a helper does not
// dial a socket the host already accepted.
func adoptHelperConn(c *conn, path string) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.running = append(helpers.running, runningConn{c: c, path: path})
}

// releaseHelperConn forgets c when its extension stopped running.
func releaseHelperConn(c *conn) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.running = slices.DeleteFunc(helpers.running, func(running runningConn) bool { return running.c == c })
}
