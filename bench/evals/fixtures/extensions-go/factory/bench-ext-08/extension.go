// SPDX-License-Identifier: MIT

// Package benchext08 is bench-ext-08: the conventional factory form (packed subprocess cell, or fused into a Piglet Binary).
package benchext08

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"example.com/benchext10/benchext"
)

func Extension() *sdk.Extension { return benchext.New("08") }
