package surfaces

// NewestDigest is the digest of plugin's script in the newest set the client's updates were shown ("" when there is none): a
// test that holds a retry's timer open waits for a newer set to reach the client's watch with it.
func (c *Client) NewestDigest(plugin string) string {
	u := c.updates
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.latest == nil {
		return ""
	}
	for _, entry := range u.latest.Plugins {
		if entry.Name == plugin {
			return entry.Digest
		}
	}
	return ""
}

// Following is closed once the client's background follow of the scripts object ended: its watch is started (or it failed). Pi's
// startClient returns with the follow already made in a test's in-memory link (every promise there is a microtask); a goroutine has no
// such order, so a test that publishes a set right after the start waits for this.
func (c *Client) Following() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		c.following.Wait()
		close(done)
	}()
	return done
}

// Holding is whether the client's updates hold the scripts object: its release is recorded.
func (c *Client) Holding() bool {
	u := c.updates
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.release != nil
}

// Watching is whether the client's updates have their watch of the scripts object.
func (c *Client) Watching() bool {
	u := c.updates
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.watch != nil
}
