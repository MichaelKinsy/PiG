//go:build linux

package nativeplatform

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func TestX11IncrementalSelectionWireCleanup(t *testing.T) {
	for _, mode := range []string{"complete", "invalid", "partial"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, server := net.Pipe()
				defer func() { _ = client.Close(); _ = server.Close() }()
				deadline := time.Now().Add(clipboardTransferTimeout)
				if err := client.SetDeadline(deadline); err != nil {
					t.Fatal(err)
				}
				peerDone := make(chan error, 1)
				go func() { peerDone <- serveIncrementalX11(server, mode) }()
				clipboard := x11Clipboard{connection: client, window: 1, clipboard: 2, property: 3, incr: 4, deadline: deadline}
				start := time.Now()
				result, err := clipboard.requestSelection(5)
				_ = client.Close()
				if peerErr := <-peerDone; peerErr != nil {
					t.Fatal(peerErr)
				}
				if mode == "complete" {
					if err != nil || len(result.data) != 4096 || result.items != 4096 || result.typeID != 5 || result.format != 8 {
						t.Fatalf("complete=%+v err=%v", result, err)
					}
				} else if err == nil || result.data != nil || result.items != 0 {
					t.Fatalf("failed transfer retained data=%d items=%d err=%v", len(result.data), result.items, err)
				}
				if mode == "partial" && time.Since(start) != clipboardTransferTimeout {
					t.Fatalf("deadline=%v, want %v", time.Since(start), clipboardTransferTimeout)
				}
			})
		})
	}
}

func serveIncrementalX11(connection net.Conn, mode string) error {
	order := binary.LittleEndian
	var sequence uint16
	read := func() (byte, error) {
		var header [4]byte
		if _, err := io.ReadFull(connection, header[:]); err != nil {
			return 0, err
		}
		sequence++
		_, err := io.CopyN(io.Discard, connection, int64(order.Uint16(header[2:]))*4-4)
		return header[0], err
	}
	write := func(packet []byte) error { _, err := connection.Write(packet); return err }
	for range 2 {
		if _, err := read(); err != nil {
			return err
		}
	}
	selection := make([]byte, 32)
	selection[0] = 31
	order.PutUint32(selection[12:], 2)
	order.PutUint32(selection[16:], 5)
	order.PutUint32(selection[20:], 3)
	if err := write(selection); err != nil {
		return err
	}
	if _, err := read(); err != nil {
		return err
	}
	reply := make([]byte, 36)
	reply[0], reply[1] = 1, 32
	order.PutUint16(reply[2:], sequence)
	order.PutUint32(reply[4:], 1)
	order.PutUint32(reply[8:], 4)
	order.PutUint32(reply[16:], 1)
	order.PutUint32(reply[32:], 8192)
	if err := write(reply); err != nil {
		return err
	}
	if _, err := read(); err != nil {
		return err
	}
	property := make([]byte, 32)
	property[0] = 28
	order.PutUint32(property[8:], 3)
	if err := write(property); err != nil {
		return err
	}
	if _, err := read(); err != nil {
		return err
	}
	reply = make([]byte, 32+4096)
	reply[0], reply[1] = 1, 8
	order.PutUint16(reply[2:], sequence)
	order.PutUint32(reply[4:], 4096/4)
	order.PutUint32(reply[8:], 5)
	order.PutUint32(reply[16:], 4096)
	if err := write(reply); err != nil {
		return err
	}
	if mode == "partial" {
		_, err := io.Copy(io.Discard, connection)
		return err
	}
	if err := write(property); err != nil {
		return err
	}
	if _, err := read(); err != nil {
		return err
	}
	reply = make([]byte, 32)
	reply[0], reply[1] = 1, 8
	order.PutUint16(reply[2:], sequence)
	order.PutUint32(reply[8:], 5)
	if mode == "invalid" {
		reply[1] = 32
		order.PutUint32(reply[8:], 4)
	}
	return write(reply)
}
