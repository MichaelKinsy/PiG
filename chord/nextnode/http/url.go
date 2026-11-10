package http

// Ports packages/chord/src/node/http/node.ts: the URL Node's parser makes of `http://` + Host + target (NodeIncoming.of, node.ts:103-117),
// a WHATWG basic URL parser for the special scheme `http`, checked against the pinned Node's own URL by testdata/url_corpus.jsonl.

import (
	"math"
	nethttp "net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// The URL parser's part of what the component reads (Pi calls `new URL(...)` on `http://<Host><target>`; the WHATWG URL
// parser is Pi's runtime, Go has none): the basic URL parser's run over `http://<Host><target>` for the special scheme `http`:
// the authority up to its first `/`, `\`, `?` or `#`, userinfo (a non-empty one is refused 403), the host (percent-decoded,
// IDNA-mapped, lowercased; an IPv4 address in any of its number forms, an IPv6 one compressed), the port (default and leading
// zeros dropped), the path (`\` as `/`, dot segments resolved, also the percent-encoded ones), the query and the fragment, each
// percent-encoded by its set, and every failure 400. The input is Node's: the request line's and header's bytes as Latin-1
// characters, so a byte above 0x7f is the code point of that value. Checked against the pinned Node's `URL` by
// testdata/url_corpus.jsonl (url_test.go). Go-only file.

// webURL is the serialized URL of request: its target (the request line's, for a server's request) as the URL parser
// serializes it, over the `Host` the client sent.
func webURL(request *nethttp.Request) string {
	host, ok := requestHeader(request, "host")
	if !ok || host == "" {
		host = "invalid"
	}
	target := request.RequestURI
	if !strings.HasPrefix(target, "/") {
		target = "/"
		if request.URL != nil {
			if path := request.URL.EscapedPath(); path != "" {
				target = path
			}
			if request.URL.RawQuery != "" {
				target += "?" + request.URL.RawQuery
			}
		}
	}
	if href, status := parseWireURL(host, target); status == 0 {
		return href
	}
	href, _ := parseWireURL("invalid", target)
	return href
}

// parseWireURL is `new URL("http://" + host + target)` for an origin-form target: the URL as serialized, or the status that
// refuses it at the wire (400 when it does not parse, 403 for userinfo).
func parseWireURL(host, target string) (string, int) {
	input := latin1Runes("http://" + host + target)
	// Leading and trailing C0 controls and spaces go, and every tab and newline.
	input = strings.TrimFunc(input, func(r rune) bool { return r <= 0x20 })
	input = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, input)
	rest := input[len("http:"):]
	// The special authority: any run of `/` and `\` before it.
	rest = strings.TrimLeft(rest, "/\\")
	end := strings.IndexAny(rest, "/\\?#")
	authority, after := rest, ""
	if end >= 0 {
		authority, after = rest[:end], rest[end:]
	}
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		credentials := authority[:at]
		authority = authority[at+1:]
		if credentials != "" && credentials != ":" {
			if authority == "" {
				return "", 400
			}
			// A non-empty user name or password. Checked after the host is found valid.
			if _, _, status := parseHostPort(authority); status != 0 {
				return "", status
			}
			return "", 403
		}
	}
	hostName, port, status := parseHostPort(authority)
	if status != 0 {
		return "", status
	}
	var out strings.Builder
	out.WriteString("http://")
	out.WriteString(hostName)
	if port != "" {
		out.WriteByte(':')
		out.WriteString(port)
	}
	out.WriteString(serializeRest(after))
	return out.String(), 0
}

// latin1Runes is bytes read as Latin-1: each byte as the character of its value, as UTF-8.
func latin1Runes(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, rune(s[i]))
	}
	return string(out)
}

// parseHostPort is the host and port states over an authority without userinfo: the host serialized and the port as the URL
// serializes it (empty: none or the default), or the failure's status.
func parseHostPort(authority string) (hostName, port string, status int) {
	inside, colon := false, -1
	for i := 0; i < len(authority) && colon < 0; i++ {
		switch authority[i] {
		case '[':
			inside = true
		case ']':
			inside = false
		case ':':
			if !inside {
				colon = i
			}
		}
	}
	hostText := authority
	if colon >= 0 {
		hostText, port = authority[:colon], authority[colon+1:]
	}
	if hostText == "" {
		return "", "", 400
	}
	hostName, ok := parseHost(hostText)
	if !ok {
		return "", "", 400
	}
	if port == "" {
		return hostName, "", 0
	}
	value := 0
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return "", "", 400
		}
		value = value*10 + int(port[i]-'0')
		if value > 65535 {
			return "", "", 400
		}
	}
	if value == 80 {
		return hostName, "", 0
	}
	return hostName, strconv.Itoa(value), 0
}

