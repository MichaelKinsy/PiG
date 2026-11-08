package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"math/rand/v2"
	"os/exec"
	"strings"
	"testing"
)

// decodeUTF8 is `new TextDecoder().decode(data)`: the WHATWG vectors, then every byte string of up to three bytes from the
// bytes that tell decoders apart, and random longer ones, against Node's own decoder.
func TestDecodeUTF8GivesTheWHATWGDecodersMaximalSubpartReplacements(t *testing.T) {
	for _, each := range []struct{ in, want string }{
		{"", ""},
		{"a\u00e9\u20ac\U0001f600", "a\u00e9\u20ac\U0001f600"},
		{"\xff", "\ufffd"},
		{"\xef\xbb\xbfa", "a"},
		{"\xef\xbb\xbf\xef\xbb\xbfa", "\ufeffa"},
		{"a\xef\xbb\xbf", "a\ufeff"},
		{"\xf0\x9f\x98", "\ufffd"},
		{"\xf0\x9f\x98a", "\ufffda"},
		{"\xe2\x82", "\ufffd"},
		{"\xe0\x80\x80", "\ufffd\ufffd\ufffd"},
		{"\xed\xa0\x80", "\ufffd\ufffd\ufffd"},
		{"\xc0\xaf", "\ufffd\ufffd"},
		{"\xf4\x90\x80\x80", "\ufffd\ufffd\ufffd\ufffd"},
		{"\xc3(", "\ufffd("},
		{"a\xffb", "a\ufffdb"},
	} {
		if got := decodeUTF8([]byte(each.in)); got != each.want {
			t.Errorf("decodeUTF8(%q) = %q, want %q", each.in, got, each.want)
		}
	}
}

func TestDecodeUTF8GivesWhatNodesTextDecoderGivesForEveryShortSequenceAndRandomOnes(t *testing.T) {
	path, err := discoverNode(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	alphabet := []byte{0x00, 0x41, 0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0, 0xc1, 0xc2, 0xdf, 0xe0, 0xe1, 0xec, 0xed, 0xee, 0xef, 0xf0, 0xf1, 0xf3, 0xf4, 0xf5, 0xff, 0xbb}
	var cases [][]byte
	cases = append(cases, nil)
	for _, a := range alphabet {
		cases = append(cases, []byte{a})
		for _, b := range alphabet {
			cases = append(cases, []byte{a, b})
			for _, c := range alphabet {
				cases = append(cases, []byte{a, b, c})
			}
		}
	}
	cases = append(cases, []byte{0xef, 0xbb, 0xbf}, []byte{0xef, 0xbb, 0xbf, 0xef, 0xbb, 0xbf, 0x41})
	random := rand.New(rand.NewPCG(7, 11))
	for range 3000 {
		data := make([]byte, random.IntN(14))
		for i := range data {
			data[i] = alphabet[random.IntN(len(alphabet))]
		}
		cases = append(cases, data)
	}
	var input strings.Builder
	for _, data := range cases {
		input.WriteString(base64.StdEncoding.EncodeToString(data))
		input.WriteByte('\n')
	}
	cmd := exec.Command(path, "-e", `const lines = require("node:fs").readFileSync(0, "utf8").split("\n");
lines.pop();
process.stdout.write(lines.map((line) => Buffer.from(new TextDecoder().decode(Buffer.from(line, "base64")), "utf8").toString("base64")).join("\n") + "\n");`)
	cmd.Stdin = strings.NewReader(input.String())
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(bytes.NewReader(out))
	lines.Buffer(nil, 1<<20)
	differing := 0
	for _, data := range cases {
		if !lines.Scan() {
			t.Fatal("Node answered fewer lines than it was asked")
		}
		want, err := base64.StdEncoding.DecodeString(lines.Text())
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeUTF8(data); got != string(want) {
			differing++
			if differing <= 8 {
				t.Errorf("decodeUTF8(%x) = %q, Node's is %q", data, got, want)
			}
		}
	}
	if differing > 0 {
		t.Fatalf("%d of %d byte strings decode differently from Node", differing, len(cases))
	}
}
