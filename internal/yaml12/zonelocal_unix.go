//go:build !windows

package yaml12

import (
	"os"
	"path/filepath"
	"time"
)

// detectZoneID reads the zone the way Node does on Unix: TZ, or the /etc/localtime link.
func detectZoneID() string {
	tz, tzSet := os.LookupEnv("TZ")
	target, _ := filepath.EvalSymlinks("/etc/localtime")
	return zoneIDFrom(tz, tzSet, target)
}

// hostLocation is the zone the process reads times in; Go reads TZ and /etc/localtime itself.
func hostLocation() *time.Location { return time.Local }
