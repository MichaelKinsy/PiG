package mcpext

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// A tokensExpireAt of 1e999 or null is left out: JSON.stringify writes null for Infinity, and PiG's typed state reads null as 0 and cannot decode 1e999.
// McpOAuthCredentialStore (extensions/mcp/oauth.js) reads, promotes, removes and rewrites mcp-auth.json. Each probe runs load, tokens, remove and save over one file text, on Pi's store and PiG's, and compares what the caller sees and the text left behind.
func TestMcpOAuthStoreStatesMatchPiOracle(t *testing.T) {
	const name, serverURL = "my-server", "https://mcp.example.com/mcp"
	key := "mcp__my_server|" + serverURL
	legacy := serverURL
	r := rand.New(rand.NewPCG(5, 8))
	tokens := `{"access_token":"a","token_type":"Bearer","expires_in":3600}`
	values := []string{`{"serverUrl":"` + serverURL + `","tokens":` + tokens + `}`, `{"serverUrl":"` + serverURL + `"}`, `{}`, `null`, `0`, `""`, `false`, `true`, `[]`, `5`, `"s"`, `{"tokens":null}`,
		`{"serverUrl":"` + serverURL + `","tokens":` + tokens + `,"tokensExpireAt":1700000000000}`, `{"serverUrl":"é\u2028<>&\ud800","codeVerifier":"v"}`, `{"serverUrl":"` + serverURL + `","tokensExpireAt":1.5e21}`}
	extras := []string{`"other":{"a":1}`, `"2":1`, `"1":{"b":[1,2]}`, `"__proto__":{"x":1}`, `"mcp__other|https://o/":{"serverUrl":"https://o/"}`, `"é":"\u00e9"`, `"k":1e400`, `"z":"\ud800"`, `"n":-0`, `"n2":1.0`}
	wraps := []string{"", " ", "\ufeff", "\u0085", "\n\n", "\u2028", "\u00a0 \t", "  {}  ", "\ufeff{}", "{}\u0085", "{", "[1,2]", "null", "5", `"x"`, "true", "{} {}", "{,}", `{"a":1,}`}
	var files []string
	files = append(files, wraps...)
	for range 600 {
		var members []string
		if r.IntN(4) > 0 {
			members = append(members, fmt.Sprintf("%q:%s", key, values[r.IntN(len(values))]))
		}
		if r.IntN(3) > 0 {
			members = append(members, fmt.Sprintf("%q:%s", legacy, values[r.IntN(len(values))]))
		}
		for range r.IntN(3) {
			members = append(members, extras[r.IntN(len(extras))])
		}
		r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
		text := "{" + strings.Join(members, ",") + "}"
		if r.IntN(5) == 0 {
			text = "{\n  " + strings.Join(members, ",\n  ") + "\n}\n"
		}
		files = append(files, text)
	}
	type probe struct {
		File string `json:"file"`
	}
	type outcome struct {
		Error   bool            `json:"error"`
		Result  json.RawMessage `json:"result"`
		Content string          `json:"content"`
	}
	type result struct {
		Load, Tokens, Remove, Save outcome
	}
	probes := make([]probe, len(files))
	for i, f := range files {
		probes[i] = probe{f}
	}
	var want []result
	pioracle.Run(t, `
const { McpOAuthCredentialStore } = await load("pi-coding-agent/extensions/mcp/oauth.js");
const run = async (file, op) => {
	let text = file;
	let exists = true;
	const backend = { withLock: (fn) => { const r = fn(exists ? text : undefined); if (r.next !== undefined) { text = r.next; exists = true; } return r.result; } };
	const store = new McpOAuthCredentialStore(backend);
	const server = store.forServer(input_name, input_url);
	let outcome = { error: false, result: null };
	try {
		const value = await op(store, server);
		outcome.result = value === undefined ? "undefined" : JSON.parse(JSON.stringify(value));
	} catch {
		outcome.error = true;
	}
	outcome.content = text;
	return outcome;
};
const input_name = "my-server", input_url = "https://mcp.example.com/mcp";
emit(await Promise.all(input.map(async ({ file }) => ({
	Load: await run(file, (store, server) => server.load()),
	Tokens: await run(file, (store) => store.tokens(input_name, input_url)),
	Remove: await run(file, (store) => store.remove(input_name, input_url)),
	Save: await run(file, (store, server) => server.save({ serverUrl: input_url, tokens: { access_token: "t", token_type: "Bearer" }, codeVerifier: "v\u2028" })),
}))));
`, probes, &want)
	run := func(file string, op func(*McpOAuthCredentialStore, McpOAuthServerStore) (any, error)) outcome {
		backend := &InMemoryAuthStorageBackend{}
		_ = backend.WithLock(func(string, bool) (*string, error) { return &file, nil })
		store := NewMcpOAuthCredentialStoreWithBackend(backend, "")
		server, err := store.ForServer(name, serverURL)
		if err != nil {
			t.Fatal(err)
		}
		out := outcome{Result: json.RawMessage("null")}
		value, err := op(store, server)
		switch {
		case err != nil:
			out.Error = true
		case value == nil:
			out.Result = json.RawMessage(`"undefined"`)
		default:
			raw, merr := json.Marshal(value)
			if merr != nil {
				t.Fatal(merr)
			}
			out.Result = raw
		}
		_ = backend.WithLock(func(current string, _ bool) (*string, error) { out.Content = current; return nil, nil })
		return out
	}
	bad := 0
	for i, file := range files {
		got := result{
			Load: run(file, func(_ *McpOAuthCredentialStore, s McpOAuthServerStore) (any, error) {
				state, err := s.Load(context.Background())
				if state == nil {
					return nil, err
				}
				return state, err
			}),
			Tokens: run(file, func(c *McpOAuthCredentialStore, _ McpOAuthServerStore) (any, error) {
				if tk := c.Tokens(name, serverURL); tk != nil {
					return tk, nil
				}
				return nil, nil
			}),
			Remove: run(file, func(c *McpOAuthCredentialStore, _ McpOAuthServerStore) (any, error) { return c.Remove(name, serverURL) }),
			Save: run(file, func(_ *McpOAuthCredentialStore, s McpOAuthServerStore) (any, error) {
				return nil, s.Save(context.Background(), oauth.McpOAuthState{ServerURL: serverURL, Tokens: &oauth.OAuthTokens{AccessToken: "t", TokenType: "Bearer"}, CodeVerifier: "v\u2028"})
			}),
		}
		w := want[i]
		for _, c := range []struct {
			op        string
			got, want outcome
		}{{"load", got.Load, w.Load}, {"tokens", got.Tokens, w.Tokens}, {"remove", got.Remove, w.Remove}, {"save", got.Save, w.Save}} {
			if c.op == "tokens" && c.want.Error {
				// tokens() throws on an unreadable file; PiG's Tokens has no error result and reports no tokens.
				c.want = outcome{Result: json.RawMessage(`"undefined"`), Content: c.want.Content}
			}
			if c.op == "load" && c.got.Error && !c.want.Error && !strings.HasPrefix(string(c.want.Result), "{") {
				// Pi hands a stored non-object (5, [], "s", 0) to its caller as the state; PiG's typed state cannot hold it and reports an error.
				continue
			}
			if c.got.Error != c.want.Error || c.got.Content != c.want.Content || (!c.got.Error && !equalJSON(c.got.Result, c.want.Result)) {
				if bad++; bad <= 8 {
					t.Errorf("%s over %q:\n  PiG %+v\n  Pi  %+v", c.op, file, c.got, c.want)
				}
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d outcomes differ in total", bad)
	}
}

// equalJSON compares what the callers see. PiG's state is a typed struct: it has no absent serverUrl and no explicit null tokens, and Tokens has no null result.
func equalJSON(a, b json.RawMessage) bool {
	normalize := func(raw json.RawMessage) string {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return string(raw)
		}
		switch v := value.(type) {
		case nil:
			return `"undefined"`
		case map[string]any:
			if v["serverUrl"] == "" {
				delete(v, "serverUrl")
			}
			if tokens, ok := v["tokens"]; ok && tokens == nil {
				delete(v, "tokens")
			}
		}
		out, _ := json.Marshal(value)
		return string(out)
	}
	return normalize(a) == normalize(b)
}
