package codemode

// DefaultInterruptBudget is the interrupt polls an InlineSandbox execution may use before it fails with ErrorTimeout.
//
// Ports packages/codemode/src/runtime/inline.ts (DEFAULT_INTERRUPT_BUDGET).
const DefaultInterruptBudget = 100_000

// inlineMaxStackSize is the VM stack guard of an in-thread VM: the VM runs on the caller's stack, which is smaller than
// a worker thread's, so the guard allows recursion about 1,000 calls deep and trips well before a 1 MB host stack
// overflows.
//
// Ports packages/codemode/src/runtime/inline.ts (INLINE_MAX_STACK_SIZE).
const inlineMaxStackSize = 256 * 1024
