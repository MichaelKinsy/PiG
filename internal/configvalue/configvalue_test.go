package configvalue

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestResolveBareNameIsLiteral(t *testing.T) {
	// v0.78.1: a bare identifier is a literal even when an env var of that
	// name is set. Env references require an explicit "$" sigil.
	ClearCache()
	t.Setenv("PIG_TEST_VAR", "abc-123")
	if got := Resolve("PIG_TEST_VAR", nil); got != "PIG_TEST_VAR" {
		t.Fatalf("bare name should be literal, got %q", got)
	}
	if got := Resolve("PIG_TEST_DEFINITELY_UNSET", nil); got != "PIG_TEST_DEFINITELY_UNSET" {
		t.Fatalf("unset bare name should be literal, got %q", got)
	}
}

func TestResolveDollarEnvExpands(t *testing.T) {
	ClearCache()
	t.Setenv("PIG_TEST_VAR", "abc-123")
	for _, in := range []string{"$PIG_TEST_VAR", "${PIG_TEST_VAR}"} {
		if got := Resolve(in, nil); got != "abc-123" {
			t.Fatalf("Resolve(%q, nil) = %q, want abc-123", in, got)
		}
	}
}

func TestResolveDollarEnvUnsetIsEmpty(t *testing.T) {
	// A template with a missing env var resolves to undefined → "".
	ClearCache()
	if got := Resolve("$PIG_TEST_DEFINITELY_UNSET", nil); got != "" {
		t.Fatalf("missing env should resolve to empty, got %q", got)
	}
	// Empty env value is treated as missing.
	t.Setenv("PIG_TEST_EMPTY", "")
	if got := Resolve("$PIG_TEST_EMPTY", nil); got != "" {
		t.Fatalf("empty env should resolve to empty, got %q", got)
	}
}

func TestResolveTemplateMultiPart(t *testing.T) {
	ClearCache()
	t.Setenv("PIG_T_MID", "MID")
	if got := Resolve("prefix-$PIG_T_MID-suffix", nil); got != "prefix-MID-suffix" {
		t.Fatalf("multi-part template = %q", got)
	}
	// One missing part undefines the whole template.
	if got := Resolve("prefix-$PIG_T_MISSING-suffix", nil); got != "" {
		t.Fatalf("template with missing env should be empty, got %q", got)
	}
}

