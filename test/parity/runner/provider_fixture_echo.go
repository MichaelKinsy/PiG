//go:build parity

package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// echoRequestTrigger is the user prompt that makes a provider fixture answer with a part of the request it received instead of the prompt's
// text: `ECHO_REQUEST` names the whole body, `ECHO_REQUEST /tools` the part an RFC 6901 JSON Pointer names. Each binary then prints what it
// sent, and the scenario's comparator compares the two requests.
const echoRequestTrigger = "ECHO_REQUEST"

// echoRequestPointer reports whether text is the echo trigger and returns its pointer. A pointer holds no white space, so a member whose name
// has a space cannot be named; any other text after the trigger word makes the prompt an ordinary one.
func echoRequestPointer(text string) (string, bool) {
	fields := strings.Fields(text)
	switch {
	case len(fields) == 1 && fields[0] == echoRequestTrigger:
		return "", true
	case len(fields) == 2 && fields[0] == echoRequestTrigger && strings.HasPrefix(fields[1], "/"):
		return fields[1], true
	}
	return "", false
}

// renderRequestPart renders the part of a request body that pointer names, or the word "absent" when the body has no such part. The rendering
// is the JSON text as received with only its spelling made uniform: no white space between tokens and every string in one escape spelling.
// Members stay in the order received, repeated member names stay repeated, and number literals stay as written, because a provider reads
// them in that form; a difference in any of them between two binaries is a difference between their requests.
func renderRequestPart(body []byte, pointer string) (string, error) {
	part, found, err := requestPart(body, pointer)
	if err != nil {
		return "", err
	}
	if !found {
		return "absent", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(part))
	decoder.UseNumber()
	var b bytes.Buffer
	if err := renderJSONValue(decoder, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// requestPart follows an RFC 6901 pointer through body. A repeated member name resolves to its last value, as JSON.parse and encoding/json do.
func requestPart(body []byte, pointer string) (json.RawMessage, bool, error) {
	if !json.Valid(body) {
		return nil, false, fmt.Errorf("the request body is not JSON")
	}
	part := json.RawMessage(body)
	if pointer == "" {
		return part, true, nil
	}
	unescape := strings.NewReplacer("~1", "/", "~0", "~")
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token = unescape.Replace(token)
		var members map[string]json.RawMessage
		var items []json.RawMessage
		switch {
		case json.Unmarshal(part, &members) == nil && members != nil:
			next, ok := members[token]
			if !ok {
				return nil, false, nil
			}
			part = next
		case json.Unmarshal(part, &items) == nil && items != nil:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(items) || strconv.Itoa(index) != token {
				return nil, false, nil
			}
			part = items[index]
		default:
			return nil, false, nil
		}
	}
	return part, true, nil
}

// renderJSONValue writes the decoder's next value without white space, keeping the order of the tokens read.
func renderJSONValue(decoder *json.Decoder, b *bytes.Buffer) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return renderJSONScalar(token, b)
	}
	b.WriteRune(rune(delim))
	for i := 0; decoder.More(); i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		if delim == '{' {
			name, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := renderJSONScalar(name, b); err != nil {
				return err
			}
			b.WriteByte(':')
		}
		if err := renderJSONValue(decoder, b); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	b.WriteRune(rune(closing.(json.Delim)))
	return nil
}

// renderJSONScalar writes a string, number literal, boolean or null. HTML escaping is off so that "<" is written as itself.
func renderJSONScalar(token json.Token, b *bytes.Buffer) error {
	var scalar bytes.Buffer
	encoder := json.NewEncoder(&scalar)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(token); err != nil {
		return err
	}
	b.Write(bytes.TrimSuffix(scalar.Bytes(), []byte("\n")))
	return nil
}