// parseHost is the host parser for a special URL: an IPv6 address in brackets, else the percent-decoded domain made ASCII
// (an IPv4 address when it ends in a number).
func parseHost(input string) (string, bool) {
	if input[0] == '[' {
		if input[len(input)-1] != ']' {
			return "", false
		}
		return parseIPv6(input[1 : len(input)-1])
	}
	domain := percentDecode(input)
	ascii, ok := domainToASCII(domain)
	if !ok || ascii == "" {
		return "", false
	}
	for i := 0; i < len(ascii); i++ {
		if forbiddenHostByte(ascii[i]) {
			return "", false
		}
	}
	if endsInANumber(ascii) {
		return parseIPv4(ascii)
	}
	return ascii, true
}

// percentDecode is input with each `%XX` as its byte, read as UTF-8 (an invalid sequence as U+FFFD).
func percentDecode(input string) string {
	if !strings.Contains(input, "%") {
		return input
	}
	out := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		if input[i] == '%' && i+2 < len(input) && isHex(input[i+1]) && isHex(input[i+2]) {
			out = append(out, hexValue(input[i+1])<<4|hexValue(input[i+2]))
			i += 2
			continue
		}
		out = append(out, input[i])
	}
	return strings.ToValidUTF8(string(out), "\uFFFD")
}

func hexValue(b byte) byte {
	switch {
	case b >= 'a':
		return b - 'a' + 10
	case b >= 'A':
		return b - 'A' + 10
	}
	return b - '0'
}

// domainProfile is UTS #46's ToASCII as the URL standard calls it: CheckHyphens false, CheckJoiners true, UseSTD3ASCIIRules false,
// Transitional_Processing false, VerifyDnsLength false. It runs on one label at a time: the bidi rule is applied to a label alone, as
// Node's parser (ada) applies it, not to every label of a domain that holds a right-to-left one.
var domainProfile = idna.New(idna.MapForLookup(), idna.BidiRule(), idna.CheckHyphens(false), idna.CheckJoiners(true),
	idna.StrictDomainName(false), idna.VerifyDNSLength(false), idna.Transitional(false))

// labelMapper maps a label the way the profile does, without reading an `xn--` prefix as punycode.
var labelMapper = idna.New(idna.MapForLookup(), idna.CheckHyphens(false), idna.StrictDomainName(false), idna.Transitional(false))

// mappedDots turns the dots UTS #46 maps to "." into one, so the domain splits into the labels it has after mapping.
var mappedDots = strings.NewReplacer("\u3002", ".", "\uff0e", ".", "\uff61", ".")

