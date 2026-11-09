package hosts

// The unrestricted launcher (`p2 tui`): authentication, terminal and fixed-origin transport stay outside the plugins. The terminal client of a running p2 server
// (`--radius` signs in through Radius instead of a URL).
//
// pi: apps/p2/src/hosts/tui.ts

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// TUISubcommand is the name of the terminal client program: `p2 tui [url] [options]`.
const TUISubcommand = "tui"

// RunTUI runs the terminal client with args (without the program and subcommand names) and returns the exit code.
func RunTUI(args []string) int {
	stdin := bufio.NewReader(os.Stdin)
	ask := func(question string) (string, error) {
		fmt.Fprint(os.Stdout, question)
		line, err := stdin.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	var (
		mu       sync.Mutex
		current  *host.Client
		stopping bool
		finish   = func() {}
	)
	go func() {
		<-signals
		mu.Lock()
		stopping = true
		client, done := current, finish
		mu.Unlock()
		stop()
		if client != nil {
			client.Close()
		}
		done()
	}()
	isStopping := func() bool { mu.Lock(); defer mu.Unlock(); return stopping }

	options, err := host.ParseArgs(args, os.Getenv)
	if err != nil {
		return tuiFailure(err)
	}
	config, err := host.ReadConfig(host.ConfigDirectory())
	if err != nil {
		return tuiFailure(err)
	}
	// The visitor's own pi keybindings/settings/themes and p2 prefs, read here and sent to the TUI as data.
	sources := host.NewLocalPreferenceSources(host.LocalPreferenceOptions{Theme: options.Theme, NoThemes: options.NoThemes})
	preferences := host.LoadLocalPreferences(sources)
	var radius *host.RadiusConnection
	if options.Radius {
		if radius, err = host.ConnectRadius(ctx, options.RadiusServer, os.Stdout); err != nil {
			return tuiFailure(err)
		}
	}
	plugins, err := SelectPlugins(nil)
	if err != nil {
		return tuiFailure(err)
	}
	for !isStopping() {
		var admission host.Admission
		if radius != nil {
			admission = host.Admission{Role: "owner"}
		} else {
			admission, err = host.Authenticate(ctx, host.AuthOptions{URL: options.URL, OwnerLink: options.OwnerLink, JoinLink: options.JoinLink}, &config, func(kind, reason string) (string, error) {
				if kind == "name" {
					return ask("Your name (shown to the owner and other guests; not verified): ")
				}
				if reason != "" {
					reason += ".\n"
				}
				return ask(reason + "Owner link (npm run owner-link on the host) or share link: ")
			})
			if err != nil {
				return tuiFailure(err)
			}
			if err := host.SaveConfig(config, host.ConfigDirectory()); err != nil {
				return tuiFailure(err)
			}
		}
		target := options.URL
		var transport host.Transport = host.CookieTransport(target, admission.Credentials)
		if radius != nil {
			target, transport = radius.URL, radius.Transport
		}
		local := host.LauncherPreferences(sources, os.LookupEnv, runtime.GOOS)
		var failures []string
		ended := false
		exited := make(chan struct{})
		var once sync.Once
		mu.Lock()
		finish = func() { once.Do(func() { close(exited) }) }
		mu.Unlock()
		clientOptions := host.ClientOptions{
			Terminal: pitui.NewStdioProcessTerminal(), URL: target, Transport: transport, Credentials: admission.Credentials,
			Mouse: local.Mouse, Role: admission.Role, Facets: TUIFacets(plugins),
			Actions: host.LauncherActions(host.LauncherActionOptions{
				URL: target, Credentials: admission.Credentials, Transport: transport, Role: admission.Role, Downloads: local.Downloads, Editor: host.EditorArgv(local.Editor),
			}),
			Preferences: &host.PreferenceSource{Current: preferences, Subscribe: func(listener func(next prefs.TuiLocalPreferences)) func() {
				return host.WatchLocalPreferences(sources, listener, preferences)
			}},
			OnFailure: func(message string) { mu.Lock(); failures = append(failures, message); mu.Unlock() },
			// The server no longer admits the stored cookie (a refused reconnect, confirmed by the launcher itself).
			OnAccessEnded: func() { mu.Lock(); ended = true; mu.Unlock() },
		}
		if radius != nil {
			clientOptions.Poll = host.RadiusPollInterval
		}
		clientOptions.OnExit = func() {
			mu.Lock()
			client := current
			mu.Unlock()
			go func() {
				if client != nil {
					client.Close()
				}
				finish()
			}()
		}
		client, startErr := host.StartClient(clientOptions)
		if startErr != nil {
			return tuiFailure(startErr)
		}
		mu.Lock()
		current = client
		mu.Unlock()
		<-exited
		mu.Lock()
		current = nil
		messages, wasEnded, wasStopping := failures, ended, stopping
		mu.Unlock()
		for _, message := range messages {
			fmt.Fprintln(os.Stderr, message)
		}
		if !wasEnded || wasStopping {
			break
		}
		if radius != nil {
			fmt.Fprintln(os.Stderr, "The server no longer admits you through Radius.")
			break
		}
		// Launcher-drawn, on the restored terminal: the same words as the web page, then a way back in.
		fmt.Fprintln(os.Stderr, host.EndedNotice(admission.Role))
		// A local sign-in ended (all devices signed out): the data directory's owner key signs in again.
		if admission.Local {
			continue
		}
		next, err := host.ResumeAfterEnded(admission.Role, options.URL, &config, ask)
		if saveErr := host.SaveConfig(config, host.ConfigDirectory()); saveErr != nil && err == nil {
			err = saveErr
		}
		if err != nil {
			return tuiFailure(err)
		}
		if next == nil {
			break
		}
		options.JoinLink, options.OwnerLink = next.JoinLink, next.OwnerLink
	}
	return 0
}

// tuiFailure prints a launcher error (its own text; server join errors arrive sanitized to one plain line).
func tuiFailure(err error) int {
	fmt.Fprintf(os.Stderr, "p2 TUI could not start: %s\n", err.Error())
	return 1
}
