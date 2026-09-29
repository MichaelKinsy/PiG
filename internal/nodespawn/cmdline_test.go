package nodespawn

import "testing"

func TestQuoteCmdArgLibuvCases(t *testing.T) {
	cases := []struct{ in, want string }{
		// The expected input/output list in libuv src/win/process.c quote_cmd_arg.
		{`hello"world`, `"hello\"world"`},
		{`hello""world`, `"hello\"\"world"`},
		{`hello\world`, `hello\world`},
		{`hello\\world`, `hello\\world`},
		{`hello\"world`, `"hello\\\"world"`},
		{`hello\\"world`, `"hello\\\\\"world"`},
		{`hello world\`, `"hello world\\"`},
		// The branches before the escaping loop.
		{``, `""`},
		{`plain`, `plain`},
		{"a\nb\vc", "a\nb\vc"},
		{`trailing\`, `trailing\`},
		{`a b`, `"a b"`},
		{"a\tb", "\"a\tb\""},
		// A double quote forces quoting without a space or tab; Go's
		// syscall.EscapeArg leaves this argument unquoted as a\"b.
		{`a"b`, `"a\"b"`},
		{`"`, `"\""`},
		{`""`, `"\"\""`},
		{`\"`, `"\\\""`},
		{`a b\c`, `"a b\c"`},
		{`a\b c\\`, `"a\b c\\\\"`},
		{`C:\Program Files\Git\bin\bash.exe`, `"C:\Program Files\Git\bin\bash.exe"`},
		{`héllo "wörld"\`, `"héllo \"wörld\"\\"`},
	}
	for _, testCase := range cases {
		if got := QuoteCmdArg(testCase.in); got != testCase.want {
			t.Errorf("QuoteCmdArg(%q) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

func TestCommandLineJoinsQuotedArguments(t *testing.T) {
	got := CommandLine([]string{`C:\Program Files\Git\bin\bash.exe`, "-c", `a"b`, ""})
	if want := `"C:\Program Files\Git\bin\bash.exe" -c "a\"b" ""`; got != want {
		t.Fatalf("CommandLine = %q, want %q", got, want)
	}
	if got := CommandLine(nil); got != "" {
		t.Fatalf("CommandLine(nil) = %q", got)
	}
}
