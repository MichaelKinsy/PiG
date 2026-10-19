package sdk

// Ports packages/ai/src/auth/types.ts (Credential) for provider objects' auth methods.

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Credential is Pi's Credential: the object a provider object's login or refresh returns and auth.json stores. Pi writes that object with JSON.stringify (packages/coding-agent/src/core/auth-storage.ts:465-467), so its keys, and the keys of every object inside it, keep the order the provider gave them. A decoded Credential reads nested objects as *OrderedObject for the same reason. A nil *Credential is an absent credential: Get finds nothing and Keys is empty.
type Credential struct {
	object OrderedObject
}

// NewCredential builds a credential from alternating string keys and values, in that order. A nested *OrderedObject keeps its own order. It panics on a key that is not a string or a key without a value.
func NewCredential(pairs ...any) *Credential {
	return &Credential{object: *NewOrderedObject(pairs...)}
}

// Get returns the value of key.
func (c *Credential) Get(key string) (any, bool) {
	if c == nil {
		return nil, false
	}
	return c.object.Get(key)
}

// String returns the value of key when it is a string.
func (c *Credential) String(key string) string {
	value, _ := c.Get(key)
	text, _ := value.(string)
	return text
}

// Keys returns the keys in order.
func (c *Credential) Keys() []string {
	if c == nil {
		return []string{}
	}
	return c.object.Keys()
}

// Set sets key to value. A new key goes last and an existing key keeps its position, as assigning a JavaScript object's property does. Set needs a credential: on an absent (nil) one it panics, as assigning to a nil map does; Clone or NewCredential gives one.
func (c *Credential) Set(key string, value any) {
	c.object.set(key, value)
}

// Clone returns a copy with the same keys in the same order, as { ...credential } does. Nested values are shared. Cloning an absent credential gives an empty one, as { ...undefined } gives {}, so Clone then Set works on any credential a provider receives.
func (c *Credential) Clone() *Credential {
	if c == nil {
		return &Credential{}
	}
	clone := &Credential{object: OrderedObject{keys: slices.Clone(c.object.keys), values: make(map[string]any, len(c.object.values))}}
	maps.Copy(clone.object.values, c.object.values)
	return clone
}

// MarshalJSON writes the credential with its keys in order.
func (c *Credential) MarshalJSON() ([]byte, error) {
	return c.object.MarshalJSON()
}

// UnmarshalJSON reads a credential object, keeping the order of its keys and of the keys of every object inside it. A repeated key keeps the position of its first occurrence and the value of its last, as JSON.parse does.
func (c *Credential) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	value, err := decodeOrderedValue(decoder)
	if err != nil {
		return err
	}
	if _, err := decoder.Token(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	switch object := value.(type) {
	case nil:
		*c = Credential{}
	case *OrderedObject:
		*c = Credential{object: *object}
	default:
		return errors.New("credential is not a JSON object")
	}
	return nil
}

// decodeOrderedValue reads one JSON value, with every object in it as an *OrderedObject.
func decodeOrderedValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := &OrderedObject{values: map[string]any{}}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid JSON object key")
			}
			value, err := decodeOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			object.set(key, value)
		}
		_, err := decoder.Token()
		return object, err
	case '[':
		values := []any{}
		for decoder.More() {
			value, err := decodeOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err := decoder.Token()
		return values, err
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}
