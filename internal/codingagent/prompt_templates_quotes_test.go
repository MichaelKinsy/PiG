package codingagent

// pi: packages/coding-agent/test/prompt-templates.test.ts parseCommandArgs (96667f342, #11873, #11861)

import (
	"slices"
	"testing"
)

func TestParsePromptArgsQuoting(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"escaped quote in double quotes", `"quoted \"text\""`, []string{`quoted "text"`}},
		{"escaped quote in single quotes", `'it\'s here' next`, []string{"it's here", "next"}},
		{"other backslashes stay literal", `"C:\dir\file" '\\server\share'`, []string{`C:\dir\file`, `\\server\share`}},
		{"other quote character is not escaped", `"it's" 'a\"b'`, []string{"it's", `a\"b`}},
		{"outside quotes a backslash is literal", `say \"hi`, []string{"say", `\"hi`}},
		{"apostrophes inside words", "why doesn't the user's cache work", []string{"why", "doesn't", "the", "user's", "cache", "work"}},
		{"one apostrophe", "why doesn't this work", []string{"why", "doesn't", "this", "work"}},
		{"unclosed single quote at the start", "'tis the season", []string{"'tis", "the", "season"}},
		{"unclosed double quote", `say "hello world`, []string{"say", `"hello`, "world"}},
		{"lone quote", "a '", []string{"a", "'"}},
		{"quoted argument ending in a backslash", `"abc\"`, []string{`"abc\"`}},
		{"closed quote then unclosed", `"it's" 'x`, []string{"it's", "'x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParsePromptArgs(tc.input); !slices.Equal(got, tc.want) {
				t.Fatalf("ParsePromptArgs(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
