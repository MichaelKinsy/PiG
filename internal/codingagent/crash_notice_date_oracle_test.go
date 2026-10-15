package codingagent

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// interactive-mode.ts:1222 formats a crash record's time with `new Date(crash.timestamp).toLocaleString()`. The notice's time against Node's en-US
// output for instants across the whole Date range in five zones: the year is the era year without padding (year 151 is "151", 1 BCE is "1", 1721
// BCE is "1721") and zone offsets before standardisation keep their seconds.
// mutation-checked: the padded year, the signed year before 1 CE and the year counted from 0 instead of 1 each fail it. The locale is en-US; Node would use
// the process locale (LANG) for this call, which is not modelled.
func TestCrashNoticeTimeMatchesToLocaleString(t *testing.T) {
	random := rand.New(rand.NewSource(3))
	var instants []int64
	for range 1500 {
		switch random.Intn(4) {
		case 0:
			instants = append(instants, random.Int63n(4102444800000))
		case 1:
			instants = append(instants, -random.Int63n(62135596800000*2))
		case 2:
			instants = append(instants, random.Int63n(8640000000000000*2)-8640000000000000)
		default:
			instants = append(instants, (random.Int63n(2000)-1000)*86400000*365+random.Int63n(86400000))
		}
	}
	// Midnight, noon and the instants around year 0.
	instants = append(instants, 0, 12*3600*1000, -62167219200000, -62167219200001, -62135596800000, -62135596800001, 8640000000000000, -8640000000000000)
	for _, zone := range []string{"America/New_York", "Asia/Kolkata", "UTC", "Pacific/Auckland", "Europe/London"} {
		location, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		var want [][]string
		pioracle.Run(t, `emit(input.map((ms) => { const d = new Date(ms); return [d.toISOString(), d.toLocaleString()]; }));`, instants, &want, "TZ="+zone)
		for i, pair := range want {
			notice := crashNoticeIn(CrashRecord{Timestamp: pair[0], Message: "m"}, location)
			got := strings.TrimSuffix(strings.TrimPrefix(notice, AppName+" crashed on "), " (m). Run /bug to report it; the crash details are attached automatically.")
			if got != pair[1] {
				t.Errorf("%s instant %d (%s): notice time %q, Node %q", zone, instants[i], pair[0], got, pair[1])
			}
		}
	}
}

// A timestamp that names no zone is local time for Date, so crashNoticeIn reads it in the zone it prints in: Node with TZ=Pacific/Chatham prints
// new Date("2024-03-10T12:00:00").toLocaleString() as "3/10/2024, 12:00:00 PM".
func TestCrashNoticeReadsAZonelessTimestampInItsZone(t *testing.T) {
	location, err := time.LoadLocation("Pacific/Chatham")
	if err != nil {
		t.Fatal(err)
	}
	notice := crashNoticeIn(CrashRecord{Timestamp: "2024-03-10T12:00:00", Message: "m"}, location)
	if !strings.Contains(notice, " crashed on 3/10/2024, 12:00:00 PM (m)") {
		t.Fatalf("notice = %q", notice)
	}
}
