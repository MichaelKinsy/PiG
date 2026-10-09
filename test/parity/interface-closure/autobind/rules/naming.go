package rules

// Family Naming: how an upstream (TypeScript) identifier may be spelled in Go. A rule accepts a pair only when the Go name is one of
// the finite set of spellings it derives from the upstream name; it never folds case or separators blindly, so Fooid is not FooID.
//
// Registered rules: NM6 (NameRule: createX/makeX stand for NewX or CreateX), NM4 and NM5 (MemberRule: an accessor spelled as a method
// or a boolean field). Spelling (NM1-NM3) is the strict spelling function; the engine's built-in fold (N2) accepts a superset of it,
// and TestSpellingCoversHandClosedSpellingPairs shows Spelling accepts every spelling-only pair the hand-closed rows use.

import (
	"go/types"
	"slices"
	"strings"
	"unicode"
)

// Naming rule identifiers, reported with every accepted pair.
const (
	NameExact      = "NM1" // the upstream name, or the upstream name with an upper-case first letter
	NameInitialism = "NM2" // PascalCase with Go initialisms (Id -> ID, Url -> URL), also from snake_case and SCREAMING_SNAKE_CASE
	NameUnexported = "NM3" // the same spellings with a lower-case first letter (an unexported Go field or function)
	NameGetter     = "NM4" // getX() <-> X, GetX; a data property x <-> GetX()
	NameBoolean    = "NM5" // isX <-> a bool method X, or a bool field X for a data property (a member rule; IsX is NM1/NM2)
	NamePrefix     = "NM7" // McpClient <-> Client, CodemodeCall <-> Call, TuiStopOptions <-> StopOptions, KnownApi <-> API
	NameFactory    = "NM6" // createX / makeX <-> NewX, CreateX
)

// initialisms are the words whose Go spelling is all upper case (Go code review comments, plus the abbreviations Pi uses in names).
var initialisms = map[string]string{
	"id": "ID", "ids": "IDs", "url": "URL", "urls": "URLs", "uri": "URI", "uris": "URIs", "json": "JSON", "http": "HTTP", "https": "HTTPS",
	"api": "API", "apis": "APIs", "rpc": "RPC", "html": "HTML", "css": "CSS", "sql": "SQL", "ui": "UI", "ai": "AI", "gif": "GIF", "ip": "IP",
	"tls": "TLS", "ssh": "SSH", "sse": "SSE", "uuid": "UUID", "xml": "XML", "cpu": "CPU", "tcp": "TCP", "udp": "UDP", "utf": "UTF",
	"eof": "EOF", "ascii": "ASCII", "oauth": "OAuth", "jwt": "JWT", "pkce": "PKCE", "ansi": "ANSI", "osc": "OSC", "csi": "CSI", "png": "PNG",
	"jpeg": "JPEG", "jpg": "JPG", "mime": "MIME", "ttl": "TTL", "dns": "DNS", "ram": "RAM", "os": "OS", "io": "IO", "tui": "TUI", "cli": "CLI",
	"sdk": "SDK", "llm": "LLM", "mcp": "MCP", "rgb": "RGB", "sha": "SHA", "md": "MD",
	"www": "WWW", "pid": "PID", "cwd": "CWD", "ms": "MS", "jsonrpc": "JSONRPC",
	// Words Go spells with an inner capital (compound words and brand names).
	"websocket": "WebSocket", "etag": "ETag", "webp": "WebP", "openai": "OpenAI", "uuidv7": "UUIDv7",
	// Provider brand names the catalog constants carry (DEEPSEEK_MODELS is DeepSeekModels).
	"deepseek": "DeepSeek", "vllm": "VLLM", "github": "GitHub", "huggingface": "HuggingFace", "minimax": "MiniMax", "moonshotai": "MoonshotAI",
	"opencode": "OpenCode", "openrouter": "OpenRouter", "cn": "CN",
}

