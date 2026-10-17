// SPDX-License-Identifier: MIT

// Command bench-ext-06 is the exact standalone form of bench-ext-06: always one isolated subprocess.
package main

import "example.com/benchext10/benchext"

func main() {
	if err := benchext.New("06").Run(); err != nil {
		panic(err)
	}
}
