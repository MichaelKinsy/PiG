//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's reload never unregisters an extension's provider: ModelRuntime holds the registration and its streamSimple closure until the process ends or an
// extension unregisters or replaces the name (agent-session.ts:3815-3855, model-runtime.ts:965-993). Probed on Pi 1.5.2: after the extension file was
// removed and /reload ran twice, the provider was still listed and still answered from the old closure. These tests pin what a subprocess runtime needs for
// that: the removed generation's process outlives the reload while it owns a registration, and it is a bounded, owned resource.

// lingerRegistry stands for the model registry: it records registrations and unregistrations and streams through the configuration a provider registered.
type lingerRegistry struct {
	mu           sync.Mutex
	configs      map[string]extension.ProviderConfig
	unregistered []string
}

func (r *lingerRegistry) register(name string, config extension.ProviderConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.configs == nil {
		r.configs = map[string]extension.ProviderConfig{}
	}
	r.configs[name] = config
	return nil
}

func (r *lingerRegistry) unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.configs, name)
	r.unregistered = append(r.unregistered, name)
}

func (r *lingerRegistry) listed(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.configs[name]
	return ok
}

// answer streams one request through the provider the registry holds, and returns the reply text and the stop reason.
func (r *lingerRegistry) answer(t *testing.T, name string) (string, ai.StopReason) {
	t.Helper()
	r.mu.Lock()
	config, ok := r.configs[name]
	r.mu.Unlock()
	if !ok || config.StreamSimple == nil {
		t.Fatalf("provider %q is not registered with a stream", name)
	}
	stream := config.StreamSimple(&ai.Model{ID: "m", ProviderMeta: ai.ProviderMetadata{ProviderID: name, API: "linger-api"}}, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{APIKey: "linger-key"})
	message := stream.Result()
	if message.StopReason == ai.StopReasonError {
		return message.ErrorMessage, message.StopReason
	}
	text, _ := message.Content[0].(ai.TextContent)
	return text.Text, message.StopReason
}

type lingerRig struct {
	host     *Host
	registry *lingerRegistry
	configs  []ExtConfig
	ctx      context.Context
}

func newLingerRig(t *testing.T, names ...string) *lingerRig {
	t.Helper()
	rig := &lingerRig{registry: &lingerRegistry{}}
	for _, name := range names {
		path, err := filepath.Abs(filepath.Join("testdata", name+".mjs"))
		if err != nil {
			t.Fatal(err)
		}
		rig.configs = append(rig.configs, ExtConfig{Name: name, Source: path, Enabled: true})
	}
	rig.host = NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	rig.host.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), rig.configs...), nil })
	t.Cleanup(func() { rig.host.Shutdown("test done") })
	var cancel context.CancelFunc
	rig.ctx, cancel = buildContext(t)
	t.Cleanup(cancel)
	return rig
}

// reload runs Host.Reload and binds the registry as the session does after every reload.
func (r *lingerRig) reload(t *testing.T) {
	t.Helper()
	if _, err := r.host.Reload(r.ctx); err != nil {
		t.Fatal(err)
	}
	if failures := r.host.SetProviderCallbacks(r.registry.register, r.registry.unregister); len(failures) != 0 {
		t.Fatalf("registration failures: %v", failures[0].Error)
	}
}

func (r *lingerRig) remove(name string) {
	r.configs = slicesDeleteName(r.configs, name)
}

func slicesDeleteName(configs []ExtConfig, name string) []ExtConfig {
	out := configs[:0:0]
	for _, cfg := range configs {
		if cfg.Name != name {
			out = append(out, cfg)
		}
	}
	return out
}

func (r *lingerRig) lingering() []*managedExt {
	r.host.mu.Lock()
	defer r.host.mu.Unlock()
	out := make([]*managedExt, 0, len(r.host.lingering))
	for me := range r.host.lingering {
		out = append(out, me)
	}
	return out
}

