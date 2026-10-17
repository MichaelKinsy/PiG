// Command session-replaced-2860-go runs the bodies of Pi's 2860-replaced-session-context.test.ts cases as Go SDK commands. Each line goes to the file named by PIG_TEST_2860_LOG; process ids name the extension instances, because each Session's extensions run in their own process here.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func record(line string) {
	f, err := os.OpenFile(os.Getenv("PIG_TEST_2860_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

func text(content any) string {
	if value, ok := content.(string); ok {
		return value
	}
	var out strings.Builder
	parts, _ := content.([]any)
	for _, part := range parts {
		if block, ok := part.(map[string]any); ok && block["type"] == "text" {
			value, _ := block["text"].(string)
			out.WriteString(value)
		}
	}
	return out.String()
}

func conversation(manager sdk.SessionManager) string {
	branch, err := manager.GetBranch(nil)
	if err != nil {
		return "error:" + err.Error()
	}
	var rows []string
	for _, entry := range branch {
		message, _ := entry["message"].(map[string]any)
		if entry["type"] != "message" || message == nil || message["role"] == "system" {
			continue
		}
		rows = append(rows, fmt.Sprintf("%v:%s", message["role"], text(message["content"])))
	}
	return strings.Join(rows, "|")
}

func marks() map[string]string {
	values := map[string]string{}
	if data, err := os.ReadFile(os.Getenv("PIG_TEST_2860_MARKS")); err == nil {
		_ = json.Unmarshal(data, &values)
	}
	return values
}

func sessionFile(manager sdk.SessionManager) string {
	file, err := manager.GetSessionFile()
	if err != nil || file == nil {
		return ""
	}
	return *file
}

func main() {
	pid := os.Getpid()
	ext := sdk.New("session-replaced-2860-go")
	ext.OnEvent(sdk.EventSessionStart, func(sdk.Context, map[string]any) (any, error) {
		record(fmt.Sprintf("start:%d", pid))
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionShutdown, func(sdk.Context, map[string]any) (any, error) {
		record(fmt.Sprintf("shutdown:%d", pid))
		return nil, nil
	})
	ext.Command("repro", "repro", func(ctx sdk.Context, _ string) error {
		oldCtx := ctx
		oldSessionFile := sessionFile(ctx.SessionManager())
		_, err := ctx.NewSession(map[string]any{
			"parentSession": oldSessionFile,
			"withSession": sdk.WithSessionFunc(func(replaced sdk.ReplacedSessionContext) error {
				record(fmt.Sprintf("with:%d", pid))
				replacement := sessionFile(replaced.SessionManager())
				record(fmt.Sprintf("replacement:%t", replacement != "" && replacement != oldSessionFile))
				_, staleErr := oldCtx.SessionManager().GetSessionFile()
				record(fmt.Sprintf("staleCtx:%t", staleErr != nil))
				record(fmt.Sprintf("stalePi:%t", oldCtx.SendUserMessage("stale message", "") != nil))
				if err := replaced.SendUserMessage("reply with exactly: hello reply", ""); err != nil {
					return err
				}
				idle, err := replaced.IsIdle()
				if err != nil {
					return err
				}
				record(fmt.Sprintf("idle:%t", idle))
				record("model:" + replaced.Model())
				record("conversation:" + conversation(replaced.SessionManager()))
				return nil
			}),
		})
		return err
	})
	// types.ts:411, agent-session-runtime.ts:254-257: setup seeds the replacement Session through its SessionManager before withSession runs.
	ext.Command("seed-it", "seed-it", func(ctx sdk.Context, _ string) error {
		_, err := ctx.NewSession(map[string]any{
			"setup": sdk.SetupFunc(func(manager sdk.SetupSessionManager) error {
				first, err := manager.AppendCustomMessageEntry("seed-msg", "from setup", true, nil)
				if err != nil {
					return err
				}
				second, err := manager.AppendSessionInfo("seeded-session")
				if err != nil {
					return err
				}
				record(fmt.Sprintf("setup:%t", first != "" && second != "" && first != second))
				return nil
			}),
			"withSession": sdk.WithSessionFunc(func(replaced sdk.ReplacedSessionContext) error {
				name, err := replaced.SessionManager().GetSessionName()
				if err != nil || name == nil {
					return fmt.Errorf("seed name: %v", err)
				}
				record("seedName:" + *name)
				entries, err := replaced.SessionManager().GetEntries()
				if err != nil {
					return err
				}
				var rows []string
				for _, entry := range entries {
					if entry["type"] == "custom_message" {
						rows = append(rows, fmt.Sprintf("%v=%v", entry["customType"], entry["content"]))
					}
				}
				record("seedEntry:" + strings.Join(rows, "|"))
				return nil
			}),
		})
		return err
	})
	ext.Command("throw-it", "throw-it", func(ctx sdk.Context, _ string) error {
		thrown := errors.New("callback failed")
		_, err := ctx.NewSession(map[string]any{"withSession": sdk.WithSessionFunc(func(sdk.ReplacedSessionContext) error { return thrown })})
		switch {
		case err == nil:
			record("caught:none")
		case errors.Is(err, thrown):
			record("caught:" + err.Error())
		default:
			record("caught:other:" + err.Error())
		}
		return nil
	})
	// A context kept past its callback must not block the extension: the command still returns.
	ext.Command("keep-it", "keep-it", func(ctx sdk.Context, _ string) error {
		var kept sdk.ReplacedSessionContext
		if _, err := ctx.NewSession(map[string]any{"withSession": sdk.WithSessionFunc(func(replaced sdk.ReplacedSessionContext) error {
			kept = replaced
			return nil
		})}); err != nil {
			return err
		}
		_, _ = kept.SessionManager().GetSessionFile()
		_, _ = kept.IsIdle()
		_ = kept.SendUserMessage("kept message", "")
		record("kept:returned")
		return nil
	})
	ext.Command("fork-it", "fork-it", func(ctx sdk.Context, _ string) error {
		leaf, err := ctx.SessionManager().GetLeafID()
		if err != nil {
			return err
		}
		if leaf == nil {
			return errors.New("Missing leaf id")
		}
		_, err = ctx.Fork(*leaf, map[string]any{
			"position": "at",
			"withSession": sdk.WithSessionFunc(func(replaced sdk.ReplacedSessionContext) error {
				if err := replaced.SendUserMessage("reply with exactly: fork reply", ""); err != nil {
					return err
				}
				record("conversation:" + conversation(replaced.SessionManager()))
				return nil
			}),
		})
		return err
	})
	ext.Command("mark", "mark", func(ctx sdk.Context, name string) error {
		values := marks()
		values[name] = sessionFile(ctx.SessionManager())
		data, err := json.Marshal(values)
		if err != nil {
			return err
		}
		return os.WriteFile(os.Getenv("PIG_TEST_2860_MARKS"), data, 0o644)
	})
	ext.Command("new", "new", func(ctx sdk.Context, _ string) error {
		_, err := ctx.NewSession(nil)
		return err
	})
	ext.Command("switch", "switch", func(ctx sdk.Context, name string) error {
		_, err := ctx.SwitchSession(marks()[name], nil)
		return err
	})
	ext.Command("switch-it", "switch-it", func(ctx sdk.Context, _ string) error {
		target := marks()["target"]
		_, err := ctx.SwitchSession(target, map[string]any{
			"withSession": sdk.WithSessionFunc(func(replaced sdk.ReplacedSessionContext) error {
				if err := replaced.SendUserMessage("reply with exactly: switch reply", ""); err != nil {
					return err
				}
				record(fmt.Sprintf("switched:%t", sessionFile(replaced.SessionManager()) == target))
				record("conversation:" + conversation(replaced.SessionManager()))
				return nil
			}),
		})
		return err
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "session-replaced-2860-go: %v\n", err)
		os.Exit(1)
	}
}
