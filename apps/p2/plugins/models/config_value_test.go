package models

import (
	"reflect"
	"testing"
)

func TestResolveConfigValue(t *testing.T) {
	t.Setenv("TEST_CONFIG_LEFT", "left")
	t.Setenv("TEST_CONFIG_RIGHT", "right")
	t.Setenv("TEST_CONFIG_EMPTY", "")
	for config, want := range map[string]string{
		"literal-key":                            "literal-key",
		"$TEST_CONFIG_LEFT":                      "left",
		"${TEST_CONFIG_LEFT}_$TEST_CONFIG_RIGHT": "left_right",
		"$$TEST_CONFIG_LEFT":                     "$TEST_CONFIG_LEFT",
		"$!literal-$TEST_CONFIG_RIGHT":           "!literal-right",
		"${not valid}":                           "${not valid}",
		"${unterminated":                         "${unterminated",
		"cost $5":                                "cost $5",
		"":                                       "",
	} {
		if got, ok := ResolveConfigValueUncached(config, nil); !ok || got != want {
			t.Errorf("%q = %q, %v; want %q", config, got, ok, want)
		}
	}
	if got, _ := ResolveConfigValueUncached("$TEST_CONFIG_LEFT", map[string]string{"TEST_CONFIG_LEFT": "scoped"}); got != "scoped" {
		t.Errorf("the credential's environment comes before the process's: %q", got)
	}
	for _, config := range []string{"$TEST_CONFIG_MISSING_12345", "$TEST_CONFIG_EMPTY", "${TEST_CONFIG_LEFT}$TEST_CONFIG_MISSING_12345"} {
		if got, ok := ResolveConfigValueUncached(config, nil); ok {
			t.Errorf("%q resolved to %q", config, got)
		}
	}
	for command, want := range map[string]string{
		"!echo '  spaced-key  '":           "spaced-key",
		"!echo 'hello world' | tr ' ' '-'": "hello-world",
		"!printf 'line1\\nline2'":          "line1\nline2",
	} {
		if got, ok := ResolveConfigValueUncached(command, nil); !ok || got != want {
			t.Errorf("%q = %q, %v; want %q", command, got, ok, want)
		}
	}
	for _, command := range []string{"!exit 1", "!nonexistent-command-12345", "!printf ''"} {
		if got, ok := ResolveConfigValueUncached(command, nil); ok {
			t.Errorf("%q resolved to %q", command, got)
		}
	}
}

func TestResolveConfigValueErrors(t *testing.T) {
	t.Setenv("TEST_CONFIG_SET", "x")
	for config, want := range map[string]string{
		"$TEST_MISSING_A":                 "Failed to resolve key from environment variable: TEST_MISSING_A",
		"$TEST_MISSING_A-$TEST_MISSING_B": "Failed to resolve key from environment variables: TEST_MISSING_A, TEST_MISSING_B",
		"$TEST_MISSING_A-$TEST_MISSING_A": "Failed to resolve key from environment variable: TEST_MISSING_A",
		"!exit 1":                         "Failed to resolve key from shell command",
	} {
		if _, err := ResolveConfigValueOrError(config, "key", nil); err == nil || err.Error() != want {
			t.Errorf("%q: %v, want %q", config, err, want)
		}
	}
	if got := GetConfigValueEnvVarNames("$A ${B} $A !$C"); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("names = %v", got)
	}
	if got := GetConfigValueEnvVarNames("!echo $A"); got != nil {
		t.Errorf("a command references none: %v", got)
	}
	headers, err := ResolveHeadersOrError([]Header{{"X-A", "a"}, {"X-B", "$TEST_CONFIG_SET"}}, "configured provider", nil)
	if err != nil || !reflect.DeepEqual(headers, map[string]string{"X-A": "a", "X-B": "x"}) {
		t.Errorf("headers = %v, %v", headers, err)
	}
	if _, err := ResolveHeadersOrError([]Header{{"X-Z", "$TEST_MISSING_A"}}, "configured provider", nil); err == nil ||
		err.Error() != `Failed to resolve configured provider header "X-Z" from environment variable: TEST_MISSING_A` {
		t.Errorf("header error: %v", err)
	}
	if headers, err := ResolveHeadersOrError(nil, "x", nil); headers != nil || err != nil {
		t.Errorf("no headers: %v, %v", headers, err)
	}
}
