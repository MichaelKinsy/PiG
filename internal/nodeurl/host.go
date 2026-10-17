// Package nodeurl ports the Node URL host/path handling and fileURLToPath semantics used by PiG.
package nodeurl

import (
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

// whatwgDomainToASCII is the WHATWG URL Standard's domain-to-ASCII: UTS #46
// non-transitional processing with CheckBidi and CheckJoiners, without
// CheckHyphens, UseSTD3ASCIIRules, or VerifyDnsLength.
var whatwgDomainToASCII = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.Transitional(false),
	idna.StrictDomainName(false),
	idna.CheckHyphens(false),
	idna.CheckJoiners(true),
	idna.VerifyDNSLength(false),
)

// FileHost is the WHATWG host parser's result for a file URL's host: an
// IPv6 address in brackets, a dotted IPv4 address, or a lowercase ASCII
// domain, where localhost is empty. The input is percent-decoded first, as
// the parser does.
func FileHost(server string) (string, error) {
	return parseSpecialHost(server, true)
}

// SpecialHost parses the host of a WHATWG special URL. Unlike FileHost, localhost remains a hostname.
func SpecialHost(server string) (string, error) {
	return parseSpecialHost(server, false)
}

func parseSpecialHost(server string, file bool) (string, error) {
	kind := "URL"
	if file {
		kind = "file URL"
	}
	if server == "" {
		return "", nil
	}
	if strings.HasPrefix(server, "[") {
		if !strings.HasSuffix(server, "]") {
			return "", fmt.Errorf("invalid %s host %q", kind, server)
		}
		addr, err := netip.ParseAddr(server[1 : len(server)-1])
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", fmt.Errorf("invalid %s host %q", kind, server)
		}
		return "[" + serializeIPv6(addr) + "]", nil
	}
	decoded := strings.ToValidUTF8(string(percentDecode(server)), string(utf8.RuneError))
	ascii, err := whatwgDomainToASCII.ToASCII(decoded)
	if err != nil || hasInvalidPunycodeLabel(decoded) || ascii == "" || strings.ContainsFunc(ascii, isForbiddenDomainCodePoint) {
		return "", fmt.Errorf("invalid %s host %q", kind, server)
	}
	if endsInANumber(ascii) {
		ipv4, err := parseIPv4Host(ascii)
		if err != nil {
			return "", fmt.Errorf("invalid %s host %q: %w", kind, server, err)
		}
		return ipv4, nil
	}
	if file && ascii == "localhost" {
		return "", nil
	}
	return ascii, nil
}

// labelSeparators maps the UTS #46 label separators that NFKC leaves alone to a full stop.
var labelSeparators = strings.NewReplacer("\u3002", ".", "\uff61", ".")

// dropIgnoredCodePoint drops the code points UTS #46 maps to nothing: format characters other than the joiners (U+200C and U+200D map through the deviation rules), variation selectors, U+034F, and the Hangul fillers and Khmer inherent vowels that Unicode 16 made ignored. It also drops the format characters UTS #46 disallows; those fail the mapping anyway.
func dropIgnoredCodePoint(r rune) rune {
	switch r {
	case 0x200c, 0x200d:
		return r
	case 0x34f, 0x115f, 0x1160, 0x17b4, 0x17b5, 0x3164, 0xffa0:
		return -1
	}
	if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r) {
		return -1
	}
	return r
}

// hasInvalidPunycodeLabel reports a label that is only the "xn--" prefix, or that starts with it and holds a non-ASCII character. Node rejects both; x/net/idna decodes an empty payload and drops the label, and accepts the mixed label by encoding it again. UTS #46 maps the ideographic, fullwidth and halfwidth ideographic full stops to the label separator and compatibility forms to ASCII before the label split, and ignores the code points dropIgnoredCodePoint drops, so the check reads the NFKC form with those separators replaced and those code points dropped.
// An ASCII host maps to itself apart from case, which the prefix test folds.
func hasInvalidPunycodeLabel(host string) bool {
	mapped := host
	if strings.ContainsFunc(host, func(r rune) bool { return r >= utf8.RuneSelf }) {
		mapped = labelSeparators.Replace(strings.Map(dropIgnoredCodePoint, norm.NFKC.String(host)))
	}
	for label := range strings.SplitSeq(mapped, ".") {
		if len(label) >= 4 && strings.EqualFold(label[:4], "xn--") && (len(label) == 4 || strings.ContainsFunc(label, func(r rune) bool { return r >= 0x80 })) {
			return true
		}
	}
	return false
}

