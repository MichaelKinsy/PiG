// SPDX-License-Identifier: MIT

// Command floor is the harness floor for the PTY first-frame probe: a static Go binary that writes one byte to its terminal and waits, so the probe's fork, exec and read overhead can be subtracted from pig and phi.
package main

import (
	"os"
	"time"
)

func main() {
	_, _ = os.Stdout.WriteString("x")
	time.Sleep(10 * time.Second)
}
