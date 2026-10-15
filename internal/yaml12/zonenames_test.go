package yaml12

import (
	"cmp"
	"math"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the zone rules the comparison needs, whatever the host provides

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// zoneProbeInstants are instants from 1850 to 2100 at an irregular step, so every month, hour and weekday pairing occurs, plus the edges of V8's equivalent-year range.
func zoneProbeInstants() []int64 {
	var out []int64
	const day = 86400000
	for ms := time.Date(1850, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(); ms < time.Date(2101, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(); ms += 37*day + 5*3600000 + 1234567 {
		out = append(out, ms)
	}
	for _, edge := range []int64{0, maxEpochTimeMs} {
		out = append(out, edge-86400000, edge-1, edge, edge+1, edge+86400000)
	}
	return out
}

// TestDateStringMatchesNode compares Date.prototype.toString under Node's ICU with dateStringIn for every generated zone, at irregular instants from 1850 to 2100 and at both sides of every name change in the table. The name is read with loc set to UTC, so no host zone data can reach it. Node switches zone through process.env.TZ.
func TestDateStringMatchesNode(t *testing.T) {
	type probe struct {
		Zone string  `json:"zone"`
		Ms   []int64 `json:"ms"`
	}
	ms := zoneProbeInstants()
	var probes []probe
	for _, z := range zoneTimelines {
		instants := slices.Clone(ms)
		changes, ok := zoneChanges(z)
		if !ok {
			t.Fatalf("zone %s: undecodable changes", z.id)
		}
		for _, at := range changes {
			// Date.toString names the equivalent instant, so a change at second s shows at s itself and, shifted by whole equivalent years, for any later year with the same calendar position; probing s and s-1 ms catches an off-by-one in the table.
			instants = append(instants, int64(at)*1000-1, int64(at)*1000)
		}
		probes = append(probes, probe{z.id, instants})
	}
	var want [][]string
	pioracle.Run(t, `
emit(input.map(({ zone, ms }) => { process.env.TZ = zone; return ms.map((m) => new Date(m).toString()); }));`, probes, &want)
	bad, compared := 0, 0
	for i, p := range probes {
		for j, ms := range p.Ms {
			compared++
			if got := zoneNameOf(dateStringIn(float64(ms), time.UTC, p.Zone)); got != zoneNameOf(want[i][j]) {
				if bad++; bad <= 25 {
					t.Errorf("%s at %d: name %q, want %q", p.Zone, ms, got, zoneNameOf(want[i][j]))
				}
			}
		}
	}
	t.Logf("compared %d instants", compared)
	if bad > 0 {
		t.Errorf("%d instants differ", bad)
	}
}

// TestDateStringOffsetsMatchNode compares the whole Date string, offset included, for zones whose rules have been stable: the offset comes from Go's zone data, which is not host independent, so it is checked only inside 1970 to 2025.
func TestDateStringOffsetsMatchNode(t *testing.T) {
	zones := []string{"Pacific/Chatham", "Australia/Lord_Howe", "America/St_Johns", "Asia/Kathmandu", "Europe/Dublin", "America/Los_Angeles", "Asia/Kolkata", "Europe/London", "Australia/Sydney", "Europe/Moscow", "UTC"}
	type probe struct {
		Zone string  `json:"zone"`
		Ms   []int64 `json:"ms"`
	}
	var ms []int64
	for at := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(); at < time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(); at += 9*86400000 + 3600000 + 777 {
		ms = append(ms, at)
	}
	var probes []probe
	for _, z := range zones {
		probes = append(probes, probe{z, ms})
	}
	var want [][]string
	pioracle.Run(t, `
emit(input.map(({ zone, ms }) => { process.env.TZ = zone; return ms.map((m) => new Date(m).toString()); }));`, probes, &want)
	for i, p := range probes {
		loc, err := time.LoadLocation(p.Zone)
		if err != nil {
			t.Fatal(err)
		}
		for j, at := range p.Ms {
			if got := dateStringIn(float64(at), loc, p.Zone); got != want[i][j] {
				t.Errorf("%s at %d:\n got %s\nwant %s", p.Zone, at, got, want[i][j])
				break
			}
		}
	}
}

// zoneNameOf is the parenthesised zone name of a Date string. The offset in front of it comes from Go's own zone data, which can differ from ICU's for old local mean times.
func zoneNameOf(s string) string { _, after, _ := strings.Cut(s, "("); return after }

// TestZoneIDFrom covers how the process zone is named from TZ and /etc/localtime.
func TestZoneIDFrom(t *testing.T) {
	for _, c := range []struct {
		tz       string
		set      bool
		link     string
		expected string
	}{
		{"Pacific/Chatham", true, "/usr/share/zoneinfo/UTC", "Pacific/Chatham"},
		{":Australia/Lord_Howe", true, "", "Australia/Lord_Howe"},
		{"/usr/share/zoneinfo/Asia/Kolkata", true, "", "Asia/Kolkata"},
		{"", true, "/usr/share/zoneinfo/Asia/Tokyo", ""},
		{"", false, "/usr/share/zoneinfo/Asia/Tokyo", "Asia/Tokyo"},
		{"", false, "/var/db/timezone/zoneinfo/America/St_Johns", "America/St_Johns"},
		{"", false, "", ""},
	} {
		if got := zoneIDFrom(c.tz, c.set, c.link); got != c.expected {
			t.Errorf("zoneIDFrom(%q, %v, %q) = %q, want %q", c.tz, c.set, c.link, got, c.expected)
		}
	}
}

// TestInvalidDateString covers the one case that needs no zone.
func TestInvalidDateString(t *testing.T) {
	if got := dateStringIn(math.NaN(), time.UTC, "UTC"); got != "Invalid Date" {
		t.Errorf("NaN date = %q", got)
	}
}

// TestDateKeysInDifferentTimeZones runs the library comparison in child processes whose TZ differs, so the production path (TZ or /etc/localtime, then time.Local) is read the way Node reads it. The zones include ones whose names come from the generated table only: half-hour, quarter-hour and Lord Howe's half-hour daylight saving.
func TestDateKeysInDifferentTimeZones(t *testing.T) {
	if os.Getenv("YAML12_ZONE_CHILD") != "" {
		t.Skip("running as the child")
	}
	for _, zone := range []string{"Pacific/Chatham", "Australia/Lord_Howe", "America/St_Johns", "Asia/Kathmandu", "Europe/Dublin", "America/Los_Angeles", "UTC", "Asia/Kolkata", "US/Pacific"} {
		t.Run(zone, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run", "^TestParseMatchesTheLibrary$", "-test.count=1")
			cmd.Env = append(os.Environ(), "TZ="+zone, "YAML12_ZONE_CHILD=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, truncateLines(string(out), 30))
			}
		})
	}
}

func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// TestWinterNamesIgnoreGoZoneData checks the zones whose daylight flag differs between Go's zone data and ICU: Dublin counts winter as daylight time in Go's data, and Morocco's flag is inverted since 2018. The names are the ones Node prints, whatever location the Date is read in.
func TestWinterNamesIgnoreGoZoneData(t *testing.T) {
	for _, c := range []struct {
		zone string
		at   time.Time
		want string
	}{
		{"Europe/Dublin", time.Date(2001, 12, 14, 0, 0, 0, 0, time.UTC), "Greenwich Mean Time"},
		{"Europe/Dublin", time.Date(2001, 7, 14, 0, 0, 0, 0, time.UTC), "Irish Standard Time"},
		{"Europe/Dublin", time.Date(1850, 1, 14, 0, 0, 0, 0, time.UTC), "Greenwich Mean Time"},
		{"Africa/Casablanca", time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC), "GMT+01:00"},
		{"Africa/Casablanca", time.Date(2023, 3, 25, 0, 0, 0, 0, time.UTC), "GMT+00:00"},
	} {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range []*time.Location{time.UTC, loc, time.FixedZone("x", 12345)} {
			got := zoneNameOf(dateStringIn(float64(c.at.UnixMilli()), l, c.zone))
			if got != c.want+")" {
				t.Errorf("%s %s in %s: %q, want %q", c.zone, c.at.Format("2006-01-02"), l, got, c.want+")")
			}
		}
	}
}

// TestWindowsZones checks the CLDR mapping Node's ICU uses to name the host zone on Windows: the table is sorted for lookup, every target is a zone whose names are generated, and the mapping of well-known Windows names is the one CLDR gives.
func TestWindowsZones(t *testing.T) {
	for i, z := range windowsZones {
		if i > 0 && cmp.Or(strings.Compare(windowsZones[i-1].windows, z.windows), strings.Compare(windowsZones[i-1].territory, z.territory)) >= 0 {
			t.Errorf("windowsZones not strictly sorted at %q %q", z.windows, z.territory)
		}
		if _, ok := icuZoneName(z.iana, 0); !ok {
			t.Errorf("%q maps to %q, which has no generated names", z.windows, z.iana)
		}
	}
	for windows, want := range map[string]string{
		"Pacific Standard Time":          "America/Los_Angeles",
		"W. Europe Standard Time":        "Europe/Berlin",
		"GMT Standard Time":              "Europe/London",
		"India Standard Time":            "Asia/Calcutta",
		"Nepal Standard Time":            "Asia/Katmandu",
		"Chatham Islands Standard Time":  "Pacific/Chatham",
		"Lord Howe Standard Time":        "Australia/Lord_Howe",
		"Newfoundland Standard Time":     "America/St_Johns",
		"Morocco Standard Time":          "Africa/Casablanca",
		"UTC":                            "Etc/UTC",
		"Pacific Standard Time (Mexico)": "America/Tijuana",
		"No Such Standard Time":          "",
		"":                               "",
	} {
		if got := windowsZoneID(windows, ""); got != want {
			t.Errorf("windowsZoneID(%q) = %q, want %q", windows, got, want)
		}
	}
	// ICU asks with the user's region: a territory's first listed zone, else the territory-001 zone.
	for _, c := range []struct{ windows, region, want string }{
		{"Pacific Standard Time", "CA", "America/Vancouver"},
		{"Pacific Standard Time", "US", "America/Los_Angeles"},
		{"Pacific Standard Time", "DE", "America/Los_Angeles"},
		{"Pacific Standard Time", "", "America/Los_Angeles"},
		{"SA Pacific Standard Time", "BR", "America/Rio_Branco"},
		{"SA Pacific Standard Time", "CA", "America/Coral_Harbour"},
		{"Central Pacific Standard Time", "FM", "Pacific/Ponape"},
		{"W. Europe Standard Time", "AT", "Europe/Vienna"},
		{"W. Europe Standard Time", "CH", "Europe/Zurich"},
		{"W. Europe Standard Time", "SE", "Europe/Stockholm"},
		{"India Standard Time", "IN", "Asia/Calcutta"},
		{"China Standard Time", "HK", "Asia/Hong_Kong"},
		{"China Standard Time", "CN", "Asia/Shanghai"},
		{"Eastern Standard Time", "CA", "America/Toronto"},
		{"No Such Standard Time", "CA", ""},
	} {
		if got := windowsZoneID(c.windows, c.region); got != c.want {
			t.Errorf("windowsZoneID(%q, %q) = %q, want %q", c.windows, c.region, got, c.want)
		}
	}
}

// TestWindowsZoneIDFrom covers the order Node resolves a Windows host zone in: a TZ value that is set and not empty, then the registry key name with the user's region.
func TestWindowsZoneIDFrom(t *testing.T) {
	for _, c := range []struct {
		tz     string
		set    bool
		key    string
		region string
		want   string
	}{
		{"Pacific/Chatham", true, "Pacific Standard Time", "US", "Pacific/Chatham"},
		{":Asia/Kolkata", true, "UTC", "", "Asia/Kolkata"},
		{"", true, "Pacific Standard Time", "CA", "America/Vancouver"},
		{"", false, "Pacific Standard Time", "CA", "America/Vancouver"},
		{"", false, "Pacific Standard Time", "US", "America/Los_Angeles"},
		{"", false, "Unknown Standard Time", "US", ""},
	} {
		if got := windowsZoneIDFrom(c.tz, c.set, c.key, c.region); got != c.want {
			t.Errorf("windowsZoneIDFrom(%q, %v, %q, %q) = %q, want %q", c.tz, c.set, c.key, c.region, got, c.want)
		}
	}
	if loc, ok := locationForTZ("Pacific/Chatham"); !ok || loc.String() != "Pacific/Chatham" {
		t.Errorf("locationForTZ(Chatham) = %v, %v", loc, ok)
	}
	for _, tz := range []string{"", "No/Such_Zone"} {
		if _, ok := locationForTZ(tz); ok {
			t.Errorf("locationForTZ(%q) loaded a location", tz)
		}
	}
}

// TestWindowsZoneNamesMatchNode reads the date of a host whose Windows zone is a given key name: the IANA zone CLDR picks must print what Node prints for that zone.
func TestWindowsZoneNamesMatchNode(t *testing.T) {
	keys := []string{"Pacific Standard Time", "W. Europe Standard Time", "GMT Standard Time", "India Standard Time", "Nepal Standard Time", "Chatham Islands Standard Time", "Lord Howe Standard Time", "Newfoundland Standard Time", "Morocco Standard Time", "AUS Eastern Standard Time", "Russian Standard Time", "Singapore Standard Time"}
	type probe struct {
		Zone string  `json:"zone"`
		Ms   []int64 `json:"ms"`
	}
	ms := []int64{time.Date(2001, 12, 14, 21, 59, 43, 0, time.UTC).UnixMilli(), time.Date(2015, 7, 4, 3, 0, 0, 0, time.UTC).UnixMilli(), time.Date(1850, 1, 15, 0, 0, 0, 0, time.UTC).UnixMilli(), time.Date(2090, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}
	var probes []probe
	for _, k := range keys {
		probes = append(probes, probe{windowsZoneID(k, ""), ms})
	}
	var want [][]string
	pioracle.Run(t, `
emit(input.map(({ zone, ms }) => { process.env.TZ = zone; return ms.map((m) => new Date(m).toString()); }));`, probes, &want)
	for i, p := range probes {
		for j, at := range p.Ms {
			if got := zoneNameOf(dateStringIn(float64(at), time.UTC, p.Zone)); got != zoneNameOf(want[i][j]) {
				t.Errorf("%s (%s) at %d: %q, want %q", keys[i], p.Zone, at, got, zoneNameOf(want[i][j]))
			}
		}
	}
}
