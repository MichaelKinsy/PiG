package experimental

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// RadiusRelayHostSubprotocol is the upstream multiplexed host WebSocket protocol.
const RadiusRelayHostSubprotocol = "pi-session-relay.host.v1"

// RadiusRelayClientSubprotocol is the upstream raw client WebSocket protocol.
const RadiusRelayClientSubprotocol = "pi-session-relay.client.v1"

const relayDataHeaderBytes = 18

var connectionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// RelayDataFrame contains a validated lowercase UUIDv4 and an independently owned payload.
type RelayDataFrame struct {
	ConnectionID string
	Payload      []byte
}

// EncodeRelayDataFrame copies payload into the Radius version/type/UUID envelope.
func EncodeRelayDataFrame(connectionID string, payload []byte) ([]byte, error) {
	if !connectionIDPattern.MatchString(connectionID) {
		return nil, errors.New("Invalid Radius relay connection ID")
	}
	frame := make([]byte, relayDataHeaderBytes+len(payload))
	frame[0], frame[1] = 1, 1
	id, err := hex.DecodeString(strings.ReplaceAll(connectionID, "-", ""))
	if err != nil {
		return nil, err
	}
	copy(frame[2:], id)
	copy(frame[relayDataHeaderBytes:], payload)
	return frame, nil
}

// ParseRelayDataFrame validates the envelope and returns a copied payload, or false for malformed frames.
func ParseRelayDataFrame(frame []byte) (RelayDataFrame, bool) {
	if len(frame) < relayDataHeaderBytes || frame[0] != 1 || frame[1] != 1 {
		return RelayDataFrame{}, false
	}
	id := fmt.Sprintf("%x-%x-%x-%x-%x", frame[2:6], frame[6:8], frame[8:10], frame[10:12], frame[12:18])
	if !connectionIDPattern.MatchString(id) {
		return RelayDataFrame{}, false
	}
	return RelayDataFrame{ConnectionID: id, Payload: bytes.Clone(frame[relayDataHeaderBytes:])}, true
}

type hostControlMessage struct {
	Version      int    `json:"version"`
	Type         string `json:"type"`
	ConnectionID string `json:"connection_id,omitempty"`
	Code         *int   `json:"code,omitempty"`
}

func parseHostControlMessage(value []byte) (hostControlMessage, error) {
	invalid := errors.New("Invalid Radius relay control message")
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil || fields == nil {
		return hostControlMessage{}, invalid
	}
	var version float64
	if json.Unmarshal(fields["version"], &version) != nil || version != 1 {
		return hostControlMessage{}, errors.New("Unsupported Radius relay control version")
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil {
		return hostControlMessage{}, invalid
	}
	message := hostControlMessage{Version: 1, Type: kind}
	if kind == "ping" || kind == "pong" {
		return message, nil
	}
	if kind != "connection_open" && kind != "connection_close" {
		return hostControlMessage{}, invalid
	}
	if json.Unmarshal(fields["connection_id"], &message.ConnectionID) != nil || !connectionIDPattern.MatchString(message.ConnectionID) {
		return hostControlMessage{}, invalid
	}
	if raw, exists := fields["code"]; exists {
		var number float64
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &number) != nil || number < 1000 || number > 4999 || number != float64(int(number)) {
			return hostControlMessage{}, invalid
		}
		code := int(number)
		message.Code = &code
	}
	return message, nil
}

func relayWebSocketURL(gateway, serverID string) (string, error) {
	base, err := url.Parse(gateway)
	if err != nil {
		return "", err
	}
	if base.Host == "" {
		return "", fmt.Errorf("Invalid URL: %s", gateway)
	}
	switch base.Scheme {
	case "https":
		base.Scheme = "wss"
	case "http":
		base.Scheme = "ws"
	default:
		return "", fmt.Errorf("Unsupported Radius gateway protocol: %s:", base.Scheme)
	}
	endpoint, err := base.Parse("/v1/session-relays/" + serverID + "/connect")
	if err != nil {
		return "", err
	}
	return endpoint.String(), nil
}