// domainToASCII is the domain to ASCII algorithm as Node's parser (ada) runs it: an ASCII domain is only lowercased, whatever its
// labels (no punycode decoding or validation of an `xn--` label); any other domain is split into the labels it has after mapping,
// and each label goes through UTS #46 on its own, with an `xn--` label that is empty or not ASCII after mapping refused.
func domainToASCII(domain string) (string, bool) {
	plain := true
	for i := 0; i < len(domain); i++ {
		if domain[i] >= 0x80 {
			plain = false
			break
		}
	}
	if plain {
		return strings.ToLower(domain), true
	}
	labels := strings.Split(mappedDots.Replace(domain), ".")
	for i, label := range labels {
		if label == "" {
			continue
		}
		mapped, _ := labelMapper.ToUnicode("-" + label)
		mapped = strings.TrimPrefix(mapped, "-")
		if strings.HasPrefix(mapped, "xn--") && (len(mapped) == 4 || !isASCII(mapped)) {
			return "", false
		}
		ascii, err := domainProfile.ToASCII(label)
		if err != nil {
			return "", false
		}
		labels[i] = ascii
	}
	return strings.Join(labels, "."), true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// endsInANumber is the URL standard's check that a domain's last label is a number (decimal, octal or hexadecimal), which
// makes the host an IPv4 address.
func endsInANumber(domain string) bool {
	parts := strings.Split(domain, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok
}

// parseIPv4Number is a number of an IPv4 address: decimal, `0x` hexadecimal or `0` octal; values above 2^53 saturate (any
// such value is out of range anyway).
func parseIPv4Number(input string) (float64, bool) {
	if input == "" {
		return 0, false
	}
	radix := 10
	switch {
	case len(input) >= 2 && (input[:2] == "0x" || input[:2] == "0X"):
		input, radix = input[2:], 16
	case len(input) >= 2 && input[0] == '0':
		input, radix = input[1:], 8
	}
	value := 0.0
	for i := 0; i < len(input); i++ {
		digit := int(hexValueOrBig(input[i]))
		if !isHex(input[i]) || digit >= radix {
			return 0, false
		}
		value = math.Min(value*float64(radix)+float64(digit), math.MaxFloat64)
	}
	return value, true
}

func hexValueOrBig(b byte) byte {
	if !isHex(b) {
		return 255
	}
	return hexValue(b)
}

// parseIPv4 is the IPv4 parser: the address as four decimal numbers.
func parseIPv4(input string) (string, bool) {
	parts := strings.Split(input, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	numbers := make([]float64, len(parts))
	for i, part := range parts {
		number, ok := parseIPv4Number(part)
		if !ok {
			return "", false
		}
		numbers[i] = number
	}
	for _, number := range numbers[:len(numbers)-1] {
		if number > 255 {
			return "", false
		}
	}
	last := numbers[len(numbers)-1]
	if last >= math.Pow(256, float64(5-len(numbers))) {
		return "", false
	}
	address := uint64(last)
	for i, number := range numbers[:len(numbers)-1] {
		address += uint64(number) * uint64(math.Pow(256, float64(3-i)))
	}
	return strconv.Itoa(int(address>>24&255)) + "." + strconv.Itoa(int(address>>16&255)) + "." + strconv.Itoa(int(address>>8&255)) + "." + strconv.Itoa(int(address&255)), true
}

// parseIPv6 is the IPv6 parser and serializer: eight 16-bit pieces, the first longest run of two or more zero pieces
// compressed, lowercase hexadecimal.
func parseIPv6(input string) (string, bool) {
	var address [8]uint16
	piece, compress, pointer := 0, -1, 0
	at := func(i int) int {
		if i < len(input) {
			return int(input[i])
		}
		return -1
	}
	if at(pointer) == ':' {
		if at(pointer+1) != ':' {
			return "", false
		}
		pointer += 2
		piece++
		compress = piece
	}
	for at(pointer) != -1 {
		if piece == 8 {
			return "", false
		}
		if at(pointer) == ':' {
			if compress != -1 {
				return "", false
			}
			pointer++
			piece++
			compress = piece
			continue
		}
		value, length := 0, 0
		for length < 4 && at(pointer) != -1 && isHex(byte(at(pointer))) {
			value = value*16 + int(hexValue(byte(at(pointer))))
			pointer++
			length++
		}
		if at(pointer) == '.' {
			if length == 0 {
				return "", false
			}
			pointer -= length
			if piece > 6 {
				return "", false
			}
			seen := 0
			for at(pointer) != -1 {
				value := -1
				if seen > 0 {
					if at(pointer) == '.' && seen < 4 {
						pointer++
					} else {
						return "", false
					}
				}
				if at(pointer) == -1 || at(pointer) < '0' || at(pointer) > '9' {
					return "", false
				}
				for at(pointer) >= '0' && at(pointer) <= '9' {
					number := at(pointer) - '0'
					switch {
					case value == -1:
						value = number
					case value == 0:
						return "", false
					default:
						value = value*10 + number
					}
					if value > 255 {
						return "", false
					}
					pointer++
				}
				address[piece] = address[piece]*0x100 + uint16(value)
				seen++
				if seen == 2 || seen == 4 {
					piece++
				}
			}
			if seen != 4 {
				return "", false
			}
			break
		}
		if at(pointer) == ':' {
			pointer++
			if at(pointer) == -1 {
				return "", false
			}
		} else if at(pointer) != -1 {
			return "", false
		}
		address[piece] = uint16(value)
		piece++
	}
	if compress != -1 {
		swaps := piece - compress
		piece = 7
		for piece != 0 && swaps > 0 {
			address[piece], address[compress+swaps-1] = address[compress+swaps-1], address[piece]
			piece--
			swaps--
		}
	} else if piece != 8 {
		return "", false
	}
	// The first longest run of zero pieces, at least two long.
	best, bestLength := -1, 1
	for i := 0; i < 8; {
		if address[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && address[j] == 0 {
			j++
		}
		if j-i > bestLength {
			best, bestLength = i, j-i
		}
		i = j
	}
	var out strings.Builder
	out.WriteByte('[')
	ignore0 := false
	for i := range 8 {
		if ignore0 && address[i] == 0 {
			continue
		}
		ignore0 = false
		if best == i {
			if i == 0 {
				out.WriteString("::")
			} else {
				out.WriteByte(':')
			}
			ignore0 = true
			continue
		}
		out.WriteString(strconv.FormatUint(uint64(address[i]), 16))
		if i != 7 {
			out.WriteByte(':')
		}
	}
	out.WriteByte(']')
	return out.String(), true
}

// serializeRest is what follows the authority (starting at its `/`, `\`, `?` or `#`, or empty) as the URL parser writes it:
// the path with `\` as `/`, its dot segments resolved, an empty one `/`, then the query and the fragment, each percent-encoded
// by its set.
func serializeRest(rest string) string {
	var path, query, fragment string
	hasQuery, hasFragment := false, false
	if at := strings.IndexByte(rest, '#'); at >= 0 {
		rest, fragment, hasFragment = rest[:at], rest[at+1:], true
	}
	if at := strings.IndexByte(rest, '?'); at >= 0 {
		rest, query, hasQuery = rest[:at], rest[at+1:], true
	}
	path = rest
	out := "/"
	if path != "" {
		out = encodeSet(normalizePath(path), pathEncode)
	}
	if hasQuery {
		out += "?" + encodeSet(query, queryEncode)
	}
	if hasFragment {
		out += "#" + encodeSet(fragment, fragmentEncode)
	}
	return out
}

// normalizePath resolves the dot segments of an absolute path (`\` counts as `/`).
func normalizePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(path[1:], "/")
	out := make([]string, 0, len(parts))
	for index, part := range parts {
		last := index == len(parts)-1
		switch dotKind(part) {
		case 1:
			if last {
				out = append(out, "")
			}
		case 2:
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, part)
		}
	}
	return "/" + strings.Join(out, "/")
}

// dotKind is 1 for a single-dot segment (`.`, `%2e`), 2 for a double-dot one, else 0.
func dotKind(segment string) int {
	switch strings.ToLower(segment) {
	case ".", "%2e":
		return 1
	case "..", ".%2e", "%2e.", "%2e%2e":
		return 2
	}
	return 0
}

// The percent-encode sets of the URL standard for a special URL, over the code points of the text (a code point above 0x7e
// is encoded as its UTF-8 bytes).
func pathEncode(r rune) bool {
	return r <= 0x20 || r == '"' || r == '#' || r == '<' || r == '>' || r == '?' || r == '^' || r == '`' || r == '{' || r == '}' || r >= 0x7f
}

func queryEncode(r rune) bool {
	return r <= 0x20 || r == '"' || r == '#' || r == '<' || r == '>' || r == '\'' || r >= 0x7f
}

func fragmentEncode(r rune) bool {
	return r <= 0x20 || r == '"' || r == '<' || r == '>' || r == '`' || r >= 0x7f
}

// encodeSet percent-encodes the code points of text that encode selects.
func encodeSet(text string, encode func(rune) bool) string {
	clean := true
	for _, r := range text {
		if encode(r) {
			clean = false
			break
		}
	}
	if clean {
		return text
	}
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	var buf [utf8.UTFMax]byte
	for _, r := range text {
		if !encode(r) {
			out.WriteRune(r)
			continue
		}
		for _, b := range buf[:utf8.EncodeRune(buf[:], r)] {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}

// forbiddenHostByte is a forbidden domain code point of the URL standard.
func forbiddenHostByte(b byte) bool {
	if b <= 0x20 || b == 0x7f {
		return true
	}
	switch b {
	case '#', '%', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return false
}
