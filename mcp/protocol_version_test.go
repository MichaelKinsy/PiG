package mcp

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// types.ts:4-10: the client accepts the latest protocol version first, then three older ones, in this order.
func TestSupportedProtocolVersionsOrder(t *testing.T) {
	want := []SupportedProtocolVersion{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
	if len(SupportedProtocolVersions) != len(want) {
		t.Fatalf("versions = %v", SupportedProtocolVersions)
	}
	for i, version := range want {
		if SupportedProtocolVersions[i] != version {
			t.Errorf("version %d = %q, want %q", i, SupportedProtocolVersions[i], version)
		}
	}
	if string(SupportedProtocolVersions[0]) != LatestProtocolVersion {
		t.Errorf("the first supported version is the latest: %q", SupportedProtocolVersions[0])
	}
}

// upstream: packages/mcp/src/protocol/types.ts:4-10 LATEST_PROTOCOL_VERSION, SUPPORTED_PROTOCOL_VERSIONS = [LATEST_PROTOCOL_VERSION, ...] as const and
// SupportedProtocolVersion = (typeof SUPPORTED_PROTOCOL_VERSIONS)[number]: the Go versions are the pinned source's, in its order.
func TestSupportedProtocolVersionsAreThePinnedSourceArray(t *testing.T) {
	data, err := os.ReadFile("../.upstream/current/packages/mcp/src/protocol/types.ts")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	latest := regexp.MustCompile(`LATEST_PROTOCOL_VERSION\s*=\s*"([^"]+)"`).FindStringSubmatch(source)
	list := regexp.MustCompile(`SUPPORTED_PROTOCOL_VERSIONS\s*=\s*\[([^\]]*)\]`).FindStringSubmatch(source)
	if latest == nil || list == nil {
		t.Fatal("the pinned types.ts declares no LATEST_PROTOCOL_VERSION or SUPPORTED_PROTOCOL_VERSIONS")
	}
	var want []SupportedProtocolVersion
	for item := range strings.SplitSeq(list[1], ",") {
		switch item = strings.Trim(strings.TrimSpace(item), `"`); item {
		case "":
		case "LATEST_PROTOCOL_VERSION":
			want = append(want, SupportedProtocolVersion(latest[1]))
		default:
			want = append(want, SupportedProtocolVersion(item))
		}
	}
	if LatestProtocolVersion != latest[1] || !slices.Equal(SupportedProtocolVersions, want) {
		t.Errorf("Go latest %q and versions %v, pinned source %q and %v", LatestProtocolVersion, SupportedProtocolVersions, latest[1], want)
	}
}
