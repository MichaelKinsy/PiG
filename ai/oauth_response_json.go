package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

var utf8BOM = []byte("\xef\xbb\xbf")

// errOAuthResponseNotJSON marks a response body that `await response.json()` rejects.
var errOAuthResponseNotJSON = errors.New("response is not valid JSON")

// oauthJSONSyntaxError is the SyntaxError `await response.json()` rejects with: V8's JSON.parse message for the decoded body. errors.Is matches it
// to errOAuthResponseNotJSON.
type oauthJSONSyntaxError string

func (e oauthJSONSyntaxError) Error() string { return string(e) }

func (oauthJSONSyntaxError) Is(target error) bool { return target == errOAuthResponseNotJSON }

// fetchBodyReadError is the error a fetch Response's json() rejects with when its body fails to read: undici's TypeError "terminated" for a
// connection closed mid-body, or the AbortError when the request was aborted.
func fetchBodyReadError(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.New("This operation was aborted")
	}
	return errors.New("terminated")
}

// oauthJSONRejection is the error `await response.json()` rejects with for a body that is not JSON.
func oauthJSONRejection(raw []byte) error {
	if err := jsonparse.Validate([]byte(fetchBodyText(raw))); err != nil {
		return oauthJSONSyntaxError(err.Error())
	}
	return errOAuthResponseNotJSON
}

// oauthJSONValue is `await response.json()` of a provider response: fetchBodyText of the body parsed as JSON, numbers kept as json.Number so an out-of-range literal can read as Infinity, and canonical, the text JSON.stringify gives the value.
func oauthJSONValue(raw []byte) (value any, canonical string, ok bool) {
	raw = []byte(fetchBodyText(raw))
	if !json.Valid(raw) {
		return nil, "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, "", false
	}
	text, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return nil, "", false
	}
	return value, string(text), true
}

// fetchBodyText is the text a Node fetch Response's text() and json() read: UTF-8 decoded with one U+FFFD for each maximal ill-formed subpart (WHATWG "UTF-8 decode"), not one per byte or per run, after up to two leading byte order marks are dropped. undici's utf8DecodeBytes drops one, then decodes with a TextDecoder that drops another; a third remains (Node 22.13 to 26.10).
func fetchBodyText(raw []byte) string {
	return jsstring.FromUTF8(bytes.TrimPrefix(bytes.TrimPrefix(raw, utf8BOM), utf8BOM))
}

// oauthTextSuffix is `${text ? ": " + text : ""}`.
func oauthTextSuffix(raw []byte) string {
	if text := fetchBodyText(raw); text != "" {
		return ": " + text
	}
	return ""
}

// fetchStatusText is a fetch Response's statusText: the reason phrase of the status line the server sent (net/http's Status without its leading code), which is empty when the server sends none.
func fetchStatusText(status string, code int) string {
	return strings.TrimPrefix(strings.TrimPrefix(status, strconv.Itoa(code)), " ")
}
