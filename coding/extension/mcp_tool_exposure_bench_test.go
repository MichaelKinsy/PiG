// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package extension

import "testing"

// BenchmarkGetMcpToolExposure is the exposure of one tool of a server whose toolExposure holds wildcard patterns: the lookup every tool of the server
// goes through when the tools are listed, searched or exposed. The tool matches the last pattern, so every pattern is tried.
func BenchmarkGetMcpToolExposure(b *testing.B) {
	config := McpServerConfig{
		Exposure: McpExposureCodemode,
		ToolExposure: NewOrderedExposures(
			"read_exact", "direct", "list_*", "hidden", "get_*_by_id", "direct", "*_admin", "hidden",
			"search_*", "direct", "create_*", "hidden", "update_*_field", "hidden", "delete_*", "hidden", "send_*_message", "direct",
		),
	}
	b.ReportAllocs()
	for b.Loop() {
		if GetMcpToolExposure(config, "send_user_message") != McpExposureDirect {
			b.Fatal("wrong exposure")
		}
	}
}
