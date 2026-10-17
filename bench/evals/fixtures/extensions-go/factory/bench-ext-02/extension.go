// SPDX-License-Identifier: MIT

// Package benchext02 is bench-ext-02: the conventional factory form (packed subprocess cell, or fused into a Piglet Binary).
package benchext02

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"example.com/benchext10/benchext"
)

func Extension() *sdk.Extension { return benchext.New("02") }
