package planmode

import (
	"reflect"
	"strings"
	"testing"
)

// Every case of packages/coding-agent/test/plan-mode-utils.test.ts at c23b9ec1,
// in Pi's order, with Pi's inputs and expected results. The describe block is
// the test function; the it() title is the subtest name.

func TestPiIsSafeCommand(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, want bool, commands ...string) {
		t.Helper()
		for _, command := range commands {
			if got := IsSafeCommand(command); got != want {
				t.Errorf("IsSafeCommand(%q) = %v, want %v", command, got, want)
			}
		}
	}
	t.Run("allows basic read commands", func(t *testing.T) {
		check(t, true, "ls -la", "cat file.txt", "head -n 10 file.txt", "tail -f log.txt", "grep pattern file", "find . -name '*.ts'")
	})
	t.Run("allows git read commands", func(t *testing.T) {
		check(t, true, "git status", "git log --oneline", "git diff", "git branch")
	})
	t.Run("allows npm/yarn read commands", func(t *testing.T) {
		check(t, true, "npm list", "npm outdated", "yarn info react")
	})
	t.Run("allows other safe commands", func(t *testing.T) {
		check(t, true, "pwd", "echo hello", "wc -l file.txt", "du -sh .", "df -h")
	})
	t.Run("blocks file modification commands", func(t *testing.T) {
		check(t, false, "rm file.txt", "rm -rf dir", "mv old new", "cp src dst", "mkdir newdir", "touch newfile")
	})
	t.Run("blocks git write commands", func(t *testing.T) {
		check(t, false, "git add .", "git commit -m 'msg'", "git push", "git checkout main", "git reset --hard")
	})
	t.Run("blocks package manager installs", func(t *testing.T) {
		check(t, false, "npm install lodash", "yarn add react", "pip install requests", "brew install node")
	})
	t.Run("blocks redirects", func(t *testing.T) {
		check(t, false, "echo hello > file.txt", "cat foo >> bar", ">file.txt")
	})
	t.Run("blocks dangerous commands", func(t *testing.T) {
		check(t, false, "sudo rm -rf /", "kill -9 1234", "reboot")
	})
	t.Run("blocks editors", func(t *testing.T) {
		check(t, false, "vim file.txt", "nano file.txt", "code .")
	})
	t.Run("requires command to be in safe list (not just non-destructive)", func(t *testing.T) {
		check(t, false, "unknown-command", "my-script.sh")
	})
	t.Run("handles commands with leading whitespace", func(t *testing.T) {
		check(t, true, "  ls -la")
		check(t, false, "  rm file")
	})
}

func TestPiCleanStepText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"removes markdown bold/italic", "**bold text**", "Bold text"},
		{"removes markdown bold/italic", "*italic text*", "Italic text"},
		{"removes markdown code", "run `npm install`", "Npm install"},
		{"removes markdown code", "check the `config.json` file", "Config.json file"},
		{"removes leading action words", "Create the new file", "New file"},
		{"removes leading action words", "Run the tests", "Tests"},
		{"removes leading action words", "Check the status", "Status"},
		{"capitalizes first letter", "update config", "Config"},
		{"keeps full text (truncation moved to the widget display)",
			"This is a very long step description that exceeds the maximum allowed length for display",
			"This is a very long step description that exceeds the maximum allowed length for display"},
		{"normalizes whitespace", "multiple   spaces   here", "Multiple spaces here"},
	} {
		if got := CleanStepText(tc.in); got != tc.want {
			t.Errorf("%s: CleanStepText(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestPiExtractTodoItems(t *testing.T) {
	t.Parallel()
	texts := func(items []TodoItem) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.Text)
		}
		return out
	}
	t.Run("extracts numbered items after Plan: header", func(t *testing.T) {
		items := ExtractTodoItems("Here's what we'll do:\n\nPlan:\n1. First step here\n2. Second step here\n3. Third step here")
		if len(items) != 3 || items[0].Step != 1 || items[0].Text != "First step here" || items[0].Completed {
			t.Fatalf("items %+v", items)
		}
	})
	t.Run("handles bold Plan header", func(t *testing.T) {
		if items := ExtractTodoItems("**Plan:**\n1. Do something"); len(items) != 1 {
			t.Fatalf("items %+v", items)
		}
	})
	t.Run("handles parenthesis-style numbering", func(t *testing.T) {
		if items := ExtractTodoItems("Plan:\n1) First item\n2) Second item"); len(items) != 2 {
			t.Fatalf("items %+v", items)
		}
	})
	t.Run("returns empty array without Plan header", func(t *testing.T) {
		if items := ExtractTodoItems("Here are some steps:\n1. First step\n2. Second step"); len(items) != 0 {
			t.Fatalf("items %+v", items)
		}
	})
	t.Run("filters out short items", func(t *testing.T) {
		items := ExtractTodoItems("Plan:\n1. OK\n2. This is a proper step")
		if len(items) != 1 || !strings.Contains(items[0].Text, "proper") {
			t.Fatalf("items %+v", items)
		}
	})
	t.Run("keeps steps that start with backticks (code-file steps)", func(t *testing.T) {
		items := ExtractTodoItems("Plan:\n1. `npm install`\n2. Run the build process")
		if len(items) != 2 || items[0].Text != "Npm install" {
			t.Fatalf("items %+v", items)
		}
	})
	t.Run("extracts only the last Plan: section (thinking draft + text plan)", func(t *testing.T) {
		items := ExtractTodoItems("Some reasoning...\nPlan:\n1. Draft one\n2. Draft two\nNow the answer.\nPlan:\n1. Print 1\n2. Print 2\n3. Print 3")
		if want := []string{"Print 1", "Print 2", "Print 3"}; !reflect.DeepEqual(texts(items), want) {
			t.Fatalf("texts %q, want %q", texts(items), want)
		}
	})
	t.Run("extracts markdown step titles fully", func(t *testing.T) {
		items := ExtractTodoItems("Plan:\n1. **Explore** the repo structure\n2. `index.ts` — fix getTextContent")
		if want := []string{"Explore the repo structure", "Index.ts — fix getTextContent"}; !reflect.DeepEqual(texts(items), want) {
			t.Fatalf("texts %q, want %q", texts(items), want)
		}
	})
}

