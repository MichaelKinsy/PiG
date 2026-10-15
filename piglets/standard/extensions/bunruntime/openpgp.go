package bunruntime

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// The OpenPGP subset Bun's release signature uses (RFC 4880, RFC 9580 legacy EdDSA): a cleartext-signed message with one version 4
// signature of type 0x01 (canonical text) by an Ed25519 key over SHA-512. Anything else is rejected, so the verifier has no
// algorithm or key choice an attacker could steer.
const (
	sigTypeCanonicalText = 0x01
	pubkeyAlgoEdDSA      = 22
	hashAlgoSHA512       = 10
	subpacketIssuerFpr   = 33
)

// verifyClearsigned checks a cleartext-signed message against the Ed25519 public key with the given v4 fingerprint and returns the
// signed text, with dash escapes removed and lines joined by "\n".
func verifyClearsigned(armored []byte, publicKey ed25519.PublicKey, fingerprint []byte) (string, error) {
	text, signature, err := splitClearsigned(armored)
	if err != nil {
		return "", err
	}
	packet, err := signaturePacket(signature)
	if err != nil {
		return "", err
	}
	if len(packet) < 6 || packet[0] != 4 || packet[1] != sigTypeCanonicalText || packet[2] != pubkeyAlgoEdDSA || packet[3] != hashAlgoSHA512 {
		return "", errors.New("signature is not a version 4 canonical-text EdDSA SHA-512 signature")
	}
	hashedLen := int(binary.BigEndian.Uint16(packet[4:6]))
	if len(packet) < 6+hashedLen+2 {
		return "", errors.New("truncated signature packet")
	}
	hashed := packet[6 : 6+hashedLen]
	if !issuerIs(hashed, fingerprint) {
		return "", errors.New("signature is not by the pinned key")
	}
	rest := packet[6+hashedLen:]
	unhashedLen := int(binary.BigEndian.Uint16(rest[0:2]))
	if len(rest) < 2+unhashedLen+2 {
		return "", errors.New("truncated signature packet")
	}
	rest = rest[2+unhashedLen:]
	left16 := rest[0:2]
	r, rest, err := readMPI(rest[2:])
	if err != nil {
		return "", err
	}
	s, rest, err := readMPI(rest)
	if err != nil {
		return "", err
	}
	if len(rest) != 0 || len(r) > 32 || len(s) > 32 {
		return "", errors.New("malformed EdDSA signature")
	}

	hash := sha512.New()
	_, _ = hash.Write([]byte(canonicalText(text)))
	_, _ = hash.Write(packet[:6+hashedLen])
	trailer := []byte{4, 0xff, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(trailer[2:], uint32(6+hashedLen))
	_, _ = hash.Write(trailer)
	digest := hash.Sum(nil)
	if !bytes.Equal(digest[:2], left16) {
		return "", errors.New("signature does not match the signed text")
	}
	sig := make([]byte, 64)
	copy(sig[32-len(r):32], r)
	copy(sig[64-len(s):], s)
	if !ed25519.Verify(publicKey, digest, sig) {
		return "", errors.New("signature does not match the signed text")
	}
	return text, nil
}

// splitClearsigned returns the dash-unescaped text and the armored signature body of a cleartext-signed message.
func splitClearsigned(armored []byte) (string, []byte, error) {
	const (
		begin    = "-----BEGIN PGP SIGNED MESSAGE-----"
		sigBegin = "-----BEGIN PGP SIGNATURE-----"
		sigEnd   = "-----END PGP SIGNATURE-----"
	)
	scanner := bufio.NewScanner(bytes.NewReader(armored))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, strings.TrimSuffix(scanner.Text(), "\r"))
	}
	if err := scanner.Err(); err != nil {
		return "", nil, err
	}
	if len(lines) == 0 || lines[0] != begin {
		return "", nil, errors.New("not a cleartext-signed message")
	}
	i := 1
	for ; i < len(lines) && lines[i] != ""; i++ {
		if name, value, _ := strings.Cut(lines[i], ": "); name != "Hash" || value != "SHA512" {
			return "", nil, fmt.Errorf("unexpected armor header %q", lines[i])
		}
	}
	i++
	var text []string
	for ; i < len(lines) && lines[i] != sigBegin; i++ {
		line := lines[i]
		if strings.HasPrefix(line, "-") {
			unescaped, ok := strings.CutPrefix(line, "- ")
			if !ok {
				return "", nil, fmt.Errorf("unescaped dash line %q", line)
			}
			line = unescaped
		}
		text = append(text, line)
	}
	if i == len(lines) {
		return "", nil, errors.New("no signature block")
	}
	i++
	for ; i < len(lines) && lines[i] != ""; i++ {
		if !strings.Contains(lines[i], ": ") {
			return "", nil, errors.New("malformed signature armor")
		}
	}
	i++
	var body strings.Builder
	checksum := ""
	for ; i < len(lines) && lines[i] != sigEnd; i++ {
		if strings.HasPrefix(lines[i], "=") {
			checksum = lines[i][1:]
			continue
		}
		body.WriteString(lines[i])
	}
	if i == len(lines) {
		return "", nil, errors.New("unterminated signature block")
	}
	for _, line := range lines[i+1:] {
		if strings.TrimSpace(line) != "" {
			return "", nil, errors.New("data after the signature block")
		}
	}
	signature, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil {
		return "", nil, fmt.Errorf("signature armor: %w", err)
	}
	if checksum != "" {
		want, err := base64.StdEncoding.DecodeString(checksum)
		if err != nil || len(want) != 3 || crc24(signature) != uint32(want[0])<<16|uint32(want[1])<<8|uint32(want[2]) {
			return "", nil, errors.New("signature armor checksum mismatch")
		}
	}
	return strings.Join(text, "\n"), signature, nil
}

