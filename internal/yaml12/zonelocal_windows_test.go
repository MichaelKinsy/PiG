package yaml12

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// nodeDateStrings asks the Node on this host for Date.prototype.toString of each instant. Node starts with the test process's environment, so the TZ of the test is the TZ Node sees.
func nodeDateStrings(t *testing.T, ms []int64) []string {
	t.Helper()
	var out []string
	pioracle.Run(t, `emit(input.map((m) => new Date(m).toString()));`, ms, &out)
	return out
}

func unsetTZ(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("TZ")
	if err := os.Unsetenv("TZ"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("TZ", old)
		}
	})
}

func hostProbeInstants() []int64 {
	var ms []int64
	for at := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(); at < time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(); at += 61*86400000 + 3600000 + 777 {
		ms = append(ms, at)
	}
	return append(ms, time.Date(1850, 3, 4, 5, 6, 7, 0, time.UTC).UnixMilli(), time.Date(2090, 8, 9, 10, 11, 12, 0, time.UTC).UnixMilli())
}

// TestWindowsRegistryZoneNamesMatchNode reads the zone of this Windows host from the registry and the user's region, as Node's ICU does when TZ is not set, and compares the zone name of the Date strings with the Node on the host.
func TestWindowsRegistryZoneNamesMatchNode(t *testing.T) {
	unsetTZ(t)
	key, region := timeZoneKeyName(), userRegion()
	t.Logf("registry key %q, region %q, zone %q", key, region, detectZoneID())
	if key == "" {
		t.Fatal("the registry has no TimeZoneKeyName")
	}
	if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(region) {
		t.Fatalf("user region %q is not an ISO 3166 code", region)
	}
	if detectZoneID() == "" {
		t.Fatalf("no zone for %q in %q", key, region)
	}
	ms := hostProbeInstants()
	want := nodeDateStrings(t, ms)
	for i, at := range ms {
		if got := dateStringIn(float64(at), hostLocation(), detectZoneID()); zoneNameOf(got) != zoneNameOf(want[i]) {
			t.Errorf("at %d: %s, node %s", at, got, want[i])
		}
	}
}

// TestWindowsHonoursTZ sets TZ for the test process and so for the Node it starts: both must read the Date in that zone, offset and name. The location comes from the embedded zone data, so the comparison does not depend on the runner's registry.
func TestWindowsHonoursTZ(t *testing.T) {
	ms := hostProbeInstants()
	for _, zone := range []string{"Pacific/Chatham", "Australia/Lord_Howe", "America/St_Johns", "Asia/Kathmandu", "Europe/Dublin", "America/Los_Angeles", "Asia/Kolkata", "UTC"} {
		t.Run(zone, func(t *testing.T) {
			t.Setenv("TZ", zone)
			if got := detectZoneID(); got != zone {
				t.Fatalf("detectZoneID() = %q", got)
			}
			want := nodeDateStrings(t, ms)
			for i, at := range ms[:len(ms)-2] {
				if got := dateStringIn(float64(at), hostLocation(), detectZoneID()); got != want[i] {
					t.Errorf("at %d:\n got %s\nwant %s", at, got, want[i])
				}
			}
		})
	}
}
