//go:build linux

package nativeplatform

// Ports packages/tui/src/native-platform.ts

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"time"
)

// Limits and target order follow packages/tui/native/linux/src/linux-platform-x11.c.
const maxClipboardBytes = 50 * 1024 * 1024
const clipboardTransferTimeout = 2 * time.Second

var clipboardTextTypes = []string{"text/plain;charset=utf-8", "text/plain;charset=UTF-8", "UTF8_STRING", "text/plain", "STRING"}
var clipboardImageTypes = []string{"image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp", "image/tiff"}
var errX11Clipboard = errors.New("Could not read X11 clipboard")

var x11Order = binary.LittleEndian

type x11Property struct {
	data   []byte
	items  uint32
	format byte
	typeID uint32
}

func (p *x11Property) appendProperty(chunk x11Property) bool {
	if (chunk.format != 8 && chunk.format != 16 && chunk.format != 32) || chunk.typeID == 0 ||
		uint64(chunk.items)*uint64(chunk.format/8) != uint64(len(chunk.data)) ||
		(p.typeID != 0 && (p.typeID != chunk.typeID || p.format != chunk.format)) ||
		chunk.items > math.MaxUint32-p.items || len(chunk.data) > maxClipboardBytes-len(p.data) {
		return false
	}
	if p.data == nil {
		p.data = make([]byte, 0, len(chunk.data))
	}
	p.data = append(p.data, chunk.data...)
	p.typeID, p.format = chunk.typeID, chunk.format
	p.items += chunk.items
	return true
}

type x11Clipboard struct {
	connection                                 net.Conn
	window, clipboard, property, targets, incr uint32
	sequence                                   uint16
	events                                     [][]byte
	deadline                                   time.Time
	stopClose                                  func()
}

func x11Request(opcode, detail byte, size int) []byte {
	packet := make([]byte, size)
	packet[0], packet[1] = opcode, detail
	x11Order.PutUint16(packet[2:], uint16(size/4))
	return packet
}

