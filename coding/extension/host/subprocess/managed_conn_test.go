package subprocess

// withConn installs conn as me's current connection, as an adoption would.
func withConn(me *managedExt, conn *Conn) *managedExt {
	me.conn.Store(conn)
	return me
}
