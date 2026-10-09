package rules

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func TestWords(t *testing.T) {
	for in, want := range map[string]string{
		"entryId":                  "entry Id",
		"JsonRpcRequest":           "Json Rpc Request",
		"JSONRPCMessage":           "JSONRPC Message",
		"ANTHROPIC_API_KEY_ENV":    "ANTHROPIC API KEY ENV",
		"response_types_supported": "response types supported",
		"getGifDimensions":         "get Gif Dimensions",
		"x":                        "x",
		"base64Url":                "base64 Url",
	} {
		if got := strings.Join(Words(in), " "); got != want {
			t.Errorf("Words(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSpelling(t *testing.T) {
	for _, tc := range []struct{ up, goName, rule string }{
		{"addChild", "AddChild", NameExact},
		{"AgentState", "AgentState", NameExact},
		{"entryId", "EntryID", NameInitialism},
		{"entryId", "EntryId", NameExact + "|" + NameInitialism},
		{"openUrl", "OpenURL", NameInitialism},
		{"parseJsonRpcMessage", "ParseJSONRPCMessage", NameInitialism},
		{"redirect_uris", "RedirectURIs", NameInitialism},
		{"expires_in", "ExpiresIn", NameInitialism},
		{"DEFAULT_THINKING_BUDGETS", "DefaultThinkingBudgets", NameInitialism},
		{"CLOUDFLARE_AI_GATEWAY_COMPAT_BASE_URL", "CloudflareAIGatewayCompatBaseURL", NameInitialism},
		{"ANTHROPIC_API_KEY_ENV", "AnthropicAPIKeyEnv", NameInitialism},
		{"getGifDimensions", "GetGIFDimensions", NameInitialism},
		{"modelId", "modelID", NameUnexported},
		{"websocketFailures", "WebSocketFailures", NameInitialism},
		{"etag", "ETag", NameInitialism},
		{"pollAfterMs", "PollAfterMS", NameInitialism},
		{"OPENAI_PROMPT_CACHE_KEY_MAX_LENGTH", "openAIPromptCacheKeyMaxLength", NameUnexported},
		{"uri", "URI", NameInitialism},
		// A blind case and separator fold would accept these; the rule does not.
		{"entryId", "Entryid", ""},
		{"entryId", "ENTRYID", ""},
		{"fooBar", "Foo_Bar", ""},
		{"timeoutMs", "Timeout", ""},
		{"", "X", ""},
		{"x", "", ""},
	} {
		got := Spelling(tc.up, tc.goName)
		if want := tc.rule; got != want && !strings.Contains("|"+want+"|", "|"+got+"|") {
			t.Errorf("Spelling(%q, %q) = %q, want %q", tc.up, tc.goName, got, want)
		}
	}
}

func checkSrc(t *testing.T, src string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{}).Check("x", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestFactoryName(t *testing.T) {
	r := factoryName{}
	for _, tc := range []struct{ up, goName, want string }{
		{"createInMemoryTransportPair", "NewInMemoryTransportPair", NameFactory},
		{"createModelRuntime", "CreateModelRuntime", NameFactory},
		{"makeUrlParser", "NewURLParser", NameFactory},
		{"createFoo", "NewBar", ""},   // different words
		{"createFoo", "Foo", ""},      // a bare name is not a factory spelling
		{"create", "New", ""},         // no word after the prefix
		{"creatEfoo", "NewFoo", ""},   // "create" must end a word
		{"recreateFoo", "NewFoo", ""}, // prefix only at the start
		{"Foo", "NewFoo", ""},         // a class constructor is the engine's N3, not this rule
	} {
		if got := r.Match(tc.up, tc.goName); got != tc.want {
			t.Errorf("Match(%q,%q) = %q, want %q", tc.up, tc.goName, got, tc.want)
		}
	}
}

func TestPrefixName(t *testing.T) {
	r := prefixName{}
	for _, tc := range []struct{ up, goName, want string }{
		{"McpTransport", "Transport", NamePrefix},
		{"CodemodeSandboxOptions", "SandboxOptions", NamePrefix},
		{"TuiStopOptions", "StopOptions", NamePrefix},
		{"KnownApi", "API", NamePrefix},
		{"KnownClassifierApi", "ClassifierAPI", NamePrefix},
		{"McpOAuthProvider", "OAuthProvider", NamePrefix},
		{"McpTransport", "Transp", ""},       // different words
		{"McpTransport", "McpTransport", ""}, // the name rules already decide the identical name
		{"Mcp", "", ""},                      // nothing after the prefix
		{"McpClient", "", ""},
		{"Mcpish", "ish", ""}, // the prefix must end a word
		{"Tuition", "ition", ""},
		{"Unknown", "Unknown", ""},  // the prefix is only at the start
		{"AgentState", "State", ""}, // Agent is not a product prefix: the package is not unique to it
	} {
		if got := r.Match(tc.up, tc.goName); got != tc.want {
			t.Errorf("Match(%q,%q) = %q, want %q", tc.up, tc.goName, got, tc.want)
		}
	}
}

func TestAccessorMember(t *testing.T) {
	pkg := checkSrc(t, `package x
type Embedded struct{}
func (Embedded) Theme() string { return "" }
type Owner struct {
	Embedded
	Streaming bool
	Count     int
	pending   bool
}
func (*Owner) Usage() int { return 0 }
func (*Owner) GetName() string { return "" }
func (*Owner) Compacting() bool { return false }
func (*Owner) Busy() int { return 0 }
type Both struct{}
func (Both) Size() int { return 0 }
func (Both) GetSize() int { return 0 }
`)
	owner := pkg.Scope().Lookup("Owner").(*types.TypeName)
	both := pkg.Scope().Lookup("Both").(*types.TypeName)
	r := accessorMember{}
	for _, tc := range []struct {
		name  string
		prop  Property
		owner *types.TypeName
		want  string // "" = silent
	}{
		{"getX is method X", Property{Name: "getUsage", Calls: []CallShape{{}}}, owner, "Usage"},
		{"getX is method GetX", Property{Name: "getName", Calls: []CallShape{{}}}, owner, "GetName"},
		{"getX through an embedded type", Property{Name: "getTheme", Calls: []CallShape{{}}}, owner, "Theme"},
		{"getX is never a field", Property{Name: "getCount", Calls: []CallShape{{}}}, owner, ""},
		{"data property x is method GetX", Property{Name: "name"}, owner, "GetName"},
		{"data property isX is a bool field", Property{Name: "isStreaming"}, owner, "Streaming"},
		{"method isX() is never a field", Property{Name: "isStreaming", Calls: []CallShape{{}}}, owner, ""},
		{"isX is a bool method", Property{Name: "isCompacting", Calls: []CallShape{{}}}, owner, "Compacting"},
		{"isX refuses a non-bool field", Property{Name: "isCount"}, owner, ""},
		{"isX refuses a non-bool method", Property{Name: "isBusy", Calls: []CallShape{{}}}, owner, ""},
		{"isX never reaches an unexported field", Property{Name: "isPending"}, owner, ""},
		{"hasX is never a bare X", Property{Name: "hasUsage", Calls: []CallShape{{}}}, owner, ""},
		{"setX is never a bare X", Property{Name: "setUsage", Calls: []CallShape{{}}}, owner, ""},
		{"a method property x is not GetX", Property{Name: "name", Calls: []CallShape{{}}}, owner, ""},
		{"ambiguous X and GetX stay undecided", Property{Name: "getSize", Calls: []CallShape{{}}}, both, ""},
		{"no candidate", Property{Name: "getMissing", Calls: []CallShape{{}}}, owner, ""},
		{"nil owner", Property{Name: "getUsage"}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj, ok := r.Member(nil, tc.prop, tc.owner)
			if tc.want == "" {
				if ok || obj != nil {
					t.Fatalf("expected silence, got %v", obj)
				}
				return
			}
			if !ok || obj.Name() != tc.want {
				t.Fatalf("got %v (ok=%v), want %s", obj, ok, tc.want)
			}
		})
	}
}

func TestNamingRulesAreRegistered(t *testing.T) {
	got := strings.Join(Registered(), " ")
	for _, want := range []string{"naming/NM4", "naming/NM6", "naming/NM7"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s not registered in %s", want, got)
		}
	}
}

// TestSpellingCoversHandClosedSpellingPairs is trust check 1 for this family: for every hand-closed (ported) row whose upstream and Go member names differ only
// by spelling, the rule must accept the pair. A pair the rule rejects must be a rename (different words), which the test lists by shape so a new
// failure is a rule gap and not noise.
func TestSpellingCoversHandClosedSpellingPairs(t *testing.T) {
	path := filepath.Join("..", "..", "..", "interfaces", "mapping-v"+pigversion.UpstreamVersion+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("mapping not available: %v", err)
	}
	var doc struct {
		Mappings []struct {
			ID          string   `json:"id"`
			Disposition string   `json:"disposition"`
			PigTargets  []string `json:"pigTargets"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	prop := regexp.MustCompile(`::property:([^:]+)(::call:\d+)?$`)
	norm := func(s string) string {
		return strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(s, ""))
	}
	seen := map[[2]string]bool{}
	var spelled, rejected int
	var bad []string
	for _, r := range doc.Mappings {
		if r.Disposition != "ported" || len(r.PigTargets) == 0 {
			continue
		}
		up := r.ID[strings.LastIndex(r.ID, "#")+1:]
		if m := prop.FindStringSubmatch(r.ID); m != nil {
			up = m[1]
		} else if i := strings.Index(up, "::"); i >= 0 {
			up = up[:i]
		}
		tgt := r.PigTargets[0]
		goName := tgt[strings.LastIndex(tgt, "#")+1:]
		if i := strings.LastIndex(goName, "."); i >= 0 {
			goName = goName[i+1:]
		}
		key := [2]string{up, goName}
		if seen[key] {
			continue
		}
		seen[key] = true
		if norm(up) != norm(goName) {
			continue // a rename of words, not a spelling: not this family's decision
		}
		spelled++
		if Spelling(up, goName) == "" {
			rejected++
			bad = append(bad, up+" -> "+goName)
		}
	}
	if spelled < 100 {
		t.Fatalf("only %d spelling-only hand-closed pairs found; the mapping changed shape", spelled)
	}
	t.Logf("spelling-only pairs: %d, rejected by the rule: %d %v", spelled, rejected, bad)
	if rejected != 0 {
		t.Errorf("rule rejects %d hand-closed spelling pairs: %v\nA new provider or product brand needs its word (for example \"deepseek\": \"DeepSeek\") in the initialisms table of naming.go; the rule never folds case blindly", rejected, bad)
	}
}
