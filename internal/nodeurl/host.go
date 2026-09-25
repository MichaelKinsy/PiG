// Package nodeurl ports the parts of Node's url module that PiG needs to match
// Pi: the WHATWG host parser for file URLs and url.fileURLToPath.
package nodeurl

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
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
	if server == "" {
		return "", nil
	}
	if strings.HasPrefix(server, "[") {
		if !strings.HasSuffix(server, "]") {
			return "", fmt.Errorf("invalid file URL host %q", server)
		}
		addr, err := netip.ParseAddr(server[1 : len(server)-1])
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", fmt.Errorf("invalid file URL host %q", server)
		}
		return "[" + serializeIPv6(addr) + "]", nil
	}
	ascii, err := whatwgDomainToASCII.ToASCII(strings.ToValidUTF8(string(percentDecode(server)), string(utf8.RuneError)))
	if err != nil || ascii == "" || strings.ContainsFunc(ascii, isForbiddenDomainCodePoint) {
		return "", fmt.Errorf("invalid file URL host %q", server)
	}
	if endsInANumber(ascii) {
		ipv4, err := parseIPv4Host(ascii)
		if err != nil {
			return "", fmt.Errorf("invalid file URL host %q: %w", server, err)
		}
		return ipv4, nil
	}
	if ascii == "localhost" {
		return "", nil
	}
	return ascii, nil
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

// parseIPv4Number is the WHATWG IPv4 number parser.
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
	n, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, false
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
