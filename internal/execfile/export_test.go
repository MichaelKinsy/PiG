package execfile

// SetChunkSize changes the bytes Write copies per hold of the fork lock and returns the function that restores it.
func SetChunkSize(n int64) (restore func()) {
	old := chunkSize
	chunkSize = n
	return func() { chunkSize = old }
}
