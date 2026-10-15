//go:build !windows

package codingagent

import "os"

// envVar is an environment variable read as os.Getenv reads it, which does not allocate off Windows (env_lookup_windows.go).
type envVar string

func newEnvVar(name string) envVar { return envVar(name) }

func (v envVar) get() string { return os.Getenv(string(v)) }

// isOne reports whether the variable's value is exactly "1".
func (v envVar) isOne() bool { return v.get() == "1" }
