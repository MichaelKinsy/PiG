package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Ports packages/chord/src/next-node/http/cookies.ts. Cookies (decision "The HTTP component's security baseline" (5)):
// `Cookie` makes a signed `Set-Cookie` value with the safe defaults (`HttpOnly`, `Path=/`, `SameSite=Strict`, `Secure` on
// a remote origin), `ReadCookie` reads one back, its signature checked in constant time.
//
// Forced Go differences. `Date.now()` is `now()`, the package's clock (tests set it for Pi's fake `Date`). Seconds until
// expiry (`maxAge`) is a *float64 (absent: a session cookie). `encodeURIComponent` is encodeURIComponent below.

// now is `Date.now()` as a time.
var now = time.Now

// CookieOptions is what `cookie` takes besides the name and the value.
type CookieOptions struct {
	// Secret is the key it is signed with (HMAC-SHA256).
	Secret string
	// Request is the request it answers: a remote one gets `Secure`.
	Request HttpRequest
	// SameSite is `Strict` by default; `Lax` lets a top-level navigation from another site carry it.
	SameSite string
	// MaxAge is the seconds until it expires; nil: a session cookie.
	MaxAge *float64
}

// Cookie is a signed `Set-Cookie` value for name and value (put it in a response's `headers`, `Set-Cookie`). With MaxAge its
// expiry is signed with it: a copy read back later is refused; without, it holds until the secret changes.
func Cookie(name, value string, options CookieOptions) string {
	encoded := encodeURIComponent(value)
	expires := ""
	if options.MaxAge != nil {
		expires = formatNumber(math.Floor(float64(now().UnixMilli())/1000 + *options.MaxAge))
	}
	payload := encoded + "." + expires
	sameSite := options.SameSite
	if sameSite == "" {
		sameSite = "Strict"
	}
	parts := []string{
		name + "=" + payload + "." + signature(name, payload, options.Secret),
		"HttpOnly",
		"Path=/",
		"SameSite=" + sameSite,
	}
	if options.Request.Provenance().Kind != "local" {
		parts = append(parts, "Secure")
	}
	if options.MaxAge != nil {
		parts = append(parts, "Max-Age="+formatNumber(math.Floor(*options.MaxAge)))
	}
	return strings.Join(parts, "; ")
}

// ReadCookie is the value of cookie name on request when its signature with secret holds and its signed expiry has not
// passed; false otherwise.
func ReadCookie(request HttpRequest, name, secret string) (string, bool) {
	header, _ := HeaderGet(request.Headers(), "Cookie")
	for _, pair := range strings.Split(header, ";") {
		equals := strings.IndexByte(pair, '=')
		if equals < 0 || strings.TrimSpace(pair[:equals]) != name {
			continue
		}
		signed := strings.TrimSpace(pair[equals+1:])
		// From the right: the value may hold dots, the expiry and the signature never do.
		dot := strings.LastIndexByte(signed, '.')
		if dot < 0 {
			return "", false
		}
		payload := signed[:dot]
		expiresAt := strings.LastIndexByte(payload, '.')
		if expiresAt < 0 {
			return "", false
		}
		given := []byte(signed[dot+1:])
		expected := []byte(signature(name, payload, secret))
		if !hmac.Equal(given, expected) {
			return "", false
		}
		expires := payload[expiresAt+1:]
		if expires != "" {
			seconds, err := strconv.ParseFloat(expires, 64)
			if err != nil || seconds*1000 <= float64(now().UnixMilli()) {
				return "", false
			}
		}
		decoded, err := url.PathUnescape(payload[:expiresAt])
		if err != nil {
			return "", false
		}
		return decoded, true
	}
	return "", false
}

// signature is the signature of a cookie's name and payload (its encoded value and expiry).
func signature(name, payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(name + "=" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// formatNumber is a JavaScript number's string for a whole value.
func formatNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

// encodeURIComponent is JavaScript's: everything but letters, digits and `-_.!~*'()` percent-encoded as UTF-8.
func encodeURIComponent(text string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("-_.!~*'()", b) >= 0 {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&15])
	}
	return out.String()
}
