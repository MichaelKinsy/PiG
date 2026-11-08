# The PiG Chord tutorial

A shared todo app built with Chord's app kit in Go, one chapter at a time. It is the Go form of Pi's tutorial
(`packages/chord/docs/tutorial/book.md`): the server and the owner's sign-in first, then the chapters after them, each in
PiG's API. The plugins are TypeScript, as in Pi, and run in the Node host; the app that starts the server and its workers is Go.

Every block below is quoted from the example (`examples/chord-todo`). `examples/chord-todo/tutorial_test.go` fails when a
quoted block no longer matches its file, runs the example's steps (the files are what runs), and fails when the output the
book shows is not what they print.

# One Server

An app built with the kit has a fixed shape: its API (`api/v<n>`), its built-in plugins in a root directory, and a start
program. Everything else is a plugin. This chapter writes the base code and starts the server.

## The principals

A **principal** says who is calling. The app defines it; the kit needs a principal for the server, one for each of its
workers (an app without worker kinds leaves it out) and one for the owner, whom the owner's sign-in names. A principal is a
JSON value, so the Go app writes it as one:

<!-- quote: main.go -->
```go
// The app's principals: its server and its owner (chapter II adds its list workers, chapter VII its users).
var (
	serverPrincipal data.Principal = delta.JsonObjectOf("kind", "server")
	ownerPrincipal  data.Principal = delta.JsonObjectOf("kind", "owner")
)
```

A principal travels with every call and is part of objects' identities, so it stays small: a kind and an id. A user's
profile, if the app had one, would be data in an object keyed by the id.

## The API

Plugins never import Chord. They import the app's **API**, a module the app names with a version in its path
(`@todo/app/v1`). It re-exports the part of Chord the app supports and adds what is the app's own:

<!-- quote: api/v1.ts -->
```ts
// The todo app's API, version 1: the module its plugins import as `@todo/app/v1`. It re-exports the part of Chord the app
// supports; a plugin never imports Chord itself.
export * from "@earendil-works/chord";
```

The API is a **host package**: a plugin's build leaves it out, and each process hands the plugin its own instance when it
evaluates it. A version in the path lets the app ship `v2` beside `v1` later; one facet uses one version. The Go app names
the module and the file that holds it (Pi's workspace package and its `package.json` exports have no Go counterpart: the
program names each module's file):

<!-- quote: main.go -->
```go
// api is the app's API: its name, its versions (newest first), and this process's modules of them.
func api() app.AppAPI {
	return app.AppAPI{
		Name:     "@todo/app",
		Versions: []string{"v1"},
		Modules:  plugins.HostPackages{"@todo/app/v1": filepath.Join(here, "api", "v1.ts")},
	}
}
```

## The start program

`StartServer` takes what the app decides and does the rest. The program names the facet kinds (which files a plugin may have)
and starts the server with its API, kinds and principals:

<!-- quote: main.go -->
```go
// kinds are the app's facet kinds: a plugin has one file per kind it runs on (`server.ts`, `worker.ts`).
func kinds() []app.AppKind {
	return []app.AppKind{
		{Name: "server", Platform: model.PlatformNode},
		{Name: "worker", Platform: model.PlatformNode},
	}
}
```

<!-- quote: main.go -->
```go
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
```

`Listen` asks for a free port on loopback, as the example does; a real app picks a port. A Go app needs no separate entry file
for a worker: a worker is a program that calls the kit's `StartWorker`, usually the same executable (chapter II).

## Running it

Step 1 of `run` starts the app, tries the page without signing in, then does what a browser does with the owner link:

<!-- quote: main.go -->
```go
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
```

<!-- output: main -->
```text
1. owner link: http://127.0.0.1:<port>/_chord/owner#<token>
   data directory: cache, logs, owner-token, plugins, server, workers
   GET / signed out: 404
   the owner signs in: 200 chord-session=…
   GET / signed in: 404
```

`StartServer` made the data directory: `server/` (its store, the cookie secret, its files), `workers/` (one directory per
worker), `plugins/` (the user's plugin root, beside the app's built-in one), `cache/` (the workers' bases), `logs/` and
`owner-token`. No page or route answers yet, so both requests get 404; the chapters after this one add them.

The **owner link** (`/_chord/owner#<token>`) is how the owner gets in: a single-use token, valid ten minutes, made at each
start. It sits in the URL's fragment, which browsers never send, so it never reaches a log; the link's page posts it and
receives a session cookie. A command-line client on the same machine reads `owner-token` instead (mode 0600). Nothing is the
owner because it comes from loopback: any local process could.
