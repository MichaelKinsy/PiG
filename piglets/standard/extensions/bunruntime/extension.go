// Package bunruntime is the Bun runtime Piglet extension: it installs, verifies and removes stock Bun in PiG's TypeScript runtime
// slot, from which PiG runs TypeScript extensions when extensionsTypescriptRuntime or PIG_TS_RUNTIME selects "bun". Stock PiG
// downloads nothing; this extension is the only part that does, and only on the user's command.
package bunruntime

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const usage = "Usage: /bun-runtime [status|install|verify|remove]"

func Extension() *sdk.Extension {
	ext := sdk.New("bunruntime")
	ext.Command("bun-runtime", "Install, verify or remove the Bun runtime for TypeScript extensions.", run)
	return ext
}

func run(ctx sdk.Context, args string) error {
	root, err := slotRoot(sdk.AgentDir)
	if err != nil {
		return err
	}
	platform, musl := platform()
	// The command's request cancellation (Esc, shutdown) stops a download or a version check.
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-requestCtx.Done():
		}
	}()
	message, err := runAction(requestCtx, strings.TrimSpace(args), root, platform, installer{root: root, platform: platform, musl: musl, baseURL: releaseURL, client: http.DefaultClient})
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return nil
	}
	ctx.Notify(message, "info")
	return nil
}

func runAction(ctx context.Context, action, root, platform string, in installer) (string, error) {
	switch action {
	case "", "status":
		executable, err := verifyInstalled(ctx, root, platform, pinnedAssets(in))
		if err != nil {
			return fmt.Sprintf("Bun runtime: not usable (%v). Run /bun-runtime install.", err), nil
		}
		return fmt.Sprintf("Bun runtime: Bun %s at %s. %s", bunVersion, executable, selectHint), nil
	case "install":
		executable, err := in.install(ctx)
		if err != nil {
			return "", fmt.Errorf("Bun runtime install failed: %w", err)
		}
		return fmt.Sprintf("Installed Bun %s at %s after verifying its signature and digests. %s", bunVersion, executable, selectHint), nil
	case "verify":
		executable, err := verifyInstalled(ctx, root, platform, pinnedAssets(in))
		if err != nil {
			return "", fmt.Errorf("Bun runtime verify failed: %w", err)
		}
		return fmt.Sprintf("Bun %s at %s matches its pinned digest and runs.", bunVersion, executable), nil
	case "remove":
		removed, err := remove(root)
		if err != nil {
			return "", fmt.Errorf("Bun runtime remove failed: %w", err)
		}
		if !removed {
			return "Bun runtime: nothing to remove.", nil
		}
		return "Removed the Bun runtime. TypeScript extensions that request it run on Node.", nil
	}
	return "", fmt.Errorf("%s", usage)
}

const selectHint = `To use it, set "extensionsTypescriptRuntime": "bun" in settings.json, or PIG_TS_RUNTIME=bun, and restart. pig extensions runtime status shows the runtime in use.`

func pinnedAssets(in installer) []asset {
	_, _, pinned := in.pins()
	return pinned
}