// isForbiddenDomainCodePoint reports the WHATWG forbidden domain code points.
func isForbiddenDomainCodePoint(r rune) bool {
	return r <= 0x20 || r == 0x7f || strings.ContainsRune(`#%/:<>?@[\]^|`, r)
}

// endsInANumber is the WHATWG "ends in a number" check on an ASCII host.
func endsInANumber(host string) bool {
	parts := strings.Split(host, ".")
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

// parseIPv4Host is the WHATWG IPv4 parser: one to four decimal, octal
// (leading 0), or hex (0x) parts, serialized as a dotted quad.
func parseIPv4Host(host string) (string, error) {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", fmt.Errorf("IPv4 address has more than four parts")
	}
	numbers := make([]uint64, len(parts))
	for i, part := range parts {
		n, ok := parseIPv4Number(part)
		if !ok {
			return "", fmt.Errorf("invalid IPv4 part %q", part)
		}
		numbers[i] = n
	}
	for _, n := range numbers[:len(numbers)-1] {
		if n > 255 {
			return "", fmt.Errorf("IPv4 part out of range")
		}
	}
	last := numbers[len(numbers)-1]
	if last >= uint64(1)<<(8*(5-len(numbers))) {
		return "", fmt.Errorf("IPv4 address out of range")
	}
	ipv4 := last
	for i, n := range numbers[:len(numbers)-1] {
		ipv4 += n << (8 * (3 - i))
	}
	return fmt.Sprintf("%d.%d.%d.%d", byte(ipv4>>24), byte(ipv4>>16), byte(ipv4>>8), byte(ipv4)), nil
}

// parseIPv4Number is the WHATWG IPv4 number parser. The spec's number is unbounded, so a valid number past the uint64 range is MaxUint64: it still ends the host in a number, and the IPv4 parser rejects it as out of range.
func parseIPv4Number(part string) (uint64, bool) {
	if part == "" {
		return 0, false
	}
	base := 10
	digits := part
	switch {
	case len(part) >= 2 && (part[:2] == "0x" || part[:2] == "0X"):
		base, digits = 16, part[2:]
	case len(part) >= 2 && part[0] == '0':
		base, digits = 8, part[1:]
	}
	if digits == "" {
		return 0, true
	}
	for _, c := range []byte(digits) {
		digit := 16
		switch {
		case c >= '0' && c <= '9':
			digit = int(c - '0')
		case c|0x20 >= 'a' && c|0x20 <= 'f':
			digit = int(c|0x20-'a') + 10
		}
		if digit >= base {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return math.MaxUint64, true
	}
	return n, true
}

// serializeIPv6 is the WHATWG IPv6 serializer: lowercase hex pieces with the
// first longest run of two or more zero pieces compressed, and no embedded
// IPv4 notation.
func serializeIPv6(addr netip.Addr) string {
	bytes := addr.As16()
	var pieces [8]uint16
	for i := range pieces {
		pieces[i] = uint16(bytes[2*i])<<8 | uint16(bytes[2*i+1])
	}
	compressStart, compressLength := -1, 1
	for i := 0; i < len(pieces); {
		if pieces[i] != 0 {
			i++
			continue
		}
		j := i
		for j < len(pieces) && pieces[j] == 0 {
			j++
		}
		if j-i > compressLength {
			compressStart, compressLength = i, j-i
		}
		i = j
	}
	var out strings.Builder
	for i := 0; i < len(pieces); i++ {
		if i == compressStart {
			if i == 0 {
				out.WriteString("::")
			} else {
				out.WriteString(":")
			}
			i += compressLength - 1
			continue
		}
		out.WriteString(strconv.FormatUint(uint64(pieces[i]), 16))
		if i < len(pieces)-1 {
			out.WriteString(":")
		}
	}
	return out.String()
}