func TestResolveDollarEscapes(t *testing.T) {
	ClearCache()
	t.Setenv("VAR", "x")
	cases := map[string]string{
		"$$VAR":   "$VAR", // $$ → literal $, then literal VAR
		"price$$": "price$",
		"$!cmd":   "!cmd", // $! → literal !
		"a$$b":    "a$b",
	}
	for in, want := range cases {
		if got := Resolve(in, nil); got != want {
			t.Errorf("Resolve(%q, nil) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveBraceInvalidNameIsLiteral(t *testing.T) {
	ClearCache()
	// ${...} with an invalid env-var name is kept verbatim.
	if got := Resolve("${not a name}", nil); got != "${not a name}" {
		t.Fatalf("invalid ${} should be literal, got %q", got)
	}
	// Unterminated ${ is kept as literal "$" + remainder.
	if got := Resolve("${UNCLOSED", nil); got != "${UNCLOSED" {
		t.Fatalf("unterminated ${ should be literal, got %q", got)
	}
	// Trailing bare $ is literal.
	if got := Resolve("end$", nil); got != "end$" {
		t.Fatalf("trailing $ should be literal, got %q", got)
	}
}

func TestResolveBangCmdRunsShell(t *testing.T) {
	ClearCache()
	if got := Resolve("!echo hello", nil); got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
}

func TestResolveBangCmdCached(t *testing.T) {
	ClearCache()
	var calls atomic.Int32
	restore := SetExecutorForTest(func(_ context.Context, payload string) (string, bool) {
		calls.Add(1)
		if payload != "echo cached" {
			t.Fatalf("unexpected payload %q", payload)
		}
		return "cached-result", true
	})
	defer restore()

	for i := range 3 {
		if got := Resolve("!echo cached", nil); got != "cached-result" {
			t.Fatalf("call %d: got %q", i, got)
		}
	}
	if c := calls.Load(); c != 1 {
		t.Fatalf("expected 1 shell-out, got %d", c)
	}
}

func TestResolveBangCmdEmptyReturnsEmpty(t *testing.T) {
	ClearCache()
	if got := Resolve("!true", nil); got != "" {
		t.Fatalf("expected empty string for empty !cmd output, got %q", got)
	}
}

func TestResolveOrErrorBangFailure(t *testing.T) {
	ClearCache()
	if _, err := ResolveOrError("!false", "test key", nil); err == nil {
		t.Fatalf("expected error for failing !cmd")
	}
}

func TestResolveOrErrorLiteralNeverErrors(t *testing.T) {
	ClearCache()
	v, err := ResolveOrError("literal_value", "test key", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "literal_value" {
		t.Fatalf("expected literal, got %q", v)
	}
}

func TestResolveOrErrorMissingEnvReportsName(t *testing.T) {
	ClearCache()
	_, err := ResolveOrError("$PIG_TEST_MISSING_ONE", "API key", nil)
	if err == nil || err.Error() != "failed to resolve API key from environment variable: PIG_TEST_MISSING_ONE" {
		t.Fatalf("single-missing error = %v", err)
	}
	_, err = ResolveOrError("$PIG_TEST_MISS_A-$PIG_TEST_MISS_B", "API key", nil)
	if err == nil {
		t.Fatalf("expected error for multiple missing env vars")
	}
}

func TestResolveHeadersDollarAndLiteral(t *testing.T) {
	ClearCache()
	t.Setenv("PIG_HEADER_X", "value-x")
	in := map[string]string{
		"X":   "$PIG_HEADER_X",         // resolves via env
		"Lit": "literal-value",         // literal
		"Y":   "!false",                // !cmd failure → drop
		"Z":   "$PIG_HEADER_UNSET_FOO", // missing env → "" → drop
	}
	got := ResolveHeaders(in, nil)
	if got["X"] != "value-x" {
		t.Errorf("X: got %q", got["X"])
	}
	if got["Lit"] != "literal-value" {
		t.Errorf("Lit: got %q", got["Lit"])
	}
	if _, ok := got["Y"]; ok {
		t.Errorf("Y should drop (failed !cmd)")
	}
	if _, ok := got["Z"]; ok {
		t.Errorf("Z should drop (missing env → empty)")
	}
}

func TestResolveHeadersNilOnEmpty(t *testing.T) {
	if got := ResolveHeaders(nil, nil); got != nil {
		t.Errorf("nil in → nil out, got %v", got)
	}
	if got := ResolveHeaders(map[string]string{"K": "!false"}, nil); got != nil {
		t.Errorf("all-fail in → nil out, got %v", got)
	}
}

func TestConfigValueEnvVarNameHelpers(t *testing.T) {
	ClearCache()
	if got := GetConfigValueEnvVarName("$FOO"); got != "FOO" {
		t.Errorf("GetConfigValueEnvVarName($FOO) = %q", got)
	}
	if got := GetConfigValueEnvVarName("a$FOO"); got != "" {
		t.Errorf("multi-part should have no single name, got %q", got)
	}
	if got := GetConfigValueEnvVarName("literal"); got != "" {
		t.Errorf("literal should have no env name, got %q", got)
	}
	if got := GetConfigValueEnvVarName("!cmd"); got != "" {
		t.Errorf("command should have no env name, got %q", got)
	}
	names := GetConfigValueEnvVarNames("$A-$B-$A")
	if !reflect.DeepEqual(names, []string{"A", "B"}) {
		t.Errorf("GetConfigValueEnvVarNames = %v, want [A B] (deduped)", names)
	}
}

func TestIsConfigValueConfigured(t *testing.T) {
	ClearCache()
	t.Setenv("PIG_CFG_SET", "v")
	if !IsConfigValueConfigured("literal", nil) {
		t.Error("literal should be configured")
	}
	if !IsConfigValueConfigured("$PIG_CFG_SET", nil) {
		t.Error("set env should be configured")
	}
	if IsConfigValueConfigured("$PIG_CFG_MISSING", nil) {
		t.Error("missing env should be unconfigured")
	}
}

// TestResolve_ProviderScopedEnvPrecedence verifies that provider-scoped env
// overrides take precedence over the process environment when resolving a
// `$VAR` reference, fall back to the process env when absent from the scope,
// and that nil scope preserves process-env behavior. Mirrors upstream
// resolve-config-value.ts threading env? into resolveEnvConfigValue.
func TestResolve_ProviderScopedEnvPrecedence(t *testing.T) {
	t.Setenv("PIG_CV_SCOPED", "from-process")
	t.Setenv("PIG_CV_ONLYPROC", "proc-only")

	scope := map[string]string{"PIG_CV_SCOPED": "from-scope"}

	// scoped value wins over process env
	if got := Resolve("$PIG_CV_SCOPED", scope); got != "from-scope" {
		t.Errorf("scoped env should win: got %q want %q", got, "from-scope")
	}
	// nil scope falls back to process env
	if got := Resolve("$PIG_CV_SCOPED", nil); got != "from-process" {
		t.Errorf("nil scope should use process env: got %q want %q", got, "from-process")
	}
	// var absent from scope falls back to process env
	if got := Resolve("$PIG_CV_ONLYPROC", scope); got != "proc-only" {
		t.Errorf("missing-from-scope should fall back to process env: got %q want %q", got, "proc-only")
	}
	// empty scoped value is treated as unset and falls back to process env
	if got := Resolve("$PIG_CV_SCOPED", map[string]string{"PIG_CV_SCOPED": ""}); got != "from-process" {
		t.Errorf("empty scoped value should fall back to process env: got %q want %q", got, "from-process")
	}
}

// TestIsConfigValueConfigured_ProviderScopedEnv verifies a `$VAR` is reported
// configured when satisfied only by the provider-scoped env (not process env).
func TestIsConfigValueConfigured_ProviderScopedEnv(t *testing.T) {
	const name = "$PIG_CV_SCOPE_ONLY"
	if IsConfigValueConfigured(name, nil) {
		t.Fatalf("precondition: %s must be unset in the process env", name)
	}
	if !IsConfigValueConfigured(name, map[string]string{"PIG_CV_SCOPE_ONLY": "x"}) {
		t.Error("scoped env should satisfy IsConfigValueConfigured")
	}
	if got := GetMissingConfigValueEnvVarNames(name, map[string]string{"PIG_CV_SCOPE_ONLY": "x"}); len(got) != 0 {
		t.Errorf("scoped env should leave no missing vars, got %v", got)
	}
}
