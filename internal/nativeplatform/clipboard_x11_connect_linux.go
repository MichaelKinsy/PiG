//go:build linux

package nativeplatform

// Ports packages/tui/src/native-platform.ts.
// X11 connection and transfer ownership follow packages/tui/native/linux/src/linux-platform-x11.c:open_clipboard and close_clipboard.

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type x11Display struct {
	host, number string
	screen       int
	local        bool
}

func parseX11Display(display string) (x11Display, error) {
	separator := strings.LastIndexByte(display, ':')
	if separator < 0 {
		return x11Display{}, fmt.Errorf("invalid X11 display %q", display)
	}
	host, tail := display[:separator], display[separator+1:]
	if before, after, found := strings.Cut(host, "/"); found {
		switch {
		case before == "tcp":
			host = after
		case before == "unix" || after == "unix":
			host = "unix"
		default:
			return x11Display{}, fmt.Errorf("unsupported X11 transport %q", host)
		}
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	local := host == "" || host == "unix"
	number, screenText, hasScreen := strings.Cut(tail, ".")
	n, err := strconv.Atoi(number)
	// Local display numbers name Unix sockets; only TCP targets use a 16-bit port with the 6000 offset.
	if err != nil || n < 0 || (!local && n > 65535-6000) {
		return x11Display{}, fmt.Errorf("invalid X11 display number %q", number)
	}
	screen := 0
	if hasScreen {
		screen, err = strconv.Atoi(screenText)
		if err != nil || screen < 0 {
			return x11Display{}, fmt.Errorf("invalid X11 screen %q", screenText)
		}
	}
	return x11Display{host: host, number: strconv.Itoa(n), screen: screen, local: local}, nil
}

func x11Authority(display x11Display, remote net.Addr) (name, data []byte) {
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil
		}
		path = filepath.Join(home, ".Xauthority")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	hostname, _ := os.Hostname()
	var peer net.IP
	if address, ok := remote.(*net.TCPAddr); ok {
		peer = address.IP
	}
	local := display.local || peer.IsLoopback()
	readField := func() ([]byte, error) {
		var header [2]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return nil, err
		}
		value := make([]byte, int(binary.BigEndian.Uint16(header[:])))
		_, err := io.ReadFull(reader, value)
		return value, err
	}
	for {
		var header [2]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return nil, nil
		}
		family := binary.BigEndian.Uint16(header[:])
		address, err := readField()
		if err != nil {
			return nil, nil
		}
		number, err := readField()
		if err != nil {
			return nil, nil
		}
		protocol, err := readField()
		if err != nil {
			return nil, nil
		}
		cookie, err := readField()
		if err != nil {
			return nil, nil
		}
		matches := family == 65535 || local && family == 256 && string(address) == hostname
		if !display.local && (family == 0 || family == 6) {
			matches = net.IP(address).Equal(peer)
		}
		if matches && (len(number) == 0 || string(number) == display.number) && string(protocol) == "MIT-MAGIC-COOKIE-1" {
			return protocol, cookie
		}
	}
}

func dialX11(ctx context.Context, display x11Display) (net.Conn, error) {
	var dialer net.Dialer
	if display.local {
		path := "/tmp/.X11-unix/X" + display.number
		connection, err := dialer.DialContext(ctx, "unix", "\x00"+path)
		if err == nil {
			return connection, nil
		}
		return dialer.DialContext(ctx, "unix", path)
	}
	number, _ := strconv.Atoi(display.number)
	return dialer.DialContext(ctx, "tcp", net.JoinHostPort(display.host, strconv.Itoa(6000+number)))
}

func setupX11(connection net.Conn, display x11Display) (window, root uint32, err error) {
	name, cookie := x11Authority(display, connection.RemoteAddr())
	nameSize, cookieSize := (len(name)+3)&^3, (len(cookie)+3)&^3
	request := make([]byte, 12+nameSize+cookieSize)
	request[0] = 'l'
	x11Order.PutUint16(request[2:], 11)
	x11Order.PutUint16(request[6:], uint16(len(name)))
	x11Order.PutUint16(request[8:], uint16(len(cookie)))
	copy(request[12:], name)
	copy(request[12+nameSize:], cookie)
	for len(request) > 0 {
		n, writeErr := connection.Write(request)
		if writeErr != nil {
			return 0, 0, writeErr
		}
		if n == 0 {
			return 0, 0, io.ErrShortWrite
		}
		request = request[n:]
	}
	var prefix [8]byte
	if _, err = io.ReadFull(connection, prefix[:]); err != nil {
		return 0, 0, err
	}
	body := make([]byte, int(x11Order.Uint16(prefix[6:]))*4)
	if _, err = io.ReadFull(connection, body); err != nil {
		return 0, 0, err
	}
	if prefix[0] != 1 || len(body) < 32 {
		return 0, 0, fmt.Errorf("X11 connection setup failed")
	}
	base, mask := x11Order.Uint32(body[4:]), x11Order.Uint32(body[8:])
	if mask == 0 || display.screen >= int(body[20]) {
		return 0, 0, fmt.Errorf("X11 display has no requested screen")
	}
	position := 32 + (int(x11Order.Uint16(body[16:]))+3)&^3 + int(body[21])*8
	for screen := 0; screen <= display.screen; screen++ {
		if position > len(body)-40 {
			return 0, 0, fmt.Errorf("truncated X11 screen")
		}
		if screen == display.screen {
			return base | (mask & -mask), x11Order.Uint32(body[position:]), nil
		}
		depths := int(body[position+39])
		position += 40
		for range depths {
			if position > len(body)-8 {
				return 0, 0, fmt.Errorf("truncated X11 depth")
			}
			position += 8 + int(x11Order.Uint16(body[position+2:]))*24
		}
	}
	return 0, 0, fmt.Errorf("X11 screen unavailable")
}

func openX11Clipboard(ctx context.Context, displayText string) (*x11Clipboard, error) {
	deadline := time.Now().Add(clipboardTransferTimeout)
	display, err := parseX11Display(displayText)
	if err != nil {
		return nil, err
	}
	connection, err := dialX11(ctx, display)
	if err != nil {
		return nil, err
	}
	closed := make(chan struct{})
	cancelClose := context.AfterFunc(ctx, func() { _ = connection.Close(); close(closed) })
	stopClose := func() {
		if !cancelClose() {
			<-closed
		}
	}
	window, root, err := setupX11(connection, display)
	if err != nil {
		_ = connection.Close()
		stopClose()
		return nil, err
	}
	if err = connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		stopClose()
		return nil, err
	}
	c := &x11Clipboard{connection: connection, window: window, deadline: deadline, stopClose: stopClose}
	packet := x11Request(1, 0, 36)
	x11Order.PutUint32(packet[4:], window)
	x11Order.PutUint32(packet[8:], root)
	x11Order.PutUint16(packet[16:], 1)
	x11Order.PutUint16(packet[18:], 1)
	x11Order.PutUint16(packet[22:], 1)
	x11Order.PutUint32(packet[28:], 1<<11)
	x11Order.PutUint32(packet[32:], 1<<22)
	if _, err = c.send(packet); err != nil {
		c.close()
		return nil, err
	}
	for _, atom := range []struct {
		name   string
		target *uint32
	}{{"CLIPBOARD", &c.clipboard}, {"PI_CLIPBOARD", &c.property}, {"TARGETS", &c.targets}, {"INCR", &c.incr}} {
		*atom.target, err = c.internAtom(atom.name)
		if err != nil {
			c.close()
			return nil, err
		}
	}
	return c, nil
}

func (c *x11Clipboard) close() {
	_ = c.connection.Close()
	c.stopClose()
}
