package glyphs

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/glyphs/server.ts: `/glyph.svg?seed=<path or name>[&size=<px>]`, a project glyph as an SVG file, for
// embedding (`<img src>`) where the sidebar's inline glyph is not available. Open to everyone, on purpose: the glyph is a pure
// function of the query and reads no data, so it tells a visitor nothing they did not send; the seed's length bounds the work.

// maxSeed is the longest seed, in UTF-16 code units (`seed.length`).
const maxSeed = 1024

// nan is `Number`'s answer for a text that is no number.
var nan = math.NaN()

// Server is the plugin's server facet: it contributes the glyph route.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.glyph-routes", func(self host.StoreSelf) error {
		serve := p2runtime.RouteHandler(serveGlyph)
		p2runtime.ContributeKeyed(self, p2runtime.Routes,
			p2runtime.Entry("GET /glyph.svg", serve),
			p2runtime.Entry("HEAD /glyph.svg", serve),
		)
		return nil
	})
})

func serveGlyph(response http.ResponseWriter, request *http.Request, _ p2runtime.RequestAccess, _ host.Ctx) error {
	seed, hasSeed := queryValue(request.URL.RawQuery, "seed")
	size := 64.0
	if text, ok := queryValue(request.URL.RawQuery, "size"); ok {
		size = number(text)
	}
	if !hasSeed || len(utf16.Encode([]rune(seed))) > maxSeed || size != float64(int(size)) || size < 8 || size > 1024 {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusBadRequest)
		_, err := response.Write([]byte("Expected ?seed=<at most 1024 characters>&size=<8 to 1024>\n"))
		return err
	}
	body := []byte(SVG(Of(seed), int(size)))
	header := response.Header()
	header.Set("Content-Type", "image/svg+xml; charset=utf-8")
	header.Set("Content-Length", strconv.Itoa(len(body)))
	// A seed's glyph is fixed for this algorithm (`glyph-v1`); a day bounds how long one lingers after a change.
	header.Set("Cache-Control", "public, max-age=86400")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cross-Origin-Resource-Policy", "cross-origin")
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return nil
	}
	_, err := response.Write(body)
	return err
}

// queryValue is the first value of name in a query string as `URLSearchParams.get` reads it: `+` is a space and a
// percent-escape that is not valid stays as written (net/url would drop the pair).
func queryValue(query, name string) (string, bool) {
	for _, pair := range strings.Split(query, "&") {
		key, value, _ := strings.Cut(pair, "=")
		if decode(key) == name {
			return string([]rune(decode(value))), true
		}
	}
	return "", false
}

func decode(text string) string {
	var out []byte
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '+':
			out = append(out, ' ')
		case c == '%' && i+2 < len(text) && hexValue(text[i+1]) >= 0 && hexValue(text[i+2]) >= 0:
			out = append(out, byte(hexValue(text[i+1])<<4|hexValue(text[i+2])))
			i += 2
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// number is JavaScript's `Number(text)` for the forms a size may take: white space around, decimal with a fraction or an
// exponent, or a 0x, 0o or 0b integer; NaN otherwise (and for the empty text, 0).
func number(text string) float64 {
	text = strings.TrimFunc(text, isScriptSpace)
	if text == "" {
		return 0
	}
	if len(text) > 2 && text[0] == '0' {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[text[1]]
		if base != 0 {
			value, err := strconv.ParseUint(text[2:], base, 64)
			if err != nil {
				return nan
			}
			return float64(value)
		}
	}
	if strings.Trim(text, "0123456789+-.eE") != "" {
		return nan
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nan
	}
	return value
}
