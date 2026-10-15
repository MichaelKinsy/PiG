package yaml12

import (
	"cmp"
	"encoding/base64"
	"encoding/binary"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// zoneTimeline is the zone name V8 prints for a zone from 1970 to 2038. The zone prints names[0] at the epoch and the other name after the first change; changes holds the change instants, in seconds, as unsigned varint deltas in standard base64.
type zoneTimeline struct {
	id      string
	names   []string
	changes string
}

// icuZoneName is the long zone name V8 prints for an instant in [0, maxEpochTimeMs] in a zone, or false for a zone the generated data does not cover. It reads no host zone data.
func icuZoneName(zoneID string, ms int64) (string, bool) {
	i, ok := slices.BinarySearchFunc(zoneTimelines, zoneID, func(z zoneTimeline, id string) int { return strings.Compare(z.id, id) })
	if !ok {
		return "", false
	}
	z := zoneTimelines[i]
	changes, ok := zoneChanges(z)
	if !ok {
		return "", false
	}
	passed := 0
	for _, at := range changes {
		if at > uint64(ms/1000) {
			break
		}
		passed++
	}
	return z.names[passed%len(z.names)], true
}

// zoneChanges decodes the change instants of a zone, in seconds since the epoch.
func zoneChanges(z zoneTimeline) ([]uint64, bool) {
	data, err := base64.StdEncoding.DecodeString(z.changes)
	if err != nil {
		return nil, false
	}
	var out []uint64
	var at uint64
	for len(data) > 0 {
		delta, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, false
		}
		data = data[n:]
		at += delta
		out = append(out, at)
	}
	return out, true
}

// maxEpochTimeMs is V8's kMaxEpochTimeInMs: the latest instant for which V8 asks the time zone for a name directly.
const maxEpochTimeMs = int64(2147483647) * 1000

// equivalentTime is V8's DateCache::EquivalentTime: an instant before 1970 or after 2038 is named as the same calendar day and time in the 2008 to 2037 year with the same leap-ness and first weekday.
func equivalentTime(ms int64) int64 {
	if ms >= 0 && ms <= maxEpochTimeMs {
		return ms
	}
	t := time.UnixMilli(ms).UTC()
	year := t.Year()
	weekday := int(time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC).Weekday())
	leap := year%4 == 0 && (year%100 != 0 || year%400 == 0)
	recent := 1967
	if leap {
		recent = 1956
	}
	recent += weekday * 12 % 28
	equivalent := 2008 + (recent+3*28-2008)%28
	return time.Date(equivalent, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC).UnixMilli()
}

// zoneIDFrom names the zone Node resolves on a Unix host: TZ when it is set (with a leading colon and a zoneinfo directory prefix removed), otherwise the zone the /etc/localtime link points at. It is empty when neither names a zone.
func zoneIDFrom(tz string, tzSet bool, localtimeTarget string) string {
	if tzSet {
		tz = strings.TrimPrefix(tz, ":")
		if _, after, ok := strings.Cut(tz, "zoneinfo/"); ok {
			tz = after
		}
		return tz
	}
	_, after, _ := strings.Cut(filepath.ToSlash(localtimeTarget), "zoneinfo/")
	return after
}

type windowsZone struct{ windows, territory, iana string }

// windowsZoneID is the IANA zone Node's ICU picks for a Windows time zone key name such as "Pacific Standard Time" and the user's region (an ISO 3166 code such as "CA"): CLDR's first zone for that territory, or its territory-001 zone when the region has none. It is empty for a name CLDR does not list.
func windowsZoneID(keyName, region string) string {
	find := func(territory string) string {
		i, ok := slices.BinarySearchFunc(windowsZones, [2]string{keyName, territory}, func(z windowsZone, k [2]string) int {
			return cmp.Or(strings.Compare(z.windows, k[0]), strings.Compare(z.territory, k[1]))
		})
		if !ok {
			return ""
		}
		return windowsZones[i].iana
	}
	if region != "" {
		if id := find(region); id != "" {
			return id
		}
	}
	return find("001")
}

// windowsZoneIDFrom names the zone Node resolves on a Windows host: TZ when it is set and not empty, otherwise the registry's TimeZoneKeyName with the user's region.
func windowsZoneIDFrom(tz string, tzSet bool, keyName, region string) string {
	if tzSet && tz != "" {
		return zoneIDFrom(tz, true, "")
	}
	return windowsZoneID(keyName, region)
}

// locationForTZ is the location a TZ value names, or false when it names none this process can load.
func locationForTZ(tz string) (*time.Location, bool) {
	id := zoneIDFrom(tz, true, "")
	if id == "" {
		return nil, false
	}
	loc, err := time.LoadLocation(id)
	return loc, err == nil
}

// localZoneID names the zone Node resolves for the process.
var localZoneID = sync.OnceValue(detectZoneID)

// jsDateString is Date.prototype.toString in the local time zone.
func jsDateString(ms float64) string {
	return dateStringIn(ms, hostLocation(), localZoneID())
}

// dateStringIn is Date.prototype.toString for a process whose zone is loc, named zoneID. V8 names the zone from ICU's display name at equivalentTime(ms); the generated timeline gives that name without host zone data, and a zone it does not cover keeps Go's zone name. The offset in front of the name still comes from loc.
func dateStringIn(ms float64, loc *time.Location, zoneID string) string {
	if math.IsNaN(ms) {
		return "Invalid Date"
	}
	t := time.UnixMilli(int64(ms)).In(loc)
	name, _ := t.Zone()
	if zoneID == "" && name == "UTC" {
		zoneID = "UTC"
	}
	if long, ok := icuZoneName(zoneID, equivalentTime(t.UnixMilli())); ok {
		name = long
	}
	return t.Format("Mon Jan 02 2006 15:04:05 GMT-0700") + " (" + name + ")"
}
