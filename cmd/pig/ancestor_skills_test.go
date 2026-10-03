package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func BenchmarkCollectAncestorSkillInputs(b *testing.B) {
	for _, count := range []int{0, 16, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			cwd := filepath.Join(root, "nested")
			if err := os.MkdirAll(cwd, 0o700); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: unused\n"), 0o600); err != nil {
				b.Fatal(err)
			}
			for i := range count {
				file := filepath.Join(root, ".agents", "skills", fmt.Sprintf("skill-%03d", i), "SKILL.md")
				if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("---\ndescription: ancestor skill\n---\n"), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			explicit := filepath.Join(b.TempDir(), "SKILL.md")
			if err := os.WriteFile(explicit, []byte("---\nname: explicit\ndescription: explicit skill\n---\n"), 0o600); err != nil {
				b.Fatal(err)
			}
			agentDir := b.TempDir()
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			scopes := []string{"workspace"}
			flags := CLIFlags{Skills: []string{explicit}}
			b.ReportAllocs()
			for b.Loop() {
				got := collectSkillInputs(cwd, agentDir, sm, flags, &scopes)
				if len(got) != count+1 || got[len(got)-1] != explicit {
					b.Fatalf("skill inputs = %v", got)
				}
			}
		})
	}
}

// Pi package-manager.ts:448-480,2397-2401 walks ancestor skills only inside
// the nearest Git root and only after trust. resource-loader.ts:468-470
// appends --skill paths after that resolved set; skills.ts:425-454 is first-wins.
func TestRPCAncestorSkillTrustAndPrecedence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	cwd := filepath.Join(repo, "nested")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	// A worktree's .git is a file, not a directory.
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: unused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := func(base, name, description string) string {
		t.Helper()
		dir := makeSkillDir(t, base, name)
		data := "---\nname: " + name + "\ndescription: " + description + "\n---\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	skill(filepath.Join(root, ".agents", "skills"), "outside", "outside Git root")
	skill(filepath.Join(repo, ".agents", "skills"), "shared", "ancestor")
	cli := skill(t.TempDir(), "shared", "explicit")
	for _, tt := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"undecided", nil, "explicit"},
		{"approved", []string{"--approve"}, "ancestor"},
		{"denied", []string{"--no-approve"}, "explicit"},
		{"no-skills", []string{"--approve", "--no-skills"}, "explicit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			p := startRPCProcessAt(t, cwd, []string{
				"HOME=" + home, "USERPROFILE=" + home, "PIG_HOME=" + home,
				"PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_OFFLINE=1",
			}, append([]string{"--no-session", "--no-extensions", "--skill", cli}, tt.flags...)...)
			p.send(`{"id":"skills","type":"get_commands"}`)
			p.await("ancestor skill catalog", func(record rpcRecord) bool {
				if record["type"] != "response" || record["id"] != "skills" {
					return false
				}
				data, _ := record["data"].(map[string]any)
				commands, _ := data["commands"].([]any)
				var got []string
				for _, value := range commands {
					command, _ := value.(map[string]any)
					if command["source"] == "skill" {
						got = append(got, command["name"].(string)+":"+command["description"].(string))
					}
				}
				if !slices.Equal(got, []string{"skill:shared:" + tt.want}) {
					t.Fatalf("skill catalog = %v; want shared:%s", got, tt.want)
				}
				return true
			})
			p.closeAndWait("ancestor skill probe")
		})
	}
}
