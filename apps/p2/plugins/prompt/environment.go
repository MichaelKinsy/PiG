package prompt

import (
	"context"
	"errors"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	durablenode "github.com/MichaelKinsy/PiG/durable/env/node"
)

// Ports apps/p2/plugins/prompt/environment.ts: prompt resources of a session (`AGENTS.md` files and skills) loaded through
// its environment and reloaded when a watch reports a change instead of by polling.

// loadTimeout is how long a load may take: one that takes longer (a slow or stalled remote) fails and is retried at the
// next change.
const loadTimeout = 60 * time.Second

// skillExcludes: skills directories are scanned without hidden entries and `node_modules`; the watch skips them too.
var skillExcludes = &durableenv.WatchExclude{Hidden: true, Names: []string{"node_modules"}}

type envFiles struct {
	env   durableenv.ExecutionEnv
	paths PathRules
}

// EnvFiles are project files read through a session's environment.
func EnvFiles(env durableenv.ExecutionEnv, paths PathRules) Files {
	return envFiles{env: env, paths: paths}
}

func (f envFiles) Paths() PathRules { return f.paths }
func (envFiles) Local() bool        { return false }

func notFound(err error) bool {
	var fileError *durableenv.FileError
	return errors.As(err, &fileError) && fileError.Code == durableenv.FileErrorNotFound
}

func (f envFiles) ReadText(ctx context.Context, path string) (string, bool, error) {
	text, err := f.env.ReadTextFile(ctx, path)
	if err == nil {
		return text, true, nil
	}
	if notFound(err) {
		return "", false, nil
	}
	return "", false, err
}

func (f envFiles) Entries(ctx context.Context, path string) ([]Entry, bool, error) {
	listed, err := f.env.ListDir(ctx, path)
	if err != nil {
		if notFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	entries := make([]Entry, len(listed))
	for at, entry := range listed {
		kind := "other"
		switch entry.Kind {
		case durableenv.FileKindFile:
			kind = "file"
		case durableenv.FileKindDirectory:
			kind = "directory"
		}
		entries[at] = Entry{Name: entry.Name, Kind: kind}
	}
	return entries, true, nil
}

// Source is where a session's prompt resources are, and what to do with them.
type Source struct {
	// Env is the session's environment, local or remote.
	Env durableenv.ExecutionEnv
	// Directory is p2's data directory (local), for the global `AGENTS.md`.
	Directory string
	Cwd       string
	// Paths are the path rules of the environment's system.
	Paths func(ctx context.Context) (PathRules, error)
	// SkillsRoot is the global skills directory in the environment: `<data>/skills` locally, `~/.pi/p2/skills` remotely.
	SkillsRoot func(ctx context.Context) (string, error)
	// PromptsRoot is the global prompt templates directory: `<data>/prompts` locally, `~/.pi/p2/prompts` remotely (nil:
	// `<data>/prompts`).
	PromptsRoot func(ctx context.Context) (string, error)
	// Remote is a remote env's name, for messages.
	Remote string
	// Fallback is how often to load while the environment cannot be watched.
	Fallback time.Duration
	// Apply gets a newly loaded valid state, or the error of a failed load (the last valid state stays).
	Apply func(prompt *LoadedPrompt, err string)
	// OnLoaded is called after every successful load (may be nil).
	OnLoaded func()
}

// NewObserver is the prompt resources of a session, watched: the global `AGENTS.md` stays local and is watched locally.
func NewObserver(source Source) *p2runtime.Observer {
	promptsRoot := source.PromptsRoot
	if promptsRoot == nil {
		promptsRoot = func(context.Context) (string, error) { return LocalPaths.Join(source.Directory, "prompts"), nil }
	}
	return p2runtime.NewObserver(
		[]p2runtime.ObservedFiles{
			{
				Env: source.Env,
				Targets: func(ctx context.Context) ([]durableenv.WatchTarget, error) {
					paths, err := source.Paths(ctx)
					if err != nil {
						return nil, err
					}
					candidates, err := AgentsCandidates(paths, source.Cwd)
					if err != nil {
						return nil, err
					}
					skills, err := source.SkillsRoot(ctx)
					if err != nil {
						return nil, err
					}
					prompts, err := promptsRoot(ctx)
					if err != nil {
						return nil, err
					}
					targets := make([]durableenv.WatchTarget, 0, len(candidates)+4)
					for _, path := range candidates {
						targets = append(targets, durableenv.WatchTarget{Path: path})
					}
					return append(targets,
						durableenv.WatchTarget{Path: skills, Recursive: true, Exclude: skillExcludes},
						durableenv.WatchTarget{Path: paths.Join(source.Cwd, ".pi", "skills"), Recursive: true, Exclude: skillExcludes},
						// Prompt templates: `.md` files directly in these directories.
						durableenv.WatchTarget{Path: prompts},
						durableenv.WatchTarget{Path: paths.Join(source.Cwd, ".pi", "prompts")},
					), nil
				},
			},
			{
				Env: durablenode.NewNodeExecutionEnv(durablenode.NodeExecutionEnvOptions{Cwd: source.Directory}),
				Targets: func(context.Context) ([]durableenv.WatchTarget, error) {
					return []durableenv.WatchTarget{{Path: LocalPaths.Join(source.Directory, "AGENTS.md")}}, nil
				},
			},
		},
		func(ctx context.Context, retrying bool) bool {
			loaded, err := load(ctx, source, promptsRoot)
			if err != nil {
				// Possibly a file caught half-written: read once more before reporting.
				if !retrying {
					return true
				}
				if source.Remote == "" {
					source.Apply(nil, "Could not load prompt resources; keeping last valid state")
				} else {
					source.Apply(nil, "Could not load prompt resources from \""+source.Remote+"\": "+err.Error()+"; keeping last valid state")
				}
				return false
			}
			source.Apply(loaded, "")
			if source.OnLoaded != nil {
				source.OnLoaded()
			}
			return false
		},
		source.Fallback,
	)
}

func load(ctx context.Context, source Source, promptsRoot func(context.Context) (string, error)) (*LoadedPrompt, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, loadTimeout, errors.New("Loading timed out"))
	defer cancel()
	paths, err := source.Paths(ctx)
	if err != nil {
		return nil, err
	}
	skills, err := source.SkillsRoot(ctx)
	if err != nil {
		return nil, err
	}
	prompts, err := promptsRoot(ctx)
	if err != nil {
		return nil, err
	}
	loaded, err := LoadPromptResources(ctx, source.Directory, source.Cwd, EnvFiles(source.Env, paths), skills, prompts)
	if err != nil && ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	return loaded, err
}
