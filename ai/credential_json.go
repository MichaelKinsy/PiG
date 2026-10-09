package ai

// Ports packages/ai/src/auth/types.ts

import (
	"bytes"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Credential properties are case-sensitive JSON keys. Filter by exact tags before the Go struct decoder can fold opaque provider metadata into a known field. Extra is still collected from the original object.
// An OAuth credential (always, for tokenOnly OAuthCredentials) decodes its required tokens strictly but treats every other key as provider-owned JSON: API-key properties are not projected, and convenience strings project only string values. Extra retains the other shapes.
// An OAuth expires value is removed before typed decoding and returned as its projection and exact value, because Pi stores any JavaScript value there.
func unmarshalCredentialFields(data []byte, target any, tokenOnly bool) (int64, credentialExpiry, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return 0, credentialExpiry{}, err
	}
	var expires int64
	var expiry credentialExpiry
	if tokenOnly || credentialObjectType(object) == CredentialOAuth {
		raw, present := object["expires"]
		delete(object, "expires")
		var err error
		if expires, expiry, err = expiryFromJSON(raw, present); err != nil {
			return 0, credentialExpiry{}, err
		}
		for _, key := range []string{"key", "apiKey", "env"} {
			delete(object, key)
		}
		for _, key := range []string{"projectId", "accountId", "enterpriseUrl", "scope"} {
			if raw, ok := object[key]; ok {
				var value string
				if json.Unmarshal(raw, &value) != nil {
					delete(object, key)
				}
			}
		}
		if tokenOnly {
			delete(object, "type")
		}
	} else if raw, present := object["expires"]; present {
		// API-key metadata keeps a value the integer field cannot hold in Extra.
		if _, exact, err := expiryFromJSON(raw, true); err != nil || exact.set {
			delete(object, "expires")
		}
	}
	typ := reflect.TypeOf(target).Elem()
	fields := make(map[string]json.RawMessage, typ.NumField())
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if value, present := object[name]; present {
			fields[name] = value
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return 0, credentialExpiry{}, err
	}
	return expires, expiry, json.Unmarshal(encoded, target)
}

func credentialObjectType(object map[string]json.RawMessage) CredentialType {
	var kind CredentialType
	if raw, ok := object["type"]; ok {
		_ = json.Unmarshal(raw, &kind)
	}
	return kind
}

