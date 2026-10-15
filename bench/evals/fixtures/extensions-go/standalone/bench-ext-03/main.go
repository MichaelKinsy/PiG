// SPDX-License-Identifier: MIT

// Command bench-ext-03 is the exact standalone form of bench-ext-03: always one isolated subprocess.
package main

import "example.com/benchext10/benchext"

func main() {
	if err := benchext.New("03").Run(); err != nil {
		panic(err)
	}
}
