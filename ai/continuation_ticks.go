package ai

// postTick queues a process.nextTick callback and runs the drain it starts.
func (executor *continuationExecutor) postTick(reaction func()) {
	if drain := executor.postTickDeferred(reaction); drain != nil {
		drain()
	}
}
