package codemode

// Measured cold (no compilation cache): 19 MiB bare process, 64-67 MiB with a sandbox, 36-41 MiB with the cache.
// Reported before issue #211: 391 MiB. The bound leaves 2x for slower hosts and stays far under that.
const (
	rssBoundNoop       = 128 << 20
	rssBoundState      = 128 << 20
	rssBoundConcurrent = 128 << 20
)
