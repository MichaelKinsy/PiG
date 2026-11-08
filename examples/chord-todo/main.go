// Command chord-todo is the tutorial's app (docs/chord/tutorial.md): a todo app built with the app kit. Each numbered step of
// `run` is the chapter of that number. The tutorial's test (tutorial_test.go) quotes this file in the book, runs `run` and
// compares what it prints with the book.
//
//	go run ./examples/chord-todo [--serve]
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// here is this directory: the API's modules are files of it.
var _, thisFile, _, _ = runtime.Caller(0)
var here = filepath.Dir(thisFile)

// The app's principals: its server and its owner (chapter II adds its list workers, chapter VII its users).
var (
	serverPrincipal data.Principal = delta.JsonObjectOf("kind", "server")
	ownerPrincipal  data.Principal = delta.JsonObjectOf("kind", "owner")
)

// api is the app's API: its name, its versions (newest first), and this process's modules of them.
func api() app.AppAPI {
	return app.AppAPI{
		Name:     "@todo/app",
		Versions: []string{"v1"},
		Modules:  plugins.HostPackages{"@todo/app/v1": filepath.Join(here, "api", "v1.ts")},
	}
}

// kinds are the app's facet kinds: a plugin has one file per kind it runs on (`server.ts`, `worker.ts`).
func kinds() []app.AppKind {
	return []app.AppKind{
		{Name: "server", Platform: model.PlatformNode},
		{Name: "worker", Platform: model.PlatformNode},
	}
}

// startTodo starts the todo app on a free loopback port, with its data in dataDir and its built-in plugins in root.
func startTodo(ctx context.Context, dataDir, root string) (*app.App, error) {
	return app.StartServer(app.AppOptions{
		Name:    "todo",
		DataDir: dataDir,
		Root:    root,
		Listen:  &chordhttp.ListenAddress{Host: "127.0.0.1", Port: 0},
		API:     api(),
		Kinds:   kinds(),
		Principal: app.AppPrincipals{
			Server: serverPrincipal,
			Owner:  ownerPrincipal,
		},
		OnError: func(err app.AppError) { fmt.Fprintln(os.Stderr, "todo:", err.Kind, err.Error) },
	}, ctx)
}

// run is the tutorial's steps, printing what each does to out; with serve it then keeps the app running until interrupted. The data
// directory and the plugins root are new directories in base.
func run(ctx context.Context, base string, out io.Writer, serve bool) error {
	dataDir, root := filepath.Join(base, "data"), filepath.Join(base, "plugins")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	// 1. Start the app: the kit makes the data directory, the server's store and host, HTTP and the owner link. Nobody is signed
	//    in yet, so the page answers 401. The owner opens the link: its page posts the token from the URL's fragment and gets
	//    a session cookie.
	server, err := startTodo(ctx, dataDir, root)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close(ctx) }()
	link, err := url.Parse(server.OwnerLink())
	if err != nil {
		return err
	}
	origin := link.Scheme + "://" + link.Host
	fmt.Fprintln(out, "1. owner link:", "http://127.0.0.1:<port>"+link.Path+"#<token>")
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	fmt.Fprintln(out, "   data directory:", strings.Join(names, ", "))
	signedOut, err := http.Get(origin)
	if err != nil {
		return err
	}
	_ = signedOut.Body.Close()
	fmt.Fprintln(out, "   GET / signed out:", signedOut.StatusCode)
	signIn, err := http.Post(origin+app.OwnerPath, "application/json", strings.NewReader(`{"token":"`+link.Fragment+`"}`))
	if err != nil {
		return err
	}
	_ = signIn.Body.Close()
	cookies := signIn.Cookies()
	if len(cookies) == 0 {
		return fmt.Errorf("the owner's sign-in set no cookie (status %d)", signIn.StatusCode)
	}
	cookie := cookies[0]
	fmt.Fprintln(out, "   the owner signs in:", signIn.StatusCode, cookie.Name+"=…")
	request, err := http.NewRequest(http.MethodGet, origin, nil)
	if err != nil {
		return err
	}
	request.AddCookie(cookie)
	signedIn, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	_ = signedIn.Body.Close()
	fmt.Fprintln(out, "   GET / signed in:", signedIn.StatusCode)

	if serve {
		fmt.Fprintln(out, "serving; the owner link:", server.OwnerLink())
		interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		<-interrupted.Done()
	}
	return nil
}

func main() {
	base, err := os.MkdirTemp("", "todo-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "todo:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(base) }()
	if err := run(context.Background(), base, os.Stdout, len(os.Args) > 1 && os.Args[1] == "--serve"); err != nil {
		fmt.Fprintln(os.Stderr, "todo:", err)
		os.Exit(1)
	}
}
