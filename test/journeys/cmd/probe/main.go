// Command probe drives PiG and Pi through a short script in a real terminal and prints each product's screen, to learn what an interaction looks like before writing a journey step.
//
//	go run ./test/journeys/cmd/probe -pig /path/to/pig -pi /path/to/pi script.txt
//
// Script lines: `type TEXT`, `key NAME...` (Enter, Escape, C-c, Up, ...), `say TEXT` (type and Enter), `wait SUBSTR` (visible), `idle N` (N provider requests answered), `quiet` (terminal silent), `snap LABEL`, `resize W H`, `click TEXT` (left click on the first cell of TEXT), `wheel up|down`, `paste TEXT` (bracketed paste), `sleep` is deliberately absent. The provider echoes the newest prompt; a prompt starting with `BASH:` calls the bash tool with the rest as the command, and `SAY:` replies with the rest verbatim.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

func main() {
	pig := flag.String("pig", "", "PiG binary")
	pi := flag.String("pi", "", "Pi binary")
	only := flag.String("only", "", "run only pig or pi")
	flag.Parse()
	if flag.NArg() != 1 || *pig == "" || *pi == "" {
		fmt.Fprintln(os.Stderr, "usage: probe -pig BIN -pi BIN script.txt")
		os.Exit(2)
	}
	steps, err := readScript(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg := &harness.Config{PigBin: *pig, PiBin: *pi, Scale: 1, ArtifactDir: os.TempDir()}
	j := &harness.Journey{Name: "probe", Script: func(r *harness.Request) harness.Reply {
		last := r.Last()
		if last.Role == "user" {
			t := strings.TrimSpace(last.Text)
			switch {
			case strings.HasPrefix(t, "BASH:"):
				return harness.Use(harness.Call{Name: "bash", Args: map[string]any{"command": strings.TrimPrefix(t, "BASH:")}})
			case strings.HasPrefix(t, "SAY:"):
				return harness.Say(strings.TrimPrefix(t, "SAY:"))
			}
		}
		return harness.Say("Echo: " + strings.Join(strings.Fields(last.Text), " "))
	}}
	j.Backbone = []harness.Step{{Name: "probe", Run: func(c *harness.Ctx) {
		c.Launch()
		for _, s := range steps {
			s(c)
		}
	}}, {Name: "end", Run: func(c *harness.Ctx) { c.Key("C-c"); c.Key("C-c") }}}
	var wg sync.WaitGroup
	out := map[harness.Kind]*strings.Builder{harness.PiG: {}, harness.Pi: {}}
	for _, k := range []harness.Kind{harness.PiG, harness.Pi} {
		if *only != "" && *only != string(k) {
			continue
		}
		wg.Go(func() {
			root, _ := os.MkdirTemp("", "probe-")
			defer func() { _ = os.RemoveAll(root) }()
			r, err := harness.NewRun(cfg, j, k, harness.Plan{Seed: 1}, root)
			if err != nil {
				fmt.Fprintln(out[k], err)
				return
			}
			r.Execute()
			for _, s := range r.Snaps {
				fmt.Fprintf(out[k], "=== %s ===\n%s\n", s.Name, s.Plain)
			}
			for _, f := range r.Failures {
				if f.Kind == "exit" || f.Kind == "leak" {
					continue
				}
				fmt.Fprintf(out[k], "FAILURE %s\n", f)
			}
		})
	}
	wg.Wait()
	for _, k := range []harness.Kind{harness.PiG, harness.Pi} {
		if out[k].Len() > 0 {
			fmt.Printf("############ %s\n%s", k, out[k])
		}
	}
}

func readScript(path string) ([]func(*harness.Ctx), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var steps []func(*harness.Ctx)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		verb, rest, _ := strings.Cut(line, " ")
		switch verb {
		case "type":
			steps = append(steps, func(c *harness.Ctx) { c.Type(rest) })
		case "say":
			steps = append(steps, func(c *harness.Ctx) { c.Say(rest) })
		case "key":
			steps = append(steps, func(c *harness.Ctx) { c.Key(strings.Fields(rest)...) })
		case "wait":
			steps = append(steps, func(c *harness.Ctx) { c.WaitFor(rest) })
		case "idle":
			n, _ := strconv.Atoi(rest)
			steps = append(steps, func(c *harness.Ctx) { c.Idle(n) })
		case "quiet":
			steps = append(steps, func(c *harness.Ctx) { c.Quiet(c.Settle()) })
		case "snap":
			steps = append(steps, func(c *harness.Ctx) { c.Quiet(c.Settle()); c.Snap(rest) })
		case "click":
			steps = append(steps, func(c *harness.Ctx) {
				row, col, ok := c.FindText(rest)
				if !ok {
					c.Fail("click: %q is not on screen", rest)
				}
				c.Click(col, row)
			})
		case "wheel":
			up := strings.HasPrefix(rest, "up")
			steps = append(steps, func(c *harness.Ctx) { c.Wheel(10, 10, up) })
		case "paste":
			steps = append(steps, func(c *harness.Ctx) { c.Paste(rest) })
		case "resize":
			var w, h int
			if n, _ := fmt.Sscan(rest, &w, &h); n != 2 {
				return nil, fmt.Errorf("resize needs WIDTH HEIGHT, got %q", rest)
			}
			steps = append(steps, func(c *harness.Ctx) { c.Resize(w, h) })
		default:
			return nil, fmt.Errorf("unknown script verb %q", verb)
		}
	}
	return steps, sc.Err()
}