func processEnded(me *managedExt) <-chan struct{} {
	if exited := me.exited(); exited != nil {
		return exited
	}
	return me.packedProcess.startWait()
}

// requireConnEnded waits for the connection of a generation that shares its process with live extensions: the generation ends, the process does not.
func requireConnEnded(t *testing.T, conn *Conn, what string) {
	t.Helper()
	select {
	case <-conn.done:
	case <-time.After(testTimeout(t, 10*time.Second)):
		t.Fatalf("%s: the generation's connection is still open", what)
	}
}

func TestRemovedExtensionKeepsItsProviderAcrossTwoReloads(t *testing.T) {
	rig := newLingerRig(t, "linger-provider")
	rig.reload(t)
	first, _ := rig.registry.answer(t, "linger-provider")
	if !strings.Contains(first, "served by closure of load 1") {
		t.Fatalf("before the removal: %q", first)
	}
	rig.remove("linger-provider")
	var process *managedExt
	for pass := 1; pass <= 2; pass++ {
		rig.reload(t)
		if !rig.registry.listed("linger-provider") {
			t.Fatalf("reload %d: the removed extension's provider left the registry", pass)
		}
		if unregistered := rig.registry.unregistered; len(unregistered) != 0 {
			t.Fatalf("reload %d unregistered %v: Pi never unregisters on reload", pass, unregistered)
		}
		text, stop := rig.registry.answer(t, "linger-provider")
		if stop != ai.StopReasonStop || text != first {
			t.Fatalf("reload %d: answer %q (%s), want the old closure's %q", pass, text, stop, first)
		}
		lingering := rig.lingering()
		if len(lingering) != 1 {
			t.Fatalf("reload %d: %d lingering generations, want one", pass, len(lingering))
		}
		if process != nil && lingering[0] != process {
			t.Fatalf("reload %d replaced the lingering generation: a second reload must not duplicate or restart it", pass)
		}
		process = lingering[0]
	}
	rig.host.mu.Lock()
	_, listed := rig.host.exts["linger-provider"]
	rig.host.mu.Unlock()
	if listed {
		t.Fatal("the removed extension is still one of the host's extensions")
	}
	select {
	case <-processEnded(process):
		t.Fatal("the lingering process ended")
	default:
	}
}

func TestShutdownEndsAndReapsALingeringGeneration(t *testing.T) {
	baseline := testenv.BaselineGoroutines()
	rig := newLingerRig(t, "linger-provider")
	rig.reload(t)
	rig.remove("linger-provider")
	rig.reload(t)
	lingering := rig.lingering()
	if len(lingering) != 1 {
		t.Fatalf("%d lingering generations, want one", len(lingering))
	}
	rig.host.Shutdown("lingering shutdown")
	requireExited(t, lingering[0], "after Shutdown")
	if left := rig.lingering(); len(left) != 0 {
		t.Fatalf("Shutdown left %d lingering generations", len(left))
	}
	baseline.RequireEnded(t, goroutineGrace)
}

