//go:build gc && (linux || darwin)

#include "textflag.h"

// func goidAt(offset uintptr) uint64
TEXT ·goidAt(SB), NOSPLIT, $0-16
	MOVQ offset+0(FP), CX
	MOVQ (TLS), AX
	MOVQ (AX)(CX*1), AX
	MOVQ AX, ret+8(FP)
	RET

// func gptr() uintptr
TEXT ·gptr(SB), NOSPLIT, $0-8
	MOVQ (TLS), AX
	MOVQ AX, ret+0(FP)
	RET
