package hardtmp

import "os"

func bad() error {
	return os.MkdirAll("/tmp/pig-test", 0o700) // want `hard-coded /tmp path in os.MkdirAll`
}

func notFS() string { return "/tmp/fake" }

func good() (string, error) { return os.MkdirTemp("", "x") }
