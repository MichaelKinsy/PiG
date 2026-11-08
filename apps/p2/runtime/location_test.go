package runtime

import (
	"reflect"
	"testing"
)

func str(s string) *string { return &s }

// Ports the cases of "parseLocation reads [env:]path as the footer writes it" (apps/p2/test/p2/tui-location.test.ts).
func TestParseLocationReadsEnvPathAsTheFooterWritesIt(t *testing.T) {
	envs := []string{"gpu", "local2"}
	type want struct {
		input LocationInput
		err   string
	}
	for _, c := range []struct {
		text     string
		fallback LocationFallback
		want     want
	}{
		{"gpu:/data", LocationFallback{}, want{input: LocationInput{Env: str("gpu"), Path: "/data"}}},
		{"gpu:", LocationFallback{}, want{input: LocationInput{Env: str("gpu"), Path: "~"}}},
		{"/tmp/x", LocationFallback{}, want{input: LocationInput{Path: "/tmp/x"}}},
		// /cd: no prefix stays in the session's environment, relative to its directory.
		{"../b/./c", LocationFallback{Env: str("gpu"), Cwd: "/srv/a"}, want{input: LocationInput{Env: str("gpu"), Path: "/srv/b/c"}}},
		{"local:~/p", LocationFallback{Env: str("gpu"), Cwd: "/srv"}, want{input: LocationInput{Path: "~/p"}}},
		// Another environment, or /new: relative to the home directory.
		{"gpu:rel", LocationFallback{Cwd: "/work"}, want{input: LocationInput{Env: str("gpu"), Path: "~/rel"}}},
		{"rel", LocationFallback{}, want{input: LocationInput{Path: "~/rel"}}},
		// One letter is a Windows drive; other unknown names are refused.
		{"c:/x", LocationFallback{}, want{input: LocationInput{Path: "c:/x"}}},
		{"tpu:/x", LocationFallback{}, want{err: `Unknown environment "tpu" (/envs lists them)`}},
	} {
		got, err := ParseLocation(c.text, envs, c.fallback)
		if c.want.err != "" {
			if err == nil || err.Error() != c.want.err {
				t.Errorf("ParseLocation(%q) error = %v, want %q", c.text, err, c.want.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want.input) {
			t.Errorf("ParseLocation(%q) = %#v, %v; want %#v", c.text, got, err, c.want.input)
		}
	}
	if got := JoinPath(`C:\work\a`, `..\b`); got != `C:\work\b` {
		t.Errorf("JoinPath windows = %q", got)
	}
	if got := JoinPath("/", ".."); got != "/" {
		t.Errorf("JoinPath root = %q", got)
	}
}

func TestLocationHelpers(t *testing.T) {
	if !SameLocation(Location{Cwd: "/a", Env: str("x")}, Location{Cwd: "/a", Env: str("x")}) ||
		SameLocation(Location{Cwd: "/a"}, Location{Cwd: "/a", Env: str("x")}) {
		t.Error("SameLocation")
	}
	if got := LocationText(Location{Cwd: "/p", Env: str("gpu")}); got != "gpu:/p" {
		t.Errorf("remote text = %q", got)
	}
	if got := LocationText(Location{Cwd: "/p"}); got != "/p" {
		t.Errorf("local text = %q", got)
	}
	if got := LocationText(Location{Cwd: "/p", Env: str("")}); got != "/p" {
		t.Errorf("empty env text = %q (a falsy env is this machine)", got)
	}
	for _, rooted := range []string{"~", "~/x", "/x", `C:\x`, "c:/x", `\\host\share`} {
		if !IsRooted(rooted) {
			t.Errorf("IsRooted(%q) = false", rooted)
		}
	}
	for _, relative := range []string{"x", "~x", "./x", "C:x"} {
		if IsRooted(relative) {
			t.Errorf("IsRooted(%q) = true", relative)
		}
	}
}
