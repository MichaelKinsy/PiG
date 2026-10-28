package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// writeDiagnoseDefaultModel reports the default model a session starts with and the settings file each part came from. It
// loads settings under the project trust decision, so an untrusted project's defaultModel is named as ignored instead of
// reported as the default, and it resolves the default with the startup rule (Pi's findInitialModel), so a default that
// names no available model shows the model startup falls back to.
//
// pig additive (D41): diagnose reports the effective default and its source, not the setting alone.
func writeDiagnoseDefaultModel(w io.Writer, cwd, agentDir string, trust projectTrustReport, active *inspectedPiglet) {
	trusted := trust.Trusted()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir, ProjectTrusted: &trusted})
	if err != nil {
		_, _ = fmt.Fprintf(w, "  (could not load services: %v)\n", err)
		return
	}
	defer services.Close()
	settings := services.Settings()
	globalFile := filepath.Join(agentDir, "settings.json")
	projectFile := filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json")
	project := services.SettingsManager().GetProjectSettings()
	sourceOf := func(projectValue string) string {
		if projectValue != "" {
			return projectFile
		}
		return globalFile
	}
	_, _ = fmt.Fprintf(w, "  project trust:  %s (%s)\n", trust.State, trust.Reason)
	if !trusted {
		ignored := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true).GetProjectSettings()
		if ignored.DefaultModel != "" || ignored.DefaultProvider != "" {
			_, _ = fmt.Fprintf(w, "  ignored:        %s sets defaultProvider %q and defaultModel %q; it does not apply while the project is untrusted\n", projectFile, ignored.DefaultProvider, ignored.DefaultModel)
		}
	}
	switch {
	case settings.DefaultModel == "" && settings.DefaultProvider == "":
		_, _ = fmt.Fprintf(w, "  configured:     none (no defaultProvider or defaultModel in %s)\n", globalFile)
	case settings.DefaultProvider == "":
		_, _ = fmt.Fprintf(w, "  configured:     defaultModel %q from %s, which startup ignores: Pi reads it only together with defaultProvider\n", settings.DefaultModel, sourceOf(project.DefaultModel))
	case settings.DefaultModel == "":
		_, _ = fmt.Fprintf(w, "  configured:     defaultProvider %q from %s, which startup ignores: Pi reads it only together with defaultModel\n", settings.DefaultProvider, sourceOf(project.DefaultProvider))
	default:
		_, _ = fmt.Fprintf(w, "  configured:     %s/%s (defaultProvider from %s, defaultModel from %s)\n", settings.DefaultProvider, settings.DefaultModel, sourceOf(project.DefaultProvider), sourceOf(project.DefaultModel))
	}
	runtime := services.ModelRuntime()
	selected, err := findInitialModel(newLazyStartupModelRuntime(runtime.RuntimeModels, runtime.HasConfiguredAuth), settings.DefaultProvider, settings.DefaultModel)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(w, "  effective:      (could not resolve: %v)\n", err)
	case selected == nil:
		_, _ = fmt.Fprintf(w, "  effective:      none: no model has configured auth (run `pig login` or set a provider key)\n")
	case selected.Provider == settings.DefaultProvider && selected.ID == settings.DefaultModel:
		_, _ = fmt.Fprintf(w, "  effective:      %s/%s\n", selected.Provider, selected.ID)
	default:
		_, _ = fmt.Fprintf(w, "  effective:      %s/%s (startup falls back to it)\n", selected.Provider, selected.ID)
		if settings.DefaultProvider != "" && settings.DefaultModel != "" {
			_, _ = fmt.Fprintf(w, "                  %s/%s is not an available model.", settings.DefaultProvider, settings.DefaultModel)
			if strings.HasPrefix(settings.DefaultModel, settings.DefaultProvider+"/") {
				_, _ = fmt.Fprintf(w, " Pi reads defaultModel as a model id and does not split a provider prefix: set defaultProvider %q and defaultModel %q.", settings.DefaultProvider, strings.TrimPrefix(settings.DefaultModel, settings.DefaultProvider+"/"))
			}
			_, _ = fmt.Fprintln(w)
		}
	}
	if spec := pigletModelSpec(activePigletOf(active)); spec != "" {
		_, _ = fmt.Fprintf(w, "  active Piglet:  %s sets model %s, which replaces this default for its session\n", active.Name, spec)
	}
	_, _ = fmt.Fprintln(w, "  A --model flag, an enabledModels scope or a resumed session can replace this default.")
}

// activePigletOf returns the loaded Piglet of active, or nil.
func activePigletOf(active *inspectedPiglet) *piglet.Piglet {
	if active == nil {
		return nil
	}
	return active.Piglet
}
