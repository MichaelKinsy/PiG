package tui

import (
	"fmt"
	"strings"
)

// retentionSource is a message of the retention and owner tests: prose with inline styles, a list and a Go code block.
func retentionSource(i int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Heading %d\n\n", i)
	for p := range 6 {
		fmt.Fprintf(&sb, "Paragraph %d of message %d with **bold**, *italic*, `code` and a [link](https://example.com/%d) plus enough lorem ipsum dolor sit amet consectetur to wrap past the width of the terminal.\n\n", p, i, p)
	}
	sb.WriteString("- item one\n- item two\n\n```go\nfmt.Println(\"hi\")\nfor i := 0; i < 3; i++ {}\n```\n")
	return sb.String()
}
