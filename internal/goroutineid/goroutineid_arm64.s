//go:build gc && (linux || darwin)

#include "textflag.h"

// func goidAt(offset uintptr) uint64
TEXT ·goidAt(SB), NOSPLIT, $0-16
	MOVD offset+0(FP), R0
	ADD  g, R0, R0
	MOVD (R0), R0
	MOVD R0, ret+8(FP)
	RET
