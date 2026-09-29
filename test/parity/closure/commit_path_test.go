package closure

import (
	"strings"
	"testing"
)

func TestPrefixCandidatesWalksOutwardFromCurrentLayout(t *testing.T) {
	for _, test := range []struct {
		name   string
		prefix string
		want   []string
	}{
		{"nested", "workspace/source/", []string{"workspace/source/", "source/", ""}},
		{"single", "source/", []string{"source/", ""}},
		{"root", "", []string{""}},
		{"unslashed", "workspace/source", []string{"workspace/source/", "source/", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := prefixCandidates(test.prefix)
			if strings.Join(got, "|") != strings.Join(test.want, "|") {
				t.Fatalf("prefixCandidates(%q) = %v, want %v", test.prefix, got, test.want)
			}
		})
	}
}
