package ai

// Ports packages/ai/src/auth/types.ts (OAuthCredentials.expires).

import (
	"bytes"
	"math"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// nowMillis is Date.now() for OAuth expiry decisions.
var nowMillis = func() int64 { return time.Now().UnixMilli() }

// credentialExpiry retains an OAuth expires value that the int64 Expires projection cannot represent: an absent value, a fraction, an out-of-range number, or a non-number.
// It applies only while Expires still equals projection; a caller that assigns Expires replaces it.
// value is the JSON.stringify form and is never mutated in place; a nil value is undefined.
type credentialExpiry struct {
	value      json.RawMessage
	number     float64
	projection int64
	set        bool
}

// expiryFromJSON returns the int64 projection and the retained exact value for a decoded OAuth expires property.
func expiryFromJSON(raw json.RawMessage, present bool) (int64, credentialExpiry, error) {
	if !present {
		return 0, credentialExpiry{number: math.NaN(), set: true}, nil
	}
	number := jsnumber.FromJSON(raw)
	if token := bytes.TrimSpace(raw); len(token) > 0 && (token[0] == '-' || token[0] >= '0' && token[0] <= '9') {
		if integer, ok := exactInt64(number); ok {
			return integer, credentialExpiry{}, nil
		}
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return 0, credentialExpiry{}, err
	}
	projection := expiryProjection(number)
	return projection, credentialExpiry{value: canonical, number: number, projection: projection, set: true}, nil
}

// expiryFromNumber returns the representation of an in-memory JavaScript number.
func expiryFromNumber(value float64) (int64, credentialExpiry) {
	if integer, ok := exactInt64(value); ok {
		return integer, credentialExpiry{}
	}
	projection := expiryProjection(value)
	return projection, credentialExpiry{value: jsnumber.JSON(value), number: value, projection: projection, set: true}
}

// exactInt64 accepts only safe integers: JSON.stringify writes a larger integer with Number::toString digits (2**60 is 1152921504606847000), which strconv.FormatInt of the projection would not reproduce.
func exactInt64(value float64) (int64, bool) {
	if value != math.Trunc(value) || value < -(1<<53) || value > 1<<53 {
		return 0, false
	}
	return int64(value), true
}

func expiryProjection(value float64) int64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < -(1<<63) || value >= 1<<63 {
		return 0
	}
	return int64(math.Trunc(value))
}

func (e credentialExpiry) current(expires int64) bool { return e.set && e.projection == expires }

// millis returns ToNumber(expires), the operand of Pi's relational expiry checks.
func (e credentialExpiry) millis(expires int64) float64 {
	if e.current(expires) {
		return e.number
	}
	return float64(expires)
}

// json returns the serialized expires property and whether it is present.
func (e credentialExpiry) json(expires int64) (json.RawMessage, bool) {
	if e.current(expires) {
		return e.value, e.value != nil
	}
	return json.RawMessage(strconv.FormatInt(expires, 10)), true
}

// ExpiresMillis returns ToNumber of the credential's expires value, the operand Pi compares with Date.now(). An absent value is NaN, so both orderings compare false.
func (c OAuthCredentials) ExpiresMillis() float64 { return c.expiry.millis(c.Expires) }

// HasExpires reports whether the credential carries an expires property.
func (c OAuthCredentials) HasExpires() bool {
	_, present := c.expiry.json(c.Expires)
	return present
}

// SetExpiresMillis stores a JavaScript number, including a fraction. Expires receives its truncated projection.
func (c *OAuthCredentials) SetExpiresMillis(value float64) {
	c.Expires, c.expiry = expiryFromNumber(value)
}

// ClearExpires removes the expires property, as a provider result without one does in Pi.
func (c *OAuthCredentials) ClearExpires() {
	c.Expires, c.expiry = 0, credentialExpiry{number: math.NaN(), set: true}
}

// ExpiresMillis returns ToNumber of an OAuth credential's expires value. An absent value is NaN, so both orderings compare false.
func (c Credential) ExpiresMillis() float64 { return c.expiry.millis(c.Expires) }

// HasExpires reports whether the credential carries an expires property.
func (c Credential) HasExpires() bool {
	if c.Type != CredentialOAuth && !c.expiry.set {
		return c.Expires != 0
	}
	_, present := c.expiry.json(c.Expires)
	return present
}

// SetExpiresMillis stores a JavaScript number, including a fraction. Expires receives its truncated projection.
func (c *Credential) SetExpiresMillis(value float64) { c.Expires, c.expiry = expiryFromNumber(value) }

// ClearExpires removes the expires property.
func (c *Credential) ClearExpires() {
	c.Expires, c.expiry = 0, credentialExpiry{number: math.NaN(), set: true}
}

// OAuthCredentials returns the OAuth token object stored in c, including provider-owned metadata and the exact expires value.
func (c Credential) OAuthCredentials() OAuthCredentials { return credentialToOAuth(c) }

// CredentialFromOAuth returns the stored OAuth credential for a token object, as Pi's `{ ...credential, type: "oauth" }` does. It fails only when retained metadata is not valid JSON.
func CredentialFromOAuth(value OAuthCredentials) (Credential, error) {
	return credentialFromOAuth(value)
}

// jsonNumberToken reports whether raw is a JSON number, the typeof "number" check Pi applies to parsed responses.
func jsonNumberToken(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9')
}
