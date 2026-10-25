// Package nodehome is Node's os.homedir(), which Pi's getHomeDir (package-manager.ts) falls back to when HOME is empty or absent: the home directory
// the environment names, else the one the system's user database lists for the process's user, else a thrown SystemError. Go's os.UserHomeDir reads
// the environment alone, and os/user.Current reads the user database alone; neither throws as Node does.
package nodehome

import (
	"os"
	"os/user"
	"runtime"
	"sync/atomic"
)

// Error is the SystemError Node throws when neither the environment nor the user database names a home directory: ERR_SYSTEM_ERROR, errno ENOENT
// from libuv's uv_os_homedir. Its text is the message Node's error carries.
type Error struct{}

func (*Error) Error() string {
	return "A system error occurred: uv_os_homedir returned ENOENT (no such file or directory)"
}

// Dir is os.homedir(): the home variable of the platform (HOME, or USERPROFILE on Windows) when it is set, an explicit empty value included, else the
// home directory of the user database's entry for the process's user. With neither it returns *Error.
func Dir() (string, error) {
	if override := overridden.Load(); override != nil {
		return (*override)()
	}
	return dir(runtime.GOOS, os.LookupEnv, user.Current)
}

var overridden atomic.Pointer[func() (string, error)]

// Override makes Dir answer with lookup until the returned restore function runs, so a test can stand in a host that has no home directory, which
// no environment variable can do while the user database lists the test's user. Production code never installs one.
func Override(lookup func() (string, error)) (restore func()) {
	previous := overridden.Swap(&lookup)
	return func() { overridden.Store(previous) }
}

func dir(goos string, lookupEnv func(string) (string, bool), current func() (*user.User, error)) (string, error) {
	key := "HOME"
	if goos == "windows" {
		key = "USERPROFILE"
	}
	if home, present := lookupEnv(key); present {
		return home, nil
	}
	if account, err := current(); err == nil {
		return account.HomeDir, nil
	}
	return "", &Error{}
}
