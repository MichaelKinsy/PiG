// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package extension

import "testing"

// A tool pattern is compiled once: GetMcpToolExposure walks the wildcard patterns of the server for every tool it is asked about, and compiled each
// of them every time (BenchmarkGetMcpToolExposure: 465 allocations and 40 KB for one lookup that tries nine patterns).
func TestToolPatternRegExpIsCompiledOncePerPattern(t *testing.T) {
	first := toolPatternRegExp("get_*_by_id")
	if allocs := testing.AllocsPerRun(100, func() { _ = toolPatternRegExp("get_*_by_id") }); allocs != 0 {
		t.Errorf("toolPatternRegExp allocated %v times for a pattern it has seen, want 0", allocs)
	}
	if toolPatternRegExp("get_*_by_id") != first {
		t.Error("toolPatternRegExp returned another expression for the same pattern")
	}
	for tool, want := range map[string]bool{"get_user_by_id": true, "get__by_id": true, "get_a\nb_by_id": false, "get_a\u2028b_by_id": false, "xget_user_by_id": false, "get_user_by_idx": false} {
		if got := first.MatchString(tool); got != want {
			t.Errorf("get_*_by_id matches %q = %v, want %v", tool, got, want)
		}
	}
}

// A lookup that tries every wildcard pattern allocates what reading the keys and the exact entry allocates, and nothing for the patterns once they are compiled.
func TestGetMcpToolExposureAllocatesNothingForCompiledPatterns(t *testing.T) {
	config := McpServerConfig{Exposure: McpExposureCodemode, ToolExposure: NewOrderedExposures("list_*", "hidden", "get_*_by_id", "hidden", "send_*_message", "direct")}
	if GetMcpToolExposure(config, "send_user_message") != McpExposureDirect {
		t.Fatal("wrong exposure")
	}
	ready := toolPatternRegExp("send_*_message")
	own := testing.AllocsPerRun(100, func() {
		_, _ = config.ToolExposure.Get("send_user_message")
		_ = config.ToolExposure.Keys()
		_ = ready.MatchString("send_user_message")
		_, _ = config.ToolExposure.Get("send_*_message")
	})
	if allocs := testing.AllocsPerRun(100, func() { _ = GetMcpToolExposure(config, "send_user_message") }); allocs > own {
		t.Errorf("GetMcpToolExposure allocated %v times with its patterns compiled, reading the entries allocates %v", allocs, own)
	}
}
