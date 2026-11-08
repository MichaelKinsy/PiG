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
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
	"github.com/MichaelKinsy/PiG/chord/telemetrysqlite"
)

// here is this directory: the API's modules are files of it.
var _, thisFile, _, _ = runtime.Caller(0)
var here = filepath.Dir(thisFile)

// The app's principals: its server, its owner, one for each list's worker, and a user who signed in with a name (chapter VII).
var (
	serverPrincipal data.Principal = delta.JsonObjectOf("kind", "server")
	ownerPrincipal  data.Principal = delta.JsonObjectOf("kind", "owner")
)

// workerPrincipal is the principal of the worker of the list at address.
func workerPrincipal(address contracts.Address) data.Principal {
	return delta.JsonObjectOf("kind", "worker", "list", address.Key)
}

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
		{Name: "server", Platform: plugins.PlatformNode},
		{Name: "worker", Platform: plugins.PlatformNode},
		{Name: "tui", Platform: plugins.PlatformNode, Packages: plugins.HostPackages{"@todo/app/v1/tui": filepath.Join(here, "client", "tui.ts")}},
		{Name: "web", Platform: plugins.PlatformBrowser, Packages: plugins.HostPackages{
			"react": "", "react/jsx-runtime": "",
			"@todo/app/v1/web": filepath.Join(here, "client", "web.ts"),
		}},
	}
}

// startTodo starts the todo app on a free loopback port, with its data in dataDir, its built-in plugins in root, recording to store (the
// telemetry store in dataDir).
func startTodo(ctx context.Context, dataDir, root string, store *telemetrysqlite.SqliteTelemetry) (*app.App, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return app.StartServer(app.AppOptions{
		Name:    "todo",
		DataDir: dataDir,
		Root:    root,
		Listen:  &chordhttp.ListenAddress{Host: "127.0.0.1", Port: 0},
		API:     api(),
		Kinds:   kinds(),
		Principal: app.AppPrincipals{
			Server: serverPrincipal,
			Worker: workerPrincipal,
			Owner:  ownerPrincipal,
		},
		Workers: []app.AppWorkerKind{{
			Kind:  listKind,
			Entry: executable,
			Env:   map[string]string{workerEnv: "1"},
			Start: func(contracts.Address, context.Context) (delta.JsonValue, error) { return delta.NewJsonObject(0), nil },
			// Who may reach a list's worker: everyone signed in, and the server itself.
			Authorize: func(principal data.Principal, _ contracts.Address) bool {
				return principal.Value("kind") != "worker"
			},
		}},
		// The kit's pick-a-name login (a Go app loads it itself, where Pi's app has a one-line plugin) and the lists' routes.
		// The store the server writes: its own spans and metrics, and what the workers and recording pages send.
		Telemetry: store,
		// Who a span was for: a short key and a user's name.
		PrincipalAttributes: principalAttributes,
		Facets: []host.HostFacet{
			{Name: "chord.app.ephemeral", Facet: app.EphemeralLoginFacet()},
			{Name: "@todo/routes", Facet: routes()},
			{Name: "@todo/traces", Facet: tracesFacet(dataDir)},
		},
		// The page: the app's own HTML around the assets the kit made.
		Web:     &app.AppWeb{Entry: filepath.Join(here, "client", "page.ts"), Page: page, Paths: []string{"/", "/traces"}, Conditions: []string{"source"}},
		Tui:     &app.AppTui{Entry: filepath.Join(here, "client", "terminal.ts"), Conditions: []string{"source"}},
		OnError: func(err app.AppError) { fmt.Fprintln(os.Stderr, "todo:", err.Kind, err.Error) },
	}, ctx)
}

// page is the page's template: the app's own HTML around the assets the kit made (the base bundle, the plugins' scripts and styles).
func page(request chordhttp.HttpRequest, assets nodesurfaces.PageAssets) (string, error) {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Todo</title>` + nodesurfaces.PageScripts(assets, request.Nonce()) +
		`</head><body><div id="root"></div></body></html>`, nil
}

// workerEnv is set in a worker's environment: the program is the worker of a list then, not the app.
const workerEnv = "TODO_ROLE_WORKER"

// startWorker runs this process as a list's worker: it opens the list's store in its directory, connects to the server and loads
// the plugins' worker facets. It does not return.
func startWorker() {
	app.StartWorker(app.WorkerOptions{
		API: api(),
		// A worker runs the plugins' worker facets only: the page's and the terminal's packages are not its.
		Kinds:       kinds()[:2],
		WorkerKinds: []*contracts.HostKind{listKind},
		OnError:     func(err any) { fmt.Fprintln(os.Stderr, "list:", err) },
	})
}

// run is the tutorial's steps, printing what each does to out; with serve it then keeps the app running until interrupted. The data
// directory and the plugins root are new directories in base.
func run(ctx context.Context, base string, out io.Writer, serve bool) error {
	dataDir, root := filepath.Join(base, "data"), filepath.Join(base, "plugins")
	if err := os.CopyFS(root, os.DirFS(filepath.Join(here, "plugins"))); err != nil {
		return err
	}

	// 1. Start the app: the kit makes the data directory, the server's store and host, HTTP and the owner link. Nobody is signed
	//    in yet, so the page answers 401. The owner opens the link: its page posts the token from the URL's fragment and gets
	//    a session cookie.
	store, err := openTelemetry(telemetryFile(dataDir))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	server, err := startTodo(ctx, dataDir, root, store)
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

	s := &session{ctx: ctx, out: out, app: server, dataDir: dataDir, store: store, root: root, base: base, origin: origin}
	defer s.stop()
	for _, step := range []func() error{s.listsStep, s.routesStep, s.pageStep, s.terminalStep, s.themeStep, s.userStep, s.languagesStep, s.reloadStep, s.exportStep, s.telemetryStep} {
		if err := step(); err != nil {
			return err
		}
	}

	if serve {
		fmt.Fprintln(out, "serving; the owner link:", server.OwnerLink())
		interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		<-interrupted.Done()
	}
	return nil
}

func main() {
	if os.Getenv(workerEnv) != "" {
		startWorker()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "trace" {
		if err := traceCommand(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "todo:", err)
			os.Exit(1)
		}
		return
	}
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
