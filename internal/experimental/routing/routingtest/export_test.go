package routingtest

// HasPendingWaiter reports whether a wait is registered on client, so a test can deliver a message after the wait begins.
func HasPendingWaiter(client *ProtocolTestClient) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.waiters) > 0
}

// WireChannelOf returns the channel client sends through, so a test can drive the transport's own WireChannel methods.
func WireChannelOf(client *ProtocolTestClient) WireChannel { return client.channel }
