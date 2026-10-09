package clock

import "time"

var now = time.Now

func bad() time.Time { return time.Now() } // want `time.Now\(\) bypasses the package's injected clock`

func good() time.Time { return now() }