// Words splits an upstream identifier into words. It splits snake_case, kebab-case and camelCase, and keeps a run of capitals together
// until the next capital that starts a lower-case run (JSONRPCMessage is JSONRPC, Message).
func Words(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '$':
			flush()
		case unicode.IsUpper(r):
			if len(cur) > 0 {
				prev := cur[len(cur)-1]
				nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					flush()
				}
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

func title(w string) string {
	rs := []rune(strings.ToLower(w))
	if len(rs) > 0 {
		rs[0] = unicode.ToUpper(rs[0])
	}
	return string(rs)
}

// spellings returns every exported Go spelling of the words: each initialism word is either title case or its initialism form.
// Upstream words that are already upper case (a SCREAMING_SNAKE word or a capital run) keep one spelling per rule.
func spellings(words []string) []string {
	out := []string{""}
	for _, w := range words {
		forms := []string{title(w)}
		if init, ok := initialisms[strings.ToLower(w)]; ok && !slices.Contains(forms, init) {
			forms = append(forms, init)
		}
		if w == strings.ToUpper(w) && len(w) > 1 && !slices.Contains(forms, w) && !strings.ContainsAny(w, "0123456789") {
			// A capital run such as JSONRPC stays itself as one Go word.
			forms = append(forms, w)
		}
		var next []string
		for _, prefix := range out {
			for _, f := range forms {
				next = append(next, prefix+f)
			}
		}
		out = next
	}
	return out
}

func lowerFirst(s string) string {
	rs := []rune(s)
	if len(rs) > 0 {
		rs[0] = unicode.ToLower(rs[0])
	}
	return string(rs)
}

func upperFirst(s string) string {
	rs := []rune(s)
	if len(rs) > 0 {
		rs[0] = unicode.ToUpper(rs[0])
	}
	return string(rs)
}

// Spelling reports under which rule goName spells the plain upstream identifier, or "" when it does not.
func Spelling(upstream, goName string) string {
	if upstream == "" || goName == "" {
		return ""
	}
	if goName == upstream || goName == upperFirst(upstream) {
		return NameExact
	}
	words := Words(upstream)
	if len(words) == 0 {
		return ""
	}
	cands := spellings(words)
	if slices.Contains(cands, goName) {
		return NameInitialism
	}
	for _, c := range cands {
		if lowerFirst(c) == goName {
			return NameUnexported
		}
	}
	return ""
}

// stripAccessor returns the rest of name after prefix when the next letter starts a new word.
func stripAccessor(name, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || rest == "" || !unicode.IsUpper([]rune(rest)[0]) {
		return "", false
	}
	return rest, true
}

// factoryName is NM7: createX and makeX are functions that build an X, spelled NewX or CreateX in Go.
type factoryName struct{}

func (factoryName) Name() string { return NameFactory }

func (factoryName) Match(upstream, goName string) string {
	for _, p := range []string{"create", "make"} {
		rest, ok := stripAccessor(upstream, p)
		if !ok {
			continue
		}
		if Spelling("new"+rest, goName) != "" || Spelling("create"+rest, goName) != "" {
			return NameFactory
		}
	}
	return ""
}

// productPrefixes are the upstream product prefixes whose Go package name already carries them: McpClient is mcp.Client, CodemodeSourceError is
// codemode.SourceError, TuiStopOptions is tui.StopOptions. KnownApi is a union of the known API names, Go's API (Pi types.ts KnownApi).
var productPrefixes = []string{"Mcp", "Codemode", "Tui", "Known"}

// prefixName is NM7: an upstream name that starts with a product prefix stands for the Go name of the rest, spelled by NM1-NM3. The detector still
// requires the Go symbol to live in the upstream package's directories and to agree in shape, so the rule never reaches across packages.
type prefixName struct{}

func (prefixName) Name() string { return NamePrefix }

func (prefixName) Match(upstream, goName string) string {
	for _, p := range productPrefixes {
		if rest, ok := stripAccessor(upstream, p); ok && Spelling(rest, goName) != "" {
			return NamePrefix
		}
	}
	return ""
}

// accessorMember is NM4 and NM5. It finds the one exported Go member of owner that stands for an upstream accessor:
//
//	getX    a method X or GetX (a field is not a call)
//	x       a method GetX (a data property read through a getter)
//	isX     a method returning bool, spelled X; a bool field only for a data property (a stored field is not provably the live state an isX() method computes)
//
// hasX is never a bare X (a count or a list is not a predicate), and setX is never a bare X. The rule applies only when exactly one
// member of the owner's method set and fields qualifies, so a type with both X and GetX is left to the name rules.
type accessorMember struct{}

func (accessorMember) Name() string { return NameGetter }

func (accessorMember) Member(_ Env, prop Property, owner *types.TypeName) (types.Object, bool) {
	var want []func(types.Object) bool
	if rest, ok := stripAccessor(prop.Name, "get"); ok {
		want = append(want, func(o types.Object) bool {
			_, isFunc := o.(*types.Func)
			return isFunc && (Spelling(rest, o.Name()) != "" || Spelling(prop.Name, o.Name()) != "")
		})
	} else if rest, ok := stripAccessor(prop.Name, "is"); ok {
		called := len(prop.Calls) > 0
		want = append(want, func(o types.Object) bool {
			_, isFunc := o.(*types.Func)
			return (isFunc || !called) && returnsBool(o) && Spelling(rest, o.Name()) != ""
		})
	} else if len(prop.Calls) == 0 && !strings.HasPrefix(prop.Name, "set") {
		want = append(want, func(o types.Object) bool {
			_, isFunc := o.(*types.Func)
			return isFunc && Spelling("get"+upperFirst(prop.Name), o.Name()) != ""
		})
	}
	if len(want) == 0 || owner == nil {
		return nil, false
	}
	var found []types.Object
	ms := types.NewMethodSet(types.NewPointer(owner.Type()))
	for method := range ms.Methods() {
		if o := method.Obj(); o.Exported() && want[0](o) {
			found = append(found, o)
		}
	}
	if st, ok := owner.Type().Underlying().(*types.Struct); ok {
		for f := range st.Fields() {
			if f.Exported() && want[0](f) {
				found = append(found, f)
			}
		}
	}
	if len(found) != 1 {
		return nil, false
	}
	return found[0], true
}

// returnsBool reports whether o is a bool field, or a method whose only result is bool.
func returnsBool(o types.Object) bool {
	isBool := func(t types.Type) bool {
		b, ok := t.Underlying().(*types.Basic)
		return ok && b.Kind() == types.Bool
	}
	if f, ok := o.(*types.Func); ok {
		sig := f.Type().(*types.Signature)
		return sig.Results().Len() == 1 && isBool(sig.Results().At(0).Type())
	}
	return isBool(o.Type())
}

func init() {
	RegisterName(Naming, factoryName{})
	RegisterName(Naming, prefixName{})
	RegisterMember(Naming, accessorMember{})
}
