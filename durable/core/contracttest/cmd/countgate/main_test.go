// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

// The native instruction count takes valgrind only from VALGRIND, as durable/contract/lib/instr.mjs does: without it the
// command refuses before it runs anything, never counting with a valgrind found on PATH, whose release the baseline does
// not name.
func TestInstructionsRefusesWithoutVALGRIND(t *testing.T) {
	t.Setenv("VALGRIND", "")
	err := instructions([]string{"--rec", "missing.rec", "--size", "50"})
	if err == nil || !strings.Contains(err.Error(), "VALGRIND=PREFIX/bin/valgrind") {
		t.Fatalf("instructions without VALGRIND returned %v; want the refusal naming the pinned valgrind", err)
	}
}
