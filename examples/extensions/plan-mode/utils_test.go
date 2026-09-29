package planmode

import (
	"reflect"
	"strings"
	"testing"
)

func TestIsSafeCommand(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		commands []string
		want     bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:13
		{"allows basic read commands", []string{"ls -la", "cat file.txt", "head -n 10 file.txt", "tail -f log.txt", "grep pattern file", "find . -name '*.ts'"}, true},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:22
		{"allows git read commands", []string{"git status", "git log --oneline", "git diff", "git branch"}, true},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:29
		{"allows npm/yarn read commands", []string{"npm list", "npm outdated", "yarn info react"}, true},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:35
		{"allows other safe commands", []string{"pwd", "echo hello", "wc -l file.txt", "du -sh .", "df -h"}, true},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:45
		{"blocks file modification commands", []string{"rm file.txt", "rm -rf dir", "mv old new", "cp src dst", "mkdir newdir", "touch newfile"}, false},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:54
		{"blocks git write commands", []string{"git add .", "git commit -m 'msg'", "git push", "git checkout main", "git reset --hard"}, false},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:62
		{"blocks package manager installs", []string{"npm install lodash", "yarn add react", "pip install requests", "brew install node"}, false},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:69
		{"blocks redirects", []string{"echo hello > file.txt", "cat foo >> bar", ">file.txt"}, false},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:75
		{"blocks dangerous commands", []string{"sudo rm -rf /", "kill -9 1234", "reboot"}, false},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:81
		{"blocks editors", []string{"vim file.txt", "nano file.txt", "code ."}, false},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:89
		{"requires command to be in safe list (not just non-destructive)", []string{"unknown-command", "my-script.sh"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, command := range tt.commands {
				if got := IsSafeCommand(command); got != tt.want {
					t.Errorf("IsSafeCommand(%q) = %v, want %v", command, got, tt.want)
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:94
	t.Run("handles commands with leading whitespace", func(t *testing.T) {
		if !IsSafeCommand("  ls -la") || IsSafeCommand("  rm file") {
			t.Fatal("leading whitespace changed command safety")
		}
	})
}

func TestCleanStepText(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		pairs [][2]string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:102
		{"removes markdown bold/italic", [][2]string{{"**bold text**", "Bold text"}, {"*italic text*", "Italic text"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:107
		{"removes markdown code", [][2]string{{"run `npm install`", "Npm install"}, {"check the `config.json` file", "Config.json file"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:112
		{"removes leading action words", [][2]string{{"Create the new file", "New file"}, {"Run the tests", "Tests"}, {"Check the status", "Status"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:118
		{"capitalizes first letter", [][2]string{{"update config", "Config"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:129
		{"normalizes whitespace", [][2]string{{"multiple   spaces   here", "Multiple spaces here"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, pair := range tt.pairs {
				if got := CleanStepText(pair[0]); got != pair[1] {
					t.Errorf("CleanStepText(%q) = %q, want %q", pair[0], got, pair[1])
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:122
	t.Run("truncates long text", func(t *testing.T) {
		got := CleanStepText("This is a very long step description that exceeds the maximum allowed length for display")
		if len(got) != 50 || !strings.HasSuffix(got, "...") {
			t.Fatalf("long text = %q, want 50 characters ending in ...", got)
		}
	})
}

func TestExtractTodoItems(t *testing.T) {
	t.Parallel()
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:135
	t.Run("extracts numbered items after Plan: header", func(t *testing.T) {
		items := ExtractTodoItems("Here's what we'll do:\n\nPlan:\n1. First step here\n2. Second step here\n3. Third step here")
		if len(items) != 3 {
			t.Fatalf("items = %+v", items)
		}
		if items[0] != (TodoItem{Step: 1, Text: "First step here", Completed: false}) {
			t.Fatalf("first item = %+v", items[0])
		}
	})
	for _, tt := range []struct {
		name, message string
		want          int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:150
		{"handles bold Plan header", "**Plan:**\n1. Do something", 1},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:158
		{"handles parenthesis-style numbering", "Plan:\n1) First item\n2) Second item", 2},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:167
		{"returns empty array without Plan header", "Here are some steps:\n1. First step\n2. Second step", 0},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:186
		{"filters out code-like items", "Plan:\n1. `npm install`\n2. Run the build process", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if items := ExtractTodoItems(tt.message); len(items) != tt.want {
				t.Fatalf("items = %+v, want length %d", items, tt.want)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:176
	t.Run("filters out short items", func(t *testing.T) {
		items := ExtractTodoItems("Plan:\n1. OK\n2. This is a proper step")
		if len(items) != 1 || !strings.Contains(items[0].Text, "proper") {
			t.Fatalf("items = %+v", items)
		}
	})
}

func TestExtractDoneSteps(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, message string
		want          []float64
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:197
		{"extracts single DONE marker", "I've completed the first step [DONE:1]", []float64{1}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:202
		{"extracts multiple DONE markers", "Did steps [DONE:1] and [DONE:2] and [DONE:3]", []float64{1, 2, 3}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:207
		{"handles case insensitivity", "[done:1] [DONE:2] [Done:3]", []float64{1, 2, 3}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:212
		{"returns empty array with no markers", "No markers here", []float64{}},
		// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:217
		{"ignores malformed markers", "[DONE:abc] [DONE:] [DONE:1]", []float64{1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractDoneSteps(tt.message); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("steps = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMarkCompletedSteps(t *testing.T) {
	t.Parallel()
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:224
	t.Run("marks matching items as completed", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First"}, {Step: 2, Text: "Second"}, {Step: 3, Text: "Third"}}
		if count := MarkCompletedSteps("[DONE:1] [DONE:3]", items); count != 2 {
			t.Fatalf("count = %d", count)
		}
		if !items[0].Completed || items[1].Completed || !items[2].Completed {
			t.Fatalf("items = %+v", items)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:239
	t.Run("returns count of completed items", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First"}}
		if count := MarkCompletedSteps("[DONE:1]", items); count != 1 {
			t.Fatalf("count = %d", count)
		}
		if count := MarkCompletedSteps("no markers", items); count != 0 {
			t.Fatalf("count = %d", count)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:246
	t.Run("ignores markers for non-existent steps", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First"}}
		if count := MarkCompletedSteps("[DONE:99]", items); count != 1 {
			t.Fatalf("count = %d", count)
		}
		if items[0].Completed {
			t.Fatal("unknown marker completed an item")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/plan-mode-utils.test.ts:255
	t.Run("doesn't double-complete already completed items", func(t *testing.T) {
		items := []TodoItem{{Step: 1, Text: "First", Completed: true}}
		MarkCompletedSteps("[DONE:1]", items)
		if !items[0].Completed {
			t.Fatal("completed item became incomplete")
		}
	})
}
