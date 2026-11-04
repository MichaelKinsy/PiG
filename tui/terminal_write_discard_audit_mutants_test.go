package tui

import (
	"go/parser"
	"go/token"
	"testing"
)

// The discard audit must see every spelling of a dropped terminal write: a planted mutant for each form that once slipped past it.
func TestDiscardAuditSeesEveryFormOfADroppedTerminalWrite(t *testing.T) {
	const header = "package p\nimport (\n\t\"fmt\"\n\t\"io\"\n\t\"os\"\n)\nvar _ = fmt.Sprint\nvar _ io.Writer\ntype T struct{ out io.Writer }\ntype C struct{ stdout io.Writer }\n"
	for _, tc := range []struct {
		name, body string
		flagged    bool
	}{
		{"routed", "WriteToTerminal(os.Stdout, \"x\")", false},
		{"mutant: _, _ = t.out.Write", "_, _ = t.out.Write([]byte(\"x\"))", true},
		{"mutant: _, _ = os.Stdout.Write", "_, _ = os.Stdout.Write([]byte(\"x\"))", true},
		{"mutant: _, _ = os.Stdout.WriteString", "_, _ = os.Stdout.WriteString(\"x\")", true},
		{"mutant: _, _ = fmt.Fprint(t.cursorOut())", "_, _ = fmt.Fprint(t.cursorOut(), \"x\")", true},
		{"mutant: _, _ = fmt.Printf", "_, _ = fmt.Printf(\"x\")", true},
		{"mutant: bare fmt.Println", "fmt.Println(\"x\")", true},
		{"mutant: bare fmt.Fprintf to stdout", "fmt.Fprintf(os.Stdout, \"x\")", true},
		{"mutant: bare os.Stdout.WriteString", "os.Stdout.WriteString(\"x\")", true},
		{"mutant: _, _ = io.WriteString(c.stdout)", "_, _ = io.WriteString(c.stdout, \"x\")", true},
		{"stderr is not the terminal's output", "_, _ = fmt.Fprint(os.Stderr, \"x\")", false},
		{"a file is not the terminal", "var f *os.File\n_, _ = f.Write([]byte(\"x\"))", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recv := "func (t *T) f(c *C) {\n" + tc.body + "\n}\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "audited.go", header+"func WriteToTerminal(io.Writer, string) {}\n"+recv, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := discardedTerminalWrites(fset, "audited.go", file); (len(got) > 0) != tc.flagged {
				t.Fatalf("findings %v, flagged want %t", got, tc.flagged)
			}
		})
	}
}
