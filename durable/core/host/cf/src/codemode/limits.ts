// The script limits of Pi 1.1.0+ea448f45 packages/codemode/src/runtime/prelude-source.ts, which the prelude enforces inside the VM; the Go
// constants MaxStoreValueChars, MaxStoreTotalChars, MaxOutputChars and MaxOutputItems (codemode/assets.go) hold the same values.
export const MAX_STORE_VALUE_CHARS = 256 * 1024
export const MAX_STORE_TOTAL_CHARS = 1024 * 1024
export const MAX_OUTPUT_CHARS = 16 * 1024 * 1024
export const MAX_OUTPUT_ITEMS = 100_000
