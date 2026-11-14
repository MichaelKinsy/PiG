package coding

import "net"

// socket.destroy() while the request body is still arriving: the FIN of close(), then a reset for the bytes that arrive after it. Linux
// gives a read the queued FIN before the reset's error (tcp_recvmsg tests SOCK_DONE before sk_err), which is the end of the stream Node's
// read reports in the oracle's destroy rows; BSD's soreceive gives the error first, so this row is Linux's.
func platformSessionConnectionEnds() []sessionConnectionEnd {
	return []sessionConnectionEnd{{"close at the request headers", false, func(conn *net.TCPConn) {
		_ = conn.CloseWrite()
		_ = conn.SetLinger(0)
		_ = conn.Close()
	}, sessionConnectionClosed}}
}
