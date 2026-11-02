package archivepath

import "testing"

func TestMember(t *testing.T) {
	for name, want := range map[string]string{"a": "a", "a/": "a", "./a/b": "a/b", "a/../b": "b"} {
		if got, err := Member(name); err != nil || got != want {
			t.Errorf("Member(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"", ".", "..", "../a", "a/../..", "/a", `a\b`} {
		if got, err := Member(name); err == nil {
			t.Errorf("Member(%q) = %q, want an error", name, got)
		}
	}
}

func TestCheckLink(t *testing.T) {
	for _, test := range []struct {
		name, target string
		ok           bool
	}{
		{"a/l", "b", true},
		{"a/l", ".", true},
		{"a/b/l", "../c/d", true},
		{"a/b/l", "../../c", true},
		{"a/b/l", "./c/./d", true},
		{"a/b/l", "../", true},
		{"a/l", "../../c", false},
		{"a/l", "/c", false},
		{"a/l", `b\c`, false},
		{"a/l", "C:foo", false},
		{"a/l", "c:", false},
		{"a/l", "b:c", false},
		{"a/l", "..", false},
		{"a/b/l", "z/../z/../..", false},
		{"a/b/l", "b/../c", false},
		{"a/b/c/l", "x/..", false},
	} {
		if err := CheckLink(test.name, test.target); (err == nil) != test.ok {
			t.Errorf("CheckLink(%q, %q) = %v, want ok=%v", test.name, test.target, err, test.ok)
		}
	}
}
