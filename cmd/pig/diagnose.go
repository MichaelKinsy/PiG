package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// runDiagnose writes a human-readable diagnostic report covering
// version, build, config root, auth providers, registered tools,
// default model, and relevant environment variables. This Pig-specific
// report is designed for paste-
// into-bug-report ergonomics, not machine parsing.
func runDiagnose(w io.Writer, binaryPath string) {
	hr := strings.Repeat("─", 60)

	// ─── Build info ──────────────────────────────────────
	_, _ = fmt.Fprintf(w, "pig diagnostics\n%s\n", hr)
	_, _ = fmt.Fprintf(w, "pig-version:       %s\n", PigVersion)
	_, _ = fmt.Fprintf(w, "upstream-pin:      %s\n", UpstreamVersion)
	_, _ = fmt.Fprintf(w, "build:             %s\n", buildIdentity())
	_, _ = fmt.Fprintf(w, "go runtime:        %s\n", runtime.Version())
	_, _ = fmt.Fprintf(w, "platform:          %s/%s\n", runtime.GOOS, runtime.GOARCH)
	_, _ = fmt.Fprintf(w, "binary:            %s\n", binaryPath)

	// ─── Extension SDKs ──────────────────────────────────
	// Startup warns here when staging fails and points at this report, so the
	// detail it promises has to exist. A stale staged SDK is otherwise invisible
	// from the running process: its only symptom is an extension carrying a bug
	// that was already fixed.
	_, _ = fmt.Fprintf(w, "\nextension SDKs (staged for out-of-tree builds)\n%s\n", hr)
	if stages, err := pigsdk.Status(codingagent.ConfigRoot()); err != nil {
		_, _ = fmt.Fprintf(w, "unavailable:       %v\n", err)
	} else {
		for _, st := range stages {
			state := "current"
			if !st.Current {
				state = "STALE: extensions will rebuild on next start"
				if st.OnDisk == "" {
					state = "not staged: will stage on next start"
				}
			}
			_, _ = fmt.Fprintf(w, "%-18s %s (embedded %s, on disk %s)\n",
				st.Lang+":", state, shortHash(st.Embedded), shortHash(st.OnDisk))
		}
	}

	// ─── Paths ────────────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\npaths\n%s\n", hr)
	configRoot := codingagent.ConfigRoot()
	_, _ = fmt.Fprintf(w, "config root:       %s\n", configRoot)
	_, _ = fmt.Fprintf(w, "agent dir:         %s\n", agentDirForModel())
	_, _ = fmt.Fprintf(w, "auth.json:         %s\n", agentDirForModel()+"/auth.json")
	if cwd, err := os.Getwd(); err == nil {
		_, _ = fmt.Fprintf(w, "cwd:               %s\n", cwd)
	}

	// ─── Auth ─────────────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nauth (logged-in providers)\n%s\n", hr)
	authPath := agentDirForModel() + "/auth.json"
	if _, err := os.Stat(authPath); err != nil {
		_, _ = fmt.Fprintf(w, "  (no auth.json: run `pig login`)\n")
	} else if storage, err := ai.NewAuthStorage(authPath); err != nil {
		_, _ = fmt.Fprintf(w, "  error opening auth: %v\n", err)
	} else if creds, err := storage.Load(); err != nil {
		_, _ = fmt.Fprintf(w, "  error loading auth: %v\n", err)
	} else if len(creds) == 0 {
		_, _ = fmt.Fprintf(w, "  (none)\n")
	} else {
		providers := make([]string, 0, len(creds))
		for p := range creds {
			providers = append(providers, p)
		}
		slices.Sort(providers)
		for _, p := range providers {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", p, credentialKindLabel(creds[p]))
		}
	}

	// ─── Tools ────────────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\ntools (built-in coding set)\n%s\n", hr)
	cwd, _ := os.Getwd()
	stubSettings := codingagent.Settings{}
	t := tools.CreateCodingTools(cwd, stubSettings, "")
	names := make([]string, 0, len(t))
	for _, tool := range t {
		names = append(names, tool.Name())
	}
	slices.Sort(names)
	for _, n := range names {
		_, _ = fmt.Fprintf(w, "  %s\n", n)
	}

	// ─── Models registry ──────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nmodels (registered providers)\n%s\n", hr)
	provs := map[string]int{}
	for _, m := range ai.ListModels("") {
		provs[m.Provider]++
	}
	provNames := make([]string, 0, len(provs))
	for p := range provs {
		provNames = append(provNames, p)
	}
	slices.Sort(provNames)
	for _, p := range provNames {
		_, _ = fmt.Fprintf(w, "  %s: %d models\n", p, provs[p])
	}
	if len(provNames) == 0 {
		_, _ = fmt.Fprintf(w, "  (registry empty: likely a build-time gen-models miss)\n")
	}

	// ─── Default model ────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\ndefault model\n%s\n", hr)
	services, servErr := coding.NewServices(coding.ServicesOptions{AgentDir: agentDirForModel()})
	if servErr != nil {
		_, _ = fmt.Fprintf(w, "  (could not load services: %v)\n", servErr)
	} else {
		settings := services.Settings()
		switch {
		case settings.DefaultProvider != "" && settings.DefaultModel != "":
			_, _ = fmt.Fprintf(w, "  %s/%s (from settings)\n", settings.DefaultProvider, settings.DefaultModel)
		case settings.DefaultModel != "":
			_, _ = fmt.Fprintf(w, "  %s (from settings: no provider)\n", settings.DefaultModel)
		default:
			_, _ = fmt.Fprintf(w, "  (none: falls back to compiled-in default)\n")
		}
	}

	// ─── Env vars ─────────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nenvironment\n%s\n", hr)
	for _, k := range []string{
		"PIG_CONFIG", "PIG_AGENT_DIR", "PIG_NO_NOTIFY",
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
		"GROQ_API_KEY", "CEREBRAS_API_KEY", "GITHUB_TOKEN",
		"PATH", "SHELL", "TERM", "COLORTERM", "NO_COLOR",
		"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY",
	} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		_, _ = fmt.Fprintf(w, "  %-22s %s\n", k+":", maskEnv(k, v))
	}
}

// credentialKindLabel returns a one-word descriptor of how a
// credential authenticates.
func credentialKindLabel(c ai.Credential) string {
	return string(c.Type)
}

// maskEnv truncates secret-shaped env vars to first 6 + "…" so
// `pig --diagnose` output is bug-report-pasteable. PATH and
// non-secrets pass through verbatim.
func maskEnv(key, value string) string {
	switch {
	case strings.Contains(key, "API_KEY"),
		strings.Contains(key, "TOKEN"),
		strings.Contains(key, "SECRET"):
		if len(value) <= 6 {
			return "(set)"
		}
		return value[:6] + "…(masked)"
	}
	return value
}

// shortHash renders an SDK content hash for a report, or a placeholder when the
// SDK has never been staged.
func shortHash(h string) string {
	if h == "" {
		return "none"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
