package ecmascript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// String carries JavaScript string code units. Lone UTF-16 surrogates use their WTF-8 encoding in the native string and their escaped code unit in JSON.
type String string

func (text *String) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		*text = ""
		return nil
	}
	var validated string
	if err := json.Unmarshal(raw, &validated); err != nil {
		return err
	}
	if len(raw) == 0 || raw[0] != '"' {
		return fmt.Errorf("expected JSON string")
	}
	out := []byte{}
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			r, size := utf8.DecodeRune(raw[i:])
			out = utf8.AppendRune(out, r)
			i += size - 1
			continue
		}
		i++
		if raw[i] != 'u' {
			switch raw[i] {
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			default:
				out = append(out, raw[i])
			}
			continue
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return err
		}
		i += 4
		r := rune(n)
		if r >= 0xd800 && r <= 0xdbff && i+6 < len(raw) && raw[i+1] == '\\' && raw[i+2] == 'u' {
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err == nil && low >= 0xdc00 && low <= 0xdfff {
				r = utf16.DecodeRune(r, rune(low))
				i += 6
			}
		}
		if r >= 0xd800 && r <= 0xdfff {
			out = append(out, 0xe0|byte(r>>12), 0x80|byte((r>>6)&0x3f), 0x80|byte(r&0x3f))
		} else {
			out = utf8.AppendRune(out, r)
		}
	}
	*text = String(out)
	return nil
}

func (text String) MarshalJSON() ([]byte, error) {
	value := string(text)
	out := []byte{'"'}
	start := 0
	appendSegment := func(end int) error {
		encoded, err := json.Marshal(value[start:end])
		if err != nil {
			return err
		}
		out = append(out, encoded[1:len(encoded)-1]...)
		return nil
	}
	for i := 0; i+2 < len(value); i++ {
		if value[i] != 0xed || value[i+1] < 0xa0 || value[i+1] > 0xbf || value[i+2] < 0x80 || value[i+2] > 0xbf {
			continue
		}
		if err := appendSegment(i); err != nil {
			return nil, err
		}
		code := uint16(value[i]&0x0f)<<12 | uint16(value[i+1]&0x3f)<<6 | uint16(value[i+2]&0x3f)
		out = fmt.Appendf(out, `\u%04x`, code)
		i += 2
		start = i + 1
	}
	if err := appendSegment(len(value)); err != nil {
		return nil, err
	}
	return CanonicalJSON(append(out, '"'))
}
