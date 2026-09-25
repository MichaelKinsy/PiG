package planmode

import (
	"strings"
	"testing"
)

func TestIsSafeCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "ls", command: "ls -la", want: true},
		{name: "cat", command: "cat file.txt", want: true},
		{name: "head", command: "head -n 10 file.txt", want: true},
		{name: "tail", command: "tail -f log.txt", want: true},
		{name: "grep", command: "grep pattern file", want: true},
		{name: "find", command: "find . -name '*.ts'", want: true},
		{name: "git status", command: "git status", want: true},
		{name: "git log", command: "git log --oneline", want: true},
		{name: "git diff", command: "git diff", want: true},
		{name: "git branch", command: "git branch", want: true},
		{name: "npm list", command: "npm list", want: true},
		{name: "npm outdated", command: "npm outdated", want: true},
		{name: "yarn info", command: "yarn info react", want: true},
		{name: "pwd", command: "pwd", want: true},
		{name: "echo", command: "echo hello", want: true},
		{name: "wc", command: "wc -l file.txt", want: true},
		{name: "du", command: "du -sh .", want: true},
		{name: "df", command: "df -h", want: true},
		{name: "rm", command: "rm file.txt"},
		{name: "rm recursive", command: "rm -rf dir"},
		{name: "mv", command: "mv old new"},
		{name: "cp", command: "cp src dst"},
		{name: "mkdir", command: "mkdir newdir"},
		{name: "touch", command: "touch newfile"},
		{name: "git add", command: "git add ."},
		{name: "git commit", command: "git commit -m 'msg'"},
		{name: "git push", command: "git push"},
		{name: "git checkout", command: "git checkout main"},
		{name: "git reset", command: "git reset --hard"},
		{name: "npm install", command: "npm install lodash"},
		{name: "yarn add", command: "yarn add react"},
		{name: "pip install", command: "pip install requests"},
		{name: "brew install", command: "brew install node"},
		{name: "redirect", command: "echo hello > file.txt"},
		{name: "append redirect", command: "cat foo >> bar"},
		{name: "leading redirect", command: ">file.txt"},
		{name: "sudo", command: "sudo rm -rf /"},
		{name: "kill", command: "kill -9 1234"},
		{name: "reboot", command: "reboot"},
		{name: "vim", command: "vim file.txt"},
		{name: "nano", command: "nano file.txt"},
		{name: "code", command: "code ."},
		{name: "unknown", command: "unknown-command"},
		{name: "script", command: "my-script.sh"},
		{name: "leading whitespace safe", command: "  ls -la", want: true},
		{name: "leading whitespace destructive", command: "  rm file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsSafeCommand(tt.command); got != tt.want {
				t.Fatalf("IsSafeCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestCleanStepText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "bold", text: "**bold text**", want: "Bold text"},
		{name: "italic", text: "*italic text*", want: "Italic text"},
		{name: "code after action", text: "run `npm install`", want: "Npm install"},
		{name: "code in phrase", text: "check the `config.json` file", want: "Config.json file"},
		{name: "create action", text: "Create the new file", want: "New file"},
		{name: "run action", text: "Run the tests", want: "Tests"},
		{name: "check action", text: "Check the status", want: "Status"},
		{name: "capitalizes", text: "update config", want: "Config"},
		{name: "whitespace", text: "multiple   spaces   here", want: "Multiple spaces here"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CleanStepText(tt.text); got != tt.want {
				t.Fatalf("CleanStepText(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}

	longText := "This is a very long step description that exceeds the maximum allowed length for display"
	got := CleanStepText(longText)
	if len(got) != 50 || !strings.HasSuffix(got, "...") {
		t.Fatalf("CleanStepText(long) = %q (len %d), want 50 characters ending in ...", got, len(got))
	}
}

func TestExtractTodoItems(t *testing.T) {
	t.Parallel()

	t.Run("numbered items", func(t *testing.T) {
		message := "Here's what we'll do:\n\nPlan:\n1. First step here\n2. Second step here\n3. Third step here"
		items := ExtractTodoItems(message)
		if len(items) != 3 {
			t.Fatalf("len(items) = %d, want 3: %+v", len(items), items)
		}
		if items[0] != (TodoItem{Step: 1, Text: "First step here"}) {
			t.Fatalf("items[0] = %+v", items[0])
		}
	})

	tests := []struct {
		name    string
		message string
		want    int
	}{
		{name: "bold header", message: "**Plan:**\n1. Do something", want: 1},
		{name: "parenthesis numbering", message: "Plan:\n1) First item\n2) Second item", want: 2},
		{name: "missing header", message: "Here are some steps:\n1. First step\n2. Second step"},
		{name: "short item", message: "Plan:\n1. OK\n2. This is a proper step", want: 1},
		{name: "code item", message: "Plan:\n1. `npm install`\n2. Run the build process", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := len(ExtractTodoItems(tt.message)); got != tt.want {
				t.Fatalf("len(ExtractTodoItems()) = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExtractDoneSteps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		want    []int
	}{
		{name: "single", message: "I've completed the first step [DONE:1]", want: []int{1}},
		{name: "multiple", message: "Did steps [DONE:1] and [DONE:2] and [DONE:3]", want: []int{1, 2, 3}},
		{name: "case insensitive", message: "[done:1] [DONE:2] [Done:3]", want: []int{1, 2, 3}},
		{name: "none", message: "No markers here", want: []int{}},
		{name: "malformed", message: "[DONE:abc] [DONE:] [DONE:1]", want: []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractDoneSteps(tt.message)
			if len(got) != len(tt.want) {
				t.Fatalf("ExtractDoneSteps() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != float64(tt.want[i]) {
					t.Fatalf("ExtractDoneSteps() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestMarkCompletedSteps(t *testing.T) {
	t.Parallel()

	items := []TodoItem{
		{Step: 1, Text: "First"},
		{Step: 2, Text: "Second"},
		{Step: 3, Text: "Third"},
	}
	if got := MarkCompletedSteps("[DONE:1] [DONE:3]", items); got != 2 {
		t.Fatalf("MarkCompletedSteps() = %d, want 2", got)
	}
	if !items[0].Completed || items[1].Completed || !items[2].Completed {
		t.Fatalf("completion state = %+v", items)
	}

	if got := MarkCompletedSteps("no markers", items); got != 0 {
		t.Fatalf("no-marker count = %d, want 0", got)
	}
	if got := MarkCompletedSteps("[DONE:99]", items); got != 1 {
		t.Fatalf("unknown-marker count = %d, want 1", got)
	}
	if got := MarkCompletedSteps("[DONE:1]", items); got != 1 || !items[0].Completed {
		t.Fatalf("repeat marker = %d, state %+v", got, items)
	}
}
