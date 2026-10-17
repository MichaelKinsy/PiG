package ai

import (
	"strings"
	"testing"
)

// TestUserAgentReadsTheKernelReleaseOnce pins that building the User-Agent of many requests reads the release (uname) once, and that every
// header carries it.
func TestUserAgentReadsTheKernelReleaseOnce(t *testing.T) {
	reads := 0
	previous := userAgentRelease
	userAgentRelease = &releaseCache{read: func() string { reads++; return "9.8.7-test" }}
	t.Cleanup(func() { userAgentRelease = previous })
	for range 3 {
		if got := PiUserAgent(); !strings.Contains(got, " 9.8.7-test;") {
			t.Fatalf("PiUserAgent() = %q, want the kernel release 9.8.7-test", got)
		}
	}
	if reads != 1 {
		t.Fatalf("the kernel release was read %d times for 3 user agents, want 1", reads)
	}
}
