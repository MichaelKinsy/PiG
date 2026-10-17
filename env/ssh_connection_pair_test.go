package env

// sshConnectionPair destructures SshConnection's `{ connection, remote() }`, as the upstream tests do.
func sshConnectionPair(options SshConnectOptions) (*Connection, func() *RemotePlatform) {
	connected := SshConnection(options)
	return connected.Connection, connected.Remote
}
