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
	"math"
	"math/bits"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type x11Display struct {
	host   string
	number int32
	// screen is the requested screen; xcb_connect hands it to Pi unchanged, and a negative one selects the first screen (linux-platform-x11.c:188).
	screen int32
	local  bool
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
	number, rest, ok := xcbDisplayNumber(tail)
	if !ok {
		return x11Display{}, fmt.Errorf("invalid X11 display number %q", tail)
	}
	var screen int32
	if rest != "" {
		if rest[0] != '.' {
			return x11Display{}, fmt.Errorf("invalid X11 display number %q", tail)
		}
		screenText := rest[1:]
		if screen, rest, ok = xcbDisplayNumber(screenText); !ok || rest != "" {
			return x11Display{}, fmt.Errorf("invalid X11 screen %q", screenText)
		}
	}
	return x11Display{host: host, number: number, screen: screen, local: local}, nil
}

// xcbDisplayNumber reads a display or screen number as Pi's xcb_connect (linux-platform-x11.c:185) does: libxcb 1.15 xcb_util.c:176 and :186 parse it
// with C strtoul(text, &end, 10) and store the result in an int. strtoul skips leading white space, takes one optional sign, negates the value
// modulo 2^64 after a minus, and saturates at ULONG_MAX; the int keeps the low 32 bits. ok is false when no digit follows (strtoul converts
// nothing), and rest is what follows the digits.
func xcbDisplayNumber(text string) (value int32, rest string, ok bool) {
	i := 0
	for i < len(text) && (text[i] == ' ' || '\t' <= text[i] && text[i] <= '\r') {
		i++
	}
	negative := false
	if i < len(text) && (text[i] == '+' || text[i] == '-') {
		negative = text[i] == '-'
		i++
	}
	start := i
	var parsed uint64
	saturated := false
	for ; i < len(text) && '0' <= text[i] && text[i] <= '9'; i++ {
		high, low := bits.Mul64(parsed, 10)
		sum, carry := bits.Add64(low, uint64(text[i]-'0'), 0)
		saturated = saturated || high != 0 || carry != 0
		parsed = sum
	}
	if i == start {
		return 0, text, false
	}
	switch {
	case saturated:
		parsed = math.MaxUint64
	case negative:
		parsed = -parsed
	}
	return int32(uint32(parsed)), text[i:], true
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
	displayNumber := strconv.Itoa(int(display.number))
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
		if matches && (len(number) == 0 || string(number) == displayNumber) && string(protocol) == "MIT-MAGIC-COOKIE-1" {
			return protocol, cookie
		}
	}
}

func dialX11(ctx context.Context, display x11Display) (net.Conn, error) {
	var dialer net.Dialer
	if display.local {
		path := "/tmp/.X11-unix/X" + strconv.Itoa(int(display.number))
		connection, err := dialer.DialContext(ctx, "unix", "\x00"+path)
		if err == nil {
			return connection, nil
		}
		return dialer.DialContext(ctx, "unix", path)
	}
	// xcb_util.c:244 `unsigned short port = X_TCP_PORT + display`: the port is the sum's low 16 bits.
	port := uint16(6000 + display.number)
	return dialer.DialContext(ctx, "tcp", net.JoinHostPort(display.host, strconv.Itoa(int(port))))
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
	// xcb_connect fails a screen at or past the server's screen count (xcb_util.c:547); linux-platform-x11.c:188 then steps past screen_number
	// screens, so a negative one selects the first.
	if mask == 0 || int(display.screen) >= int(body[20]) {
		return 0, 0, fmt.Errorf("X11 display has no requested screen")
	}
	selected := max(int(display.screen), 0)
	position := 32 + (int(x11Order.Uint16(body[16:]))+3)&^3 + int(body[21])*8
	for screen := 0; screen <= selected; screen++ {
		if position > len(body)-40 {
			return 0, 0, fmt.Errorf("truncated X11 screen")
		}
		if screen == selected {
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
