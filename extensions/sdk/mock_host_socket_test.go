// SPDX-License-Identifier: MIT

package sdk

import (
	"net"
	"testing"
	"time"
)

// A second mock host, as another test process on the same machine makes one, must leave the first host's socket in
// place: an extension that dials the first host's path reaches the first host.
func TestMockHostSocketIsItsOwn(t *testing.T) {
	first := newMockHost(t)
	defer first.close()
	second := newMockHost(t)
	defer second.close()
	if first.sockPath == second.sockPath {
		t.Fatalf("both mock hosts listen on %s", first.sockPath)
	}
	nc, err := net.Dial("unix", first.sockPath)
	if err != nil {
		t.Fatalf("dial the first host: %v", err)
	}
	defer func() { _ = nc.Close() }()
	if err := first.listener.(*net.UnixListener).SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	accepted, err := first.listener.Accept()
	if err != nil {
		t.Fatalf("the first host did not receive the connection: %v", err)
	}
	_ = accepted.Close()
}
