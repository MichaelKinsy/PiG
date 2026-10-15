package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// A request the host abandons for inactivity reports a Settled channel that closes when the handler's late response arrives or the connection ends, never earlier.
func TestStalledRequestSettlesOnLateResponseOrConnectionEnd(t *testing.T) {
	for _, ending := range []string{"late response", "connection end"} {
		t.Run(ending, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hostSide, extSide := net.Pipe()
				conn := NewConn("stalled", hostSide)
				conn.Start(t.Context())
				go func() { _, _ = io.Copy(io.Discard, extSide) }()
				defer func() {
					_ = conn.Close("test done")
					_ = extSide.Close()
				}()
				stalled := make(chan *HandlerStalledError, 1)
				go func() {
					_, err := conn.requestWithInactivity(t.Context(), &Envelope{ID: "r1", Type: MsgRequest}, time.Second, "test")
					handlerStalled, _ := errors.AsType[*HandlerStalledError](err)
					stalled <- handlerStalled
				}()
				synctest.Wait()
				time.Sleep(2 * time.Second)
				synctest.Wait()
				err := <-stalled
				if err == nil || err.Settled == nil {
					t.Fatalf("stalled error = %+v; want a Settled channel", err)
				}
				select {
				case <-err.Settled:
					t.Fatal("Settled closed before the handler answered or the connection ended")
				default:
				}
				switch ending {
				case "late response":
					data, _ := json.Marshal(Envelope{Type: MsgResponse, ID: "r1", Response: &ResponsePayload{}})
					var length [4]byte
					binary.BigEndian.PutUint32(length[:], uint32(len(data)))
					_, _ = extSide.Write(append(length[:], data...))
				case "connection end":
					_ = extSide.Close()
				}
				synctest.Wait()
				select {
				case <-err.Settled:
				default:
					t.Fatalf("Settled stayed open after the %s", ending)
				}
				// A request abandoned after the reader exited is already settled.
				if ending == "connection end" {
					select {
					case <-conn.abandon("late"):
					default:
						t.Fatal("a request abandoned after the connection ended is not settled")
					}
				}
			})
		})
	}
}
