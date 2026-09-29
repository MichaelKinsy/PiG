//go:build linux

package nativeplatform

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestNativeX11EmptySelectionWireBoundary(t *testing.T) {
	for _, image := range []bool{false, true} {
		t.Run(strconv.FormatBool(image), func(t *testing.T) {
			server, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			served := make(chan error, 1)
			go func() {
				connection, err := server.Accept()
				if err != nil {
					served <- err
					return
				}
				defer func() { _ = connection.Close() }()
				served <- serveEmptyX11(connection)
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			port := server.Addr().(*net.TCPAddr).Port
			clipboard, err := openX11Clipboard(ctx, "127.0.0.1:"+strconv.Itoa(port-6000))
			if err != nil {
				t.Fatal(err)
			}
			if clipboard.window != 0x200001 {
				t.Errorf("resource id=%x", clipboard.window)
			}
			result, err := clipboard.readSelection(image)
			clipboard.close()
			if err != nil || result.data != nil {
				t.Fatalf("empty result=%+v err=%v", result, err)
			}
			if err := <-served; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A protocol peer independently supplies the standard setup/reply layouts and refuses every selection, exercising the real socket path without requiring a display server.
func serveEmptyX11(connection net.Conn) error {
	x11Order := binary.LittleEndian // The peer's encoding does not use the client's byte-order variable.
	var setup [12]byte
	if _, err := io.ReadFull(connection, setup[:]); err != nil {
		return err
	}
	length := ((int(x11Order.Uint16(setup[6:])) + 3) &^ 3) + ((int(x11Order.Uint16(setup[8:])) + 3) &^ 3)
	if _, err := io.CopyN(io.Discard, connection, int64(length)); err != nil {
		return err
	}
	response := make([]byte, 8+32+40)
	response[0] = 1
	x11Order.PutUint16(response[2:], 11)
	x11Order.PutUint16(response[6:], uint16((len(response)-8)/4))
	x11Order.PutUint32(response[8+4:], 0x200000)
	x11Order.PutUint32(response[8+8:], 0x1fffff)
	response[8+20] = 1
	x11Order.PutUint32(response[8+32:], 0x100)
	if _, err := connection.Write(response); err != nil {
		return err
	}
	atoms := map[string]uint32{}
	var sequence uint16
	for {
		var header [4]byte
		if _, err := io.ReadFull(connection, header[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		sequence++
		body := make([]byte, int(x11Order.Uint16(header[2:]))*4-4)
		if _, err := io.ReadFull(connection, body); err != nil {
			return err
		}
		switch header[0] {
		case 16:
			name := string(body[4 : 4+int(x11Order.Uint16(body))])
			atom := atoms[name]
			if atom == 0 {
				atom = uint32(len(atoms)) + 100
				atoms[name] = atom
			}
			reply := make([]byte, 32)
			reply[0] = 1
			x11Order.PutUint16(reply[2:], sequence)
			x11Order.PutUint32(reply[8:], atom)
			if _, err := connection.Write(reply); err != nil {
				return err
			}
		case 24:
			event := make([]byte, 32)
			event[0] = 31
			x11Order.PutUint16(event[2:], sequence)
			copy(event[8:20], body[:12])
			if _, err := connection.Write(event); err != nil {
				return err
			}
		}
	}
}
