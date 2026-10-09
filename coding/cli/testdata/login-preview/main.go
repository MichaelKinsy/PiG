package main

import (
	"errors"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"os"
	"strings"
)

func rows(w, h int) []string {
	r := make([]string, h)
	for i := range r {
		r[i] = strings.Repeat(".", w)
	}
	return r
}
func main() {
	e := sdk.New("login-preview")
	if os.Getenv("PIG_LOGIN_PREVIEW_FIXTURE") != "none" {
		e.OnSessionStart(func(c sdk.Context, _ map[string]any) (any, error) {
			if c.Mode() != "tui" {
				return nil, errors.New("login preview did not use TUI mode")
			}
			if os.Getenv("PIG_LOGIN_PREVIEW_FIXTURE") == "handler-error" {
				return nil, errors.New("fixture session start failed")
			}
			d := sdk.LoginDefinition{Brand: rows(41, 5), Hero: rows(32, 14), Mascot: rows(16, 14), Palette: map[string]string{"X": "#112233"}, Name: "Preview", Description: "Production renderer", Tagline: "No model and no session"}
			d.Brand[0] = "X" + d.Brand[0][1:]
			if os.Getenv("PIG_LOGIN_PREVIEW_FIXTURE") == "invalid" {
				d.Brand[0] = "."
			}
			return nil, c.SetLogin(d)
		})
	}
	if err := e.Run(); err != nil {
		panic(err)
	}
}