func TestRemovedExtensionComesBackAndTakesItsProviderOver(t *testing.T) {
	rig := newLingerRig(t, "linger-provider")
	rig.reload(t)
	rig.remove("linger-provider")
	rig.reload(t)
	lingering := rig.lingering()
	if len(lingering) != 1 {
		t.Fatalf("%d lingering generations, want one", len(lingering))
	}
	oldConn := lingering[0].connection()
	old, _ := rig.registry.answer(t, "linger-provider")
	path, err := filepath.Abs(filepath.Join("testdata", "linger-provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	rig.configs = append(rig.configs, ExtConfig{Name: "linger-provider", Source: path, Enabled: true})
	rig.reload(t)
	requireConnEnded(t, oldConn, "after the extension came back")
	pollUntil(t, testTimeout(t, 10*time.Second), "the lingering generation is released", func() bool { return len(rig.lingering()) == 0 })
	text, stop := rig.registry.answer(t, "linger-provider")
	if stop != ai.StopReasonStop || text == old {
		t.Fatalf("answer %q (%s): the returning extension's own closure must answer, not the lingering one's %q", text, stop, old)
	}
}

func TestAnotherExtensionUnregisteringTheNameReleasesTheLingeringGeneration(t *testing.T) {
	rig := newLingerRig(t, "linger-provider", "linger-sibling")
	rig.reload(t)
	rig.remove("linger-provider")
	rig.reload(t)
	lingering := rig.lingering()
	if len(lingering) != 1 {
		t.Fatalf("%d lingering generations, want one", len(lingering))
	}
	oldConn := lingering[0].connection()
	rig.host.mu.Lock()
	sibling := rig.host.exts["linger-sibling"]
	rig.host.mu.Unlock()
	if sibling == nil {
		t.Fatal("the sibling is not loaded")
	}
	if rig.registry.listed("linger-provider") == false {
		t.Fatal("the lingering provider is not listed before the unregistration")
	}
	// Pi's unregisterProvider removes the one effective registration whoever registered it (model-runtime.ts:988-994).
	loaded, err := rig.host.Reload(rig.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range loaded {
		if command, ok := ext.Commands["linger-unregister"]; ok {
			if err := command.Handler(rig.ctx, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	requireConnEnded(t, oldConn, "after another extension unregistered the name")
	pollUntil(t, testTimeout(t, 10*time.Second), "the provider left the registry", func() bool { return !rig.registry.listed("linger-provider") })
}

func TestLingeringProcessThatDiesFailsItsStreamAndIsNotRestarted(t *testing.T) {
	rig := newLingerRig(t, "linger-provider")
	rig.reload(t)
	rig.remove("linger-provider")
	rig.reload(t)
	lingering := rig.lingering()
	if len(lingering) != 1 {
		t.Fatalf("%d lingering generations, want one", len(lingering))
	}
	lingering[0].killProcess()
	requireExited(t, lingering[0], "after the kill")
	text, stop := rig.registry.answer(t, "linger-provider")
	if stop != ai.StopReasonError || !strings.Contains(text, "linger-provider") {
		t.Fatalf("answer %q (%s): a dead provider's stream must end in an error that names the provider", text, stop)
	}
	rig.host.mu.Lock()
	restarted := len(rig.host.exts)
	rig.host.mu.Unlock()
	if restarted != 0 {
		t.Fatalf("%d extensions after the lingering process died: it must not be started again from a file that is gone", restarted)
	}
}

func TestLingeringGenerationReceivesNothingTheHostSendsToExtensions(t *testing.T) {
	rig := newLingerRig(t, "linger-provider", "linger-sibling")
	rig.reload(t)
	rig.remove("linger-provider")
	rig.reload(t)
	lingering := rig.lingering()
	if len(lingering) != 1 {
		t.Fatalf("%d lingering generations, want one", len(lingering))
	}
	rig.host.mu.Lock()
	siblingConn := rig.host.exts["linger-sibling"].connection()
	rig.host.mu.Unlock()
	conn := lingering[0].connection()
	pollUntil(t, testTimeout(t, 10*time.Second), "the writer of the lingering connection is idle", func() bool { return conn.queued.Load() == 0 })
	before, siblingBefore := conn.writeProgress.Load(), siblingConn.writeProgress.Load()
	rig.host.NotifyWidth(137)
	rig.host.NotifyHeight(41)
	pollUntil(t, testTimeout(t, 10*time.Second), "the live sibling received the geometry", func() bool { return siblingConn.writeProgress.Load() > siblingBefore })
	if conn.queued.Load() != 0 || conn.writeProgress.Load() != before {
		t.Fatalf("the host sent the removed extension a message (queued %d, bytes %d -> %d)", conn.queued.Load(), before, conn.writeProgress.Load())
	}
}
