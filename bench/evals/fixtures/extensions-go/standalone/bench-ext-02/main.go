// SPDX-License-Identifier: MIT

// Command bench-ext-02 is the exact standalone form of bench-ext-02: always one isolated subprocess.
package main

import "example.com/benchext10/benchext"

func main() {
	if err := benchext.New("02").Run(); err != nil {
		panic(err)
	}
}
