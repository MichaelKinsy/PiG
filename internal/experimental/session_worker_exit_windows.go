package experimental

import "os"

// Windows child status carries an exit code, not a POSIX wait signal.
func workerSignalName(*os.ProcessState) string { return "" }
