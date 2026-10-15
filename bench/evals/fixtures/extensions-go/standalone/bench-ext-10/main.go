// SPDX-License-Identifier: MIT

// Command bench-ext-10 is the exact standalone form of bench-ext-10: always one isolated subprocess.
package main

import "example.com/benchext10/benchext"

func main() {
	if err := benchext.New("10").Run(); err != nil {
		panic(err)
	}
}
