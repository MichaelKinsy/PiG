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