// canonicalText is the signed form of a text: trailing spaces and tabs removed from each line and lines joined by CRLF.
func canonicalText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\r\n")
}

// signaturePacket returns the body of the single signature packet (tag 2) in data.
func signaturePacket(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0]&0x80 == 0 {
		return nil, errors.New("not an OpenPGP packet")
	}
	var tag byte
	var length int
	var header int
	if data[0]&0x40 != 0 {
		tag = data[0] & 0x3f
		switch first := data[1]; {
		case first < 192:
			length, header = int(first), 2
		case first < 224:
			if len(data) < 3 {
				return nil, errors.New("truncated packet header")
			}
			length, header = (int(first)-192)<<8+int(data[2])+192, 3
		case first == 255:
			if len(data) < 6 {
				return nil, errors.New("truncated packet header")
			}
			length, header = int(binary.BigEndian.Uint32(data[2:6])), 6
		default:
			return nil, errors.New("partial-length signature packet")
		}
	} else {
		tag = (data[0] >> 2) & 0x0f
		switch data[0] & 3 {
		case 0:
			length, header = int(data[1]), 2
		case 1:
			if len(data) < 3 {
				return nil, errors.New("truncated packet header")
			}
			length, header = int(binary.BigEndian.Uint16(data[1:3])), 3
		case 2:
			if len(data) < 5 {
				return nil, errors.New("truncated packet header")
			}
			length, header = int(binary.BigEndian.Uint32(data[1:5])), 5
		default:
			return nil, errors.New("indeterminate-length signature packet")
		}
	}
	if tag != 2 {
		return nil, fmt.Errorf("packet tag %d is not a signature", tag)
	}
	if length < 0 || len(data) != header+length {
		return nil, errors.New("signature block holds more than one signature packet")
	}
	return data[header:], nil
}

// issuerIs reports whether the hashed subpackets name the issuer fingerprint.
func issuerIs(subpackets, fingerprint []byte) bool {
	for len(subpackets) > 0 {
		length, header := int(subpackets[0]), 1
		switch {
		case subpackets[0] == 255:
			if len(subpackets) < 5 {
				return false
			}
			length, header = int(binary.BigEndian.Uint32(subpackets[1:5])), 5
		case subpackets[0] >= 192:
			if len(subpackets) < 2 {
				return false
			}
			length, header = (int(subpackets[0])-192)<<8+int(subpackets[1])+192, 2
		}
		if length < 1 || len(subpackets) < header+length {
			return false
		}
		body := subpackets[header : header+length]
		// The issuer fingerprint subpacket: type, key version 4, fingerprint.
		if body[0]&0x7f == subpacketIssuerFpr && len(body) == 2+len(fingerprint) && body[1] == 4 && bytes.Equal(body[2:], fingerprint) {
			return true
		}
		subpackets = subpackets[header+length:]
	}
	return false
}

func readMPI(data []byte) ([]byte, []byte, error) {
	if len(data) < 2 {
		return nil, nil, errors.New("truncated MPI")
	}
	bits := int(binary.BigEndian.Uint16(data[:2]))
	n := (bits + 7) / 8
	if len(data) < 2+n {
		return nil, nil, errors.New("truncated MPI")
	}
	return data[2 : 2+n], data[2+n:], nil
}

func crc24(data []byte) uint32 {
	crc := uint32(0xB704CE)
	for _, b := range data {
		crc ^= uint32(b) << 16
		for range 8 {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= 0x1864CFB
			}
		}
	}
	return crc & 0xFFFFFF
}