func (c *x11Clipboard) send(packet []byte) (uint16, error) {
	c.sequence++
	for len(packet) > 0 {
		n, err := c.connection.Write(packet)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return c.sequence, nil
}

func (c *x11Clipboard) packet() ([]byte, error) {
	packet := make([]byte, 32)
	if _, err := io.ReadFull(c.connection, packet); err != nil {
		return nil, err
	}
	if packet[0] == 1 || packet[0]&0x7f == 35 {
		length := uint64(x11Order.Uint32(packet[4:])) * 4
		if length > maxClipboardBytes {
			return nil, errX11Clipboard
		}
		body := make([]byte, int(length))
		if _, err := io.ReadFull(c.connection, body); err != nil {
			return nil, err
		}
		packet = append(packet, body...)
	}
	return packet, nil
}

func (c *x11Clipboard) reply(sequence uint16) ([]byte, error) {
	for time.Now().Before(c.deadline) {
		packet, err := c.packet()
		if err != nil {
			return nil, err
		}
		if packet[0] == 1 && x11Order.Uint16(packet[2:]) == sequence {
			return packet, nil
		}
		if packet[0] == 0 && x11Order.Uint16(packet[2:]) == sequence {
			return nil, errX11Clipboard
		}
		if packet[0] != 1 {
			c.events = append(c.events, packet)
		}
	}
	return nil, errX11Clipboard
}

func (c *x11Clipboard) event(eventType byte) ([]byte, error) {
	for time.Now().Before(c.deadline) {
		var packet []byte
		if len(c.events) > 0 {
			packet = c.events[0]
			c.events[0] = nil
			c.events = c.events[1:]
		} else {
			var err error
			packet, err = c.packet()
			if err != nil {
				return nil, err
			}
		}
		if packet[0]&0x7f == eventType {
			return packet, nil
		}
	}
	return nil, errX11Clipboard
}

func (c *x11Clipboard) internAtom(name string) (uint32, error) {
	packet := x11Request(16, 0, 8+(len(name)+3)&^3)
	x11Order.PutUint16(packet[4:], uint16(len(name)))
	copy(packet[8:], name)
	sequence, err := c.send(packet)
	if err != nil {
		return 0, err
	}
	reply, err := c.reply(sequence)
	if err != nil {
		return 0, err
	}
	atom := x11Order.Uint32(reply[8:])
	if atom == 0 {
		return 0, errX11Clipboard
	}
	return atom, nil
}

func (c *x11Clipboard) deleteProperty() error {
	packet := x11Request(19, 0, 12)
	x11Order.PutUint32(packet[4:], c.window)
	x11Order.PutUint32(packet[8:], c.property)
	_, err := c.send(packet)
	return err
}

func (c *x11Clipboard) readProperty(remove bool) (x11Property, error) {
	var deletion byte
	if remove {
		deletion = 1
	}
	packet := x11Request(20, deletion, 24)
	x11Order.PutUint32(packet[4:], c.window)
	x11Order.PutUint32(packet[8:], c.property)
	x11Order.PutUint32(packet[20:], maxClipboardBytes/4)
	sequence, err := c.send(packet)
	if err != nil {
		return x11Property{}, err
	}
	reply, err := c.reply(sequence)
	if err != nil {
		return x11Property{}, err
	}
	format := reply[1]
	typeID, after, items := x11Order.Uint32(reply[8:]), x11Order.Uint32(reply[12:]), x11Order.Uint32(reply[16:])
	length := uint64(items) * uint64(format/8)
	if after != 0 || typeID == 0 || (format != 8 && format != 16 && format != 32) || length > maxClipboardBytes || length > uint64(len(reply)-32) {
		return x11Property{}, errX11Clipboard
	}
	data := make([]byte, int(length))
	copy(data, reply[32:])
	return x11Property{data: data, items: items, format: format, typeID: typeID}, nil
}

func (c *x11Clipboard) requestSelection(target uint32) (x11Property, error) {
	if err := c.deleteProperty(); err != nil {
		return x11Property{}, err
	}
	packet := x11Request(24, 0, 24)
	x11Order.PutUint32(packet[4:], c.window)
	x11Order.PutUint32(packet[8:], c.clipboard)
	x11Order.PutUint32(packet[12:], target)
	x11Order.PutUint32(packet[16:], c.property)
	if _, err := c.send(packet); err != nil {
		return x11Property{}, err
	}
	for {
		event, err := c.event(31)
		if err != nil {
			return x11Property{}, err
		}
		if x11Order.Uint32(event[12:]) != c.clipboard || x11Order.Uint32(event[16:]) != target {
			continue
		}
		if x11Order.Uint32(event[20:]) == 0 {
			return x11Property{}, nil
		}
		break
	}
	result, err := c.readProperty(false)
	if err != nil {
		return x11Property{}, err
	}
	if result.typeID != c.incr {
		return result, nil
	}
	result = x11Property{}
	if err = c.deleteProperty(); err != nil {
		return x11Property{}, err
	}
	for {
		event, err := c.event(28)
		if err != nil {
			return x11Property{}, err
		}
		if x11Order.Uint32(event[8:]) != c.property || event[16] != 0 {
			continue
		}
		chunk, err := c.readProperty(true)
		if err != nil {
			return x11Property{}, err
		}
		if !result.appendProperty(chunk) {
			return x11Property{}, errX11Clipboard
		}
		if chunk.items == 0 {
			return result, nil
		}
	}
}

func (c *x11Clipboard) preferredTarget(image bool) (uint32, error) {
	types := clipboardTextTypes
	if image {
		types = clipboardImageTypes
	}
	wanted := make([]uint32, len(types))
	for i, name := range types {
		atom, err := c.internAtom(name)
		if err != nil {
			return 0, err
		}
		wanted[i] = atom
	}
	targets, err := c.requestSelection(c.targets)
	if err != nil {
		return 0, err
	}
	valid := targets.typeID == 4 && targets.format == 32 && uint64(targets.items)*4 == uint64(len(targets.data))
	if valid {
		for _, target := range wanted {
			for index := 0; index < len(targets.data); index += 4 {
				if x11Order.Uint32(targets.data[index:]) == target {
					return target, nil
				}
			}
		}
	}
	if !image && targets.typeID == 0 {
		return c.internAtom("UTF8_STRING")
	}
	if valid || targets.typeID == 0 {
		return 0, nil
	}
	return 0, errX11Clipboard
}

func (c *x11Clipboard) readSelection(image bool) (x11Property, error) {
	target, err := c.preferredTarget(image)
	if err != nil || target == 0 {
		return x11Property{}, err
	}
	return c.requestSelection(target)
}
