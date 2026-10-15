// Package aotruntime is the compiled-extensions runtime Piglet extension (experimental). It compiles the user's TypeScript extension set and PiG's
// extension host into one executable with Pi-Bolt's ahead-of-time compiler, and installs that executable into PiG's TypeScript runtime slot. PiG
// starts the set from the executable when the global extensionsTypescriptRuntime setting, or PIG_TS_RUNTIME, selects "aot".
//
// Stock PiG downloads nothing and compiles nothing; this extension does both, on the user's machine, and only after the setting selects "aot":
// the Pi-Bolt compiler is downloaded from its GitHub release and verified against the Ed25519 key and digests pinned in this Piglet, and a set is
// compiled in the background while it runs on the runtime the manifest names as its fallback (Bun, else Node). The next start uses the image.
package aotruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const usage = "Usage: /aot-runtime [status|install|verify|remove|prune|budget [size]]"

// Extension returns the aot runtime extension.
func Extension() *sdk.Extension {
	ext := sdk.New("aotruntime")
	ext.Command("aot-runtime", "Status, install and cache of the compiled-extensions runtime (experimental).", run)
	w := &lifecycle{}
	ext.OnSessionStart(w.sessionStart)
	ext.OnSessionShutdown(w.sessionShutdown)
	return ext
}

// lifecycle starts the background worker at session start and stops it, cancelling a build in progress, at session shutdown.
type lifecycle struct {
	mu     sync.Mutex
	worker *worker
}

func (l *lifecycle) sessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	root, err := slotRoot(sdk.AgentDir)
	if err != nil {
		return nil, nil
	}
	platform := platformName()
	if !supportedPlatform(platform) {
		// A manifest for another platform makes PiG report unsupported_platform and use the fallback runtime.
		_ = newStore(root, "unsupported:"+platform).ensureManifest()
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.worker == nil {
		l.worker = newWorker(root, platform, func(message string) {
			appendLog(root, message)
			ctx.Notify(message, "info")
		})
	}
	l.worker.start()
	return nil, nil
}

func (l *lifecycle) sessionShutdown(sdk.Context, map[string]any) (any, error) {
	l.mu.Lock()
	w := l.worker
	l.mu.Unlock()
	if w != nil {
		w.stop()
	}
	return nil, nil
}

// appendLog records a verbose message beside the build logs.
func appendLog(root, message string) {
	dir := filepath.Join(root, runtimeID, "logs")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "aot-runtime.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().UTC().Format(time.RFC3339), message)
}

func run(ctx sdk.Context, args string) error {
	root, err := slotRoot(sdk.AgentDir)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-requestCtx.Done():
		}
	}()
	message, err := runAction(requestCtx, strings.TrimSpace(args), newWorker(root, platformName(), nil))
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return nil
	}
	ctx.Notify(message, "info")
	return nil
}

func runAction(ctx context.Context, args string, w *worker) (string, error) {
	action, rest, _ := strings.Cut(args, " ")
	rest = strings.TrimSpace(rest)
	switch action {
	case "", "status":
		return w.status(ctx), nil
	case "install":
		if !supportedPlatform(w.platform) {
			return "", fmt.Errorf("the aot runtime does not support %s: it builds for linux-x64 and darwin-arm64", w.platform)
		}
		exe, err := w.ensureToolchain(ctx)
		if err != nil {
			return "", fmt.Errorf("aot runtime install failed: %w", err)
		}
		if err := w.store.ensureManifest(); err != nil {
			return "", err
		}
		return fmt.Sprintf("Installed the Pi-Bolt %s compiler at %s after verifying its signature and digests. %s", toolchainVersion, exe, selectHint), nil
	case "verify":
		exe, err := verifyToolchain(ctx, w.store.dir, w.platform, archives)
		if err != nil {
			return "", fmt.Errorf("aot runtime verify failed: %w", err)
		}
		return fmt.Sprintf("The Pi-Bolt %s compiler at %s matches its pinned digest and runs.", toolchainVersion, exe), nil
	case "remove":
		toolchain, err := removeToolchain(w.store.dir, w.platform)
		if err != nil {
			return "", fmt.Errorf("aot runtime remove failed: %w", err)
		}
		if err := w.store.removeAll(); err != nil {
			return "", fmt.Errorf("aot runtime remove failed: %w", err)
		}
		if !toolchain {
			return "The aot runtime: nothing to remove besides its images. TypeScript extensions that request it run on the fallback runtime.", nil
		}
		return "Removed the aot runtime, its compiler and its images. TypeScript extensions that request it run on the fallback runtime.", nil
	case "prune":
		budget, err := w.store.cacheBudget()
		if err != nil {
			return "", err
		}
		removed, err := w.store.prune(budget, "")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed %d image(s) to stay within %s.", len(removed), formatSize(budget)), nil
	case "budget":
		if rest == "" {
			budget, err := w.store.cacheBudget()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("The cache budget is %s (the default is %s; %s overrides it).", formatSize(budget), formatSize(defaultCacheBudget), envCacheMax), nil
		}
		if _, err := parseSize(rest); err != nil {
			return "", err
		}
		if err := w.store.writeConfig(config{CacheMax: rest}); err != nil {
			return "", err
		}
		return fmt.Sprintf("The cache budget is %s. The least recently used images go first when the images exceed it.", rest), nil
	}
	return "", fmt.Errorf("%s", usage)
}

const selectHint = `To use it, set "extensionsTypescriptRuntime": "aot" in settings.json, or PIG_TS_RUNTIME=aot, and restart. pig extensions runtime status shows the runtime in use.`

// status describes the aot runtime for the user.
func (w *worker) status(ctx context.Context) string {
	var b strings.Builder
	b.WriteString("aot runtime (experimental; Pi-Bolt " + toolchainVersion + ")\n")
	if !supportedPlatform(w.platform) {
		fmt.Fprintf(&b, "  platform: %s is not supported; it builds for linux-x64 and darwin-arm64, and other platforms run on the fallback runtime\n", w.platform)
		return b.String()
	}
	fmt.Fprintf(&b, "  platform: %s (%s code)\n", w.platform, cpuVariant(w.platform))
	if exe, err := verifyToolchain(ctx, w.store.dir, w.platform, archives); err != nil {
		fmt.Fprintf(&b, "  compiler: not installed (%v); the first request downloads it, or /aot-runtime install\n", err)
	} else {
		fmt.Fprintf(&b, "  compiler: %s\n", exe)
	}
	budget, _ := w.store.cacheBudget()
	images, _ := w.store.images()
	var total int64
	for _, image := range images {
		total += image.Size
	}
	fmt.Fprintf(&b, "  images: %d, %s of %s\n", len(images), formatSize(total), formatSize(budget))
	for _, image := range images {
		fmt.Fprintf(&b, "    %s %s\n", short(image.Key), formatSize(image.Size))
	}
	failures := w.store.failures()
	for _, key := range sortedKeys(failures) {
		f := failures[key]
		fmt.Fprintf(&b, "  not compiled in: %s: %s (%s)\n", short(key), f.Reason, f.Detail)
	}
	if requests, _ := w.store.readRequests(); len(requests) > 0 {
		fmt.Fprintf(&b, "  waiting to build: %d set(s)\n", len(requests))
	}
	b.WriteString("  " + selectHint)
	return b.String()
}