// Credential extensions belong to the provider. Retain unknown fields and their UTF-16 string identity through typed projection, OAuth conversion, and persistence.
// A property absent from the omitempty projection (empty, null, or a provider-defined shape) stays in Extra so its presence survives a rewrite. Required OAuth fields always serialize from their typed values.
func credentialExtra(data []byte, known []byte, oauth bool) (map[string]json.RawMessage, error) {
	var all, fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(known, &fields); err != nil {
		return nil, err
	}
	for key := range fields {
		delete(all, key)
	}
	if oauth {
		for _, key := range []string{"access", "refresh", "expires"} {
			delete(all, key)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}
func (c Credential) MarshalJSON() ([]byte, error) {
	data, err := c.marshalFields()
	if err != nil {
		return nil, err
	}
	return c.order.apply(data)
}

func (c Credential) marshalFields() ([]byte, error) {
	type plain Credential
	data, err := json.Marshal(plain(c))
	if err != nil || (len(c.Extra) == 0 && c.Type != CredentialOAuth) {
		return data, err
	}
	fields := maps.Clone(c.Extra)
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(data, &known); err != nil {
		return nil, err
	}
	if c.Type == CredentialOAuth {
		// Pi's OAuth credential shape requires these fields even when empty or expired.
		known["access"], _ = json.Marshal(c.Access)
		known["refresh"], _ = json.Marshal(c.Refresh)
		if value, present := c.expiry.json(c.Expires); present {
			known["expires"] = value
		} else {
			delete(known, "expires")
		}
	}
	maps.Copy(fields, known)
	return json.Marshal(fields)
}
func (c *Credential) UnmarshalJSON(data []byte) error {
	type plain Credential
	var decoded plain
	expires, expiry, err := unmarshalCredentialFields(data, &decoded, false)
	if err != nil {
		return err
	}
	if decoded.Type == CredentialOAuth {
		decoded.Expires, decoded.expiry = expires, expiry
	}
	known, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	decoded.Extra, err = credentialExtra(data, known, decoded.Type == CredentialOAuth)
	if err != nil {
		return err
	}
	value := Credential(decoded)
	value.order = decodedCredentialOrder(data).retainedFor(value.marshalFields)
	*c = value
	return nil
}
func (c *rawCredential) UnmarshalJSON(data []byte) error {
	type plain rawCredential
	var decoded plain
	expires, expiry, err := unmarshalCredentialFields(data, &decoded, false)
	if err != nil {
		return err
	}
	if decoded.Type == CredentialOAuth {
		decoded.Expires, decoded.expiry = expires, expiry
	}
	known, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	decoded.Extra, err = credentialExtra(data, known, decoded.Type == CredentialOAuth)
	if err != nil {
		return err
	}
	// normalize migrates the legacy apiKey property; an empty legacy value is not provider metadata.
	delete(decoded.Extra, "apiKey")
	if len(decoded.Extra) == 0 {
		decoded.Extra = nil
	}
	decoded.order = decodedCredentialOrder(data)
	*c = rawCredential(decoded)
	return nil
}
func (c OAuthCredentials) MarshalJSON() ([]byte, error) {
	data, err := c.marshalFields()
	if err != nil {
		return nil, err
	}
	return c.order.apply(data)
}

func (c OAuthCredentials) marshalFields() ([]byte, error) {
	type plain OAuthCredentials
	data, err := json.Marshal(plain(c))
	if err != nil || (len(c.Extra) == 0 && !c.expiry.current(c.Expires)) {
		return data, err
	}
	fields := maps.Clone(c.Extra)
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(data, &known); err != nil {
		return nil, err
	}
	if value, present := c.expiry.json(c.Expires); present {
		known["expires"] = value
	} else {
		delete(known, "expires")
	}
	maps.Copy(fields, known)
	return json.Marshal(fields)
}
func (c *OAuthCredentials) UnmarshalJSON(data []byte) error {
	type plain OAuthCredentials
	var decoded plain
	expires, expiry, err := unmarshalCredentialFields(data, &decoded, true)
	if err != nil {
		return err
	}
	decoded.Expires, decoded.expiry = expires, expiry
	known, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	decoded.Extra, err = credentialExtra(data, known, true)
	// The credential store owns the discriminator; OAuthCredentials never carries it.
	delete(decoded.Extra, "type")
	if len(decoded.Extra) == 0 {
		decoded.Extra = nil
	}
	if err != nil {
		return err
	}
	value := OAuthCredentials(decoded)
	// The order keeps the position of the discriminator the store adds back (credentialFromOAuth). Pi stores { ...credential, type: "oauth" } (packages/coding-agent/src/core/provider-composer.ts:367 adaptOAuth): a flow's own type keeps its place, and apply writes an added one after the flow's properties.
	order := decodedCredentialOrder(data)
	value.order = order.retainedFor(value.marshalFields, value.storeFields)
	*c = value
	return nil
}

// credentialFromOAuth preserves provider-owned fields while promoting fields understood by the credential store into its typed representation.
func credentialFromOAuth(value OAuthCredentials) (Credential, error) {
	data, err := value.storeFields()
	if err != nil {
		return Credential{}, err
	}
	if data, err = value.order.apply(data); err != nil {
		return Credential{}, err
	}
	// Decode as an OAuth credential so provider-owned fields that share an API-key or convenience name keep their JSON shape.
	var credential Credential
	if err := json.Unmarshal(data, &credential); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

// storeFields is the stored form of the credential without its decoded order: its properties and the oauth discriminator.
func (c OAuthCredentials) storeFields() ([]byte, error) {
	data, err := c.marshalFields()
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	fields["type"] = json.RawMessage(`"oauth"`)
	return json.Marshal(fields)
}

func cloneCredentialExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if extra == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(extra))
	for key, value := range extra {
		out[key] = bytes.Clone(value)
	}
	return out
}

// credentialKeyOrder is the property order of a credential decoded from JSON: its top-level keys and the keys of its env object. Pi stores the object a login or refresh returned and writes it with JSON.stringify, which keeps that order (packages/coding-agent/src/core/auth-storage.ts:465-467), so a decoded credential writes its properties back in their decoded order. A credential built in Go has no order and keeps the codec's order. A property added after decoding follows the decoded ones, as a spread does.
type credentialKeyOrder struct {
	keys []string
	env  []string
}

// decodedCredentialOrder reads the property order of a credential object and its env object.
func decodedCredentialOrder(data []byte) credentialKeyOrder {
	keys, err := authStorageObjectKeys(data)
	if err != nil {
		return credentialKeyOrder{}
	}
	order := credentialKeyOrder{keys: keys}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) == nil {
		if env, ok := object["env"]; ok && len(bytes.TrimSpace(env)) > 0 && bytes.TrimSpace(env)[0] == '{' {
			order.env, _ = authStorageObjectKeys(env)
		}
	}
	return order
}

// retainedFor drops an order that matches every default encoding, so a credential whose properties already follow the codec's order carries none.
func (o credentialKeyOrder) retainedFor(defaults ...func() ([]byte, error)) credentialKeyOrder {
	for _, encode := range defaults {
		data, err := encode()
		if err != nil {
			return o
		}
		ordered, err := o.apply(data)
		if err != nil || !bytes.Equal(ordered, data) {
			return o
		}
	}
	return credentialKeyOrder{}
}

// renamed is the order with the property from renamed to, unless to is already present.
func (o credentialKeyOrder) renamed(from, to string) credentialKeyOrder {
	if slices.Contains(o.keys, to) {
		return o
	}
	if index := slices.Index(o.keys, from); index >= 0 {
		o.keys = slices.Clone(o.keys)
		o.keys[index] = to
	}
	return o
}

// apply writes the JSON object data with the decoded properties first, in their decoded order, and the others after them in data's order.
func (o credentialKeyOrder) apply(data []byte) ([]byte, error) {
	if o.keys == nil && o.env == nil {
		return data, nil
	}
	out, err := reorderJSONObject(data, o.keys)
	if err != nil || o.env == nil {
		return out, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(out, &object); err != nil {
		return nil, err
	}
	env, ok := object["env"]
	if !ok || len(bytes.TrimSpace(env)) == 0 || bytes.TrimSpace(env)[0] != '{' {
		return out, nil
	}
	if object["env"], err = reorderJSONObject(env, o.env); err != nil {
		return nil, err
	}
	keys, err := authStorageObjectKeys(out)
	if err != nil {
		return nil, err
	}
	return writeOrderedJSONObject(keys, object)
}

func reorderJSONObject(data []byte, first []string) ([]byte, error) {
	present, err := authStorageObjectKeys(data)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(present))
	for _, key := range first {
		if _, ok := object[key]; ok && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	for _, key := range present {
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return writeOrderedJSONObject(javascriptObjectKeyOrder(keys), object)
}

// writeOrderedJSONObject encodes each key with the shared codec, which keeps a lone UTF-16 surrogate distinct from U+FFFD.
func writeOrderedJSONObject(keys []string, object map[string]json.RawMessage) ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(object[key])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
