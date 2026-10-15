// SPDX-License-Identifier: MIT

// Command bench-ext-05 is the exact standalone form of bench-ext-05: always one isolated subprocess.
package main

import "example.com/benchext10/benchext"

func main() {
	if err := benchext.New("05").Run(); err != nil {
		panic(err)
	}
}