func TestPiExtractDoneSteps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, message string
		want          []float64
	}{
		{"extracts single DONE marker", "I've completed the first step [DONE:1]", []float64{1}},
		{"extracts multiple DONE markers", "Did steps [DONE:1] and [DONE:2] and [DONE:3]", []float64{1, 2, 3}},
		{"handles case insensitivity", "[done:1] [DONE:2] [Done:3]", []float64{1, 2, 3}},
		{"extracts DONE marker variants", "[DONE:1] [DONE: 2] [DONE3] [Done:4] Done: 5", []float64{1, 2, 3, 4, 5}},
		{"extracts prose completion phrasings", "Step 6 done, Step 7 is complete, Step 8 finished", []float64{6, 7, 8}},
		{"does not match negated or unfinished phrasings", "Step 9 is not done, Step 10 incomplete", []float64{}},
		{"collapses duplicate markers", "[DONE:1] [DONE: 1]", []float64{1}},
		{"ignores malformed markers", "[DONE:abc] [DONE:] [DONE:1]", []float64{1}},
	} {
		got := ExtractDoneSteps(tc.message)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPiMarkCompletedSteps(t *testing.T) {
	t.Parallel()
	t.Run("marks matching items as completed", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First"}, {Step: 2, Text: "Second"}, {Step: 3, Text: "Third"}}
		if count := MarkCompletedSteps("[DONE:1] [DONE:3]", items); count != 2 {
			t.Fatalf("count %d", count)
		}
		if !items[0].Completed || items[1].Completed || !items[2].Completed {
			t.Fatalf("items %+v", items)
		}
	})
	t.Run("returns count of completed items", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First"}}
		if got := MarkCompletedSteps("[DONE:1]", items); got != 1 {
			t.Fatalf("got %d", got)
		}
		if got := MarkCompletedSteps("no markers", items); got != 0 {
			t.Fatalf("got %d", got)
		}
	})
	t.Run("ignores markers for non-existent steps", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First"}}
		if count := MarkCompletedSteps("[DONE:99]", items); count != 0 || items[0].Completed {
			t.Fatalf("count %d, items %+v", count, items)
		}
	})
	t.Run("marks all remaining steps for whole-plan completion phrasings", func(t *testing.T) {
		for message, want := range map[string]bool{
			"All steps complete, wrapping up": true,
			"Every step is finished":          true,
			"The plan is done":                true,
			"Step 1 complete, moving on":      false,
		} {
			if got := AllStepsComplete(message); got != want {
				t.Errorf("AllStepsComplete(%q) = %v, want %v", message, got, want)
			}
		}
	})
	t.Run("doesn't double-complete already completed items", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First", Completed: true}}
		MarkCompletedSteps("[DONE:1]", items)
		if !items[0].Completed {
			t.Fatalf("items %+v", items)
		}
	})
}
