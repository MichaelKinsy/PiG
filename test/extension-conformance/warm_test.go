package extensionconformance

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// warmGrace is left before the go test deadline so a setup that the deadline ends reports itself instead of the test binary's timeout panic.
const warmGrace = 10 * time.Second

// warmFixtures builds, before any test starts, what the tests share and would otherwise build inside whichever test asks first: the Go and Rust fixture
// programs (unless the grouped run prebuilt them: PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN, PIG_TEST_RUST_SDK_FIXTURE_BIN), the packed UI cells of the four
// native SDKs and the Lisp harness cell. A cold cache (a fresh checkout, a Cargo target that still compiles the SDK, a Lisp image to save) takes minutes,
// which a test's own wait budget (internal/testbudget) cannot know, so a test that came first on a cold machine failed with its wait spent on a build. The
// setup has its own bound, the go test deadline, and fails the run with what it was building. With the caches warm (the second process of a run, shared
// through shareCellCaches) it takes seconds.
func warmFixtures() error {
	if flagValue("test.list") != "" {
		return nil // go test -list only names the tests.
	}
	ctx := context.Background()
	if timeout, err := time.ParseDuration(flagValue("test.timeout")); err == nil && timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, max(timeout-warmGrace, time.Second))
		defer cancel()
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	step := func(name string, run func(context.Context) error) {
		wg.Go(func() {
			if err := run(ctx); err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					err = fmt.Errorf("%w (the go test -timeout deadline arrived, less %s left to report: the package run is out of time, not hung)", err, warmGrace)
				}
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
				mu.Unlock()
			}
		})
	}
	if os.Getenv("PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN") == "" {
		step("Go SDK fixture", func(ctx context.Context) error {
			bin, err := buildSDKFixtureBinary(ctx)
			if err == nil {
				err = os.Setenv("PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN", bin)
			}
			return err
		})
	}
	if _, err := exec.LookPath("cargo"); err == nil && os.Getenv("PIG_TEST_RUST_SDK_FIXTURE_BIN") == "" {
		step("Rust SDK fixture", func(ctx context.Context) error {
			bin, err := buildRustFixtureBinary(ctx)
			if err == nil {
				err = os.Setenv("PIG_TEST_RUST_SDK_FIXTURE_BIN", bin)
			}
			return err
		})
	}
	for _, language := range []string{"go", "rust", "python", "lisp"} {
		step("packed "+language+" cell", func(ctx context.Context) error {
			configs, err := packedUIConfigs[language]()
			if err != nil {
				return err
			}
			return loadAndStop(ctx, configs)
		})
	}
	step("Lisp harness cell", func(ctx context.Context) error {
		return loadAndStop(ctx, []subprocess.ExtConfig{lispFixtureConfigAt(fixtureSourceRoot, "lisp-sdk-fixture", lispFixtureName)})
	})
	wg.Wait()
	return errors.Join(errs...)
}

// loadAndStop loads configs through a Host on the shared config root, which builds their cells into the shared cache, and stops it.
func loadAndStop(ctx context.Context, configs []subprocess.ExtConfig) error {
	dir, err := os.MkdirTemp(fixtureRoot, "warm-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	host := subprocess.NewHost(dir)
	defer host.Shutdown("warm done")
	host.SetHostInfo(conformanceHostInfo)
	host.SetUIBridge(subprocess.NewUIBridge(func() {}))
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
	loaded, err := host.Reload(ctx)
	if err != nil {
		return fmt.Errorf("%w (%+v)", err, host.LastReloadReport())
	}
	if len(loaded) != len(configs) {
		return fmt.Errorf("loaded %d of %d extensions (%+v)", len(loaded), len(configs), host.LastReloadReport())
	}
	return nil
}

func flagValue(name string) string {
	if f := flag.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}
