//go:build parity

package codingagent

import (
	"cmp"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestMergeHeadersMatchesPinnedPiSource(t *testing.T) {
	command := exec.Command("node", "parity/testdata/provider-headers-pi.mjs")
	command.Dir = filepath.Join("..", "..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("run pinned Pi header fixture: %v", err)
	}
	var upstream []struct {
		Name    string     `json:"name"`
		Headers [][]string `json:"headers"`
	}
	if err := json.Unmarshal(output, &upstream); err != nil {
		t.Fatalf("decode pinned Pi header fixture: %v", err)
	}
	value := func(text string) *string { return &text }
	cases := map[string]struct {
		base      map[string]string
		overrides map[string]*string
	}{
		"delete-case-insensitive":  {base: map[string]string{"Authorization": "base", "X-Keep": "yes"}, overrides: map[string]*string{"authorization": nil, "x-new": value("new")}},
		"replace-case-insensitive": {base: map[string]string{"X-Test": "old"}, overrides: map[string]*string{"x-test": value("new")}},
		"delete-last-header":       {base: map[string]string{"X-Only": "value"}, overrides: map[string]*string{"x-only": nil}},
		"undefined-input":          {},
	}
	if len(upstream) != len(cases) {
		t.Fatalf("upstream cases = %d, want %d", len(upstream), len(cases))
	}
	for _, upstreamCase := range upstream {
		testCase, ok := cases[upstreamCase.Name]
		if !ok {
			t.Fatalf("unknown upstream case %q", upstreamCase.Name)
		}
		resolved := mergeHeaders(testCase.base, testCase.overrides, nil)
		got := make([][]string, 0, len(resolved))
		for name, headerValue := range resolved {
			got = append(got, []string{name, headerValue})
		}
		slices.SortFunc(got, func(left, right []string) int { return cmp.Compare(left[0], right[0]) })
		if !slices.EqualFunc(got, upstreamCase.Headers, slices.Equal) {
			t.Errorf("%s headers = %v, want pinned Pi %v", upstreamCase.Name, got, upstreamCase.Headers)
		}
	}
}
