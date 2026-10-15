package codingagent

import (
	"context"
	"encoding/hex"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/tui"
)

type clipboardTypesProbe struct {
	Wayland bool   `json:"wayland"`
	Listing string `json:"listing"`
	// ListingHex is the listing's bytes: Listing may hold invalid UTF-8, which a JSON string cannot carry.
	ListingHex string `json:"listingHex"`
}

type clipboardTypesResult struct {
	Requested string `json:"requested"`
	MimeType  string `json:"mimeType"`
	Found     bool   `json:"found"`
}

// readClipboardImage against Pi's (utils/clipboard-image.js) with a fake wl-paste or xclip that advertises a random type listing: which type is
// requested (selectPreferredImageMimeType: JS trim, the line split, the case-insensitive base type, the image/* fallback) and the mime type reported.
// Pi's command runner and native clipboard are replaced as Pi's clipboard-image.test.ts mocks them: a spawned fake command races Pi's 1 s listing
// timeout, and a timed-out listing falls back to the host's native clipboard. PiG runs the same listing through its command hook.
func TestClipboardTypeSelectionMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	types := []string{
		"image/png", "IMAGE/PNG", "image/jpeg", "image/jpeg; charset=x", " image/webp ", "\timage/gif", "image/gif;q=1", "text/plain", "text/html", "image/bmp", "image/svg+xml",
		"image/png\u0085", "\ufeffimage/png", "image/PNG\u00a0", "\u00a0image/jpeg", "İmage/png", "image/png ; x", "STRING", "TARGETS", "", "   ", "image/Webp",
		"image/jpeg\u2028image/png", "image/tiff", "image/x-icon", "application/octet-stream", "Image/GIF", "image/ png", "image/\u212a", "\u200bimage/png",
		// Invalid UTF-8: Buffer.toString writes one U+FFFD per maximal ill-formed subpart, and the selected type is passed on as the --type argument.
		"image/png\xff\xfe", "image/png;\xc3(", "\xe2\x82image/gif", "image/\xf0\x9f\x98png",
	}
	seps := []string{"\n", "\r\n", "\n", "\r\n\r\n", "\n\n", "\r", "\n "}
	var probes []clipboardTypesProbe
	for range 500 {
		var parts []string
		for range rng.Intn(6) {
			parts = append(parts, types[rng.Intn(len(types))])
		}
		listing := ""
		for i, part := range parts {
			listing += part
			if i < len(parts)-1 || rng.Intn(2) == 0 {
				listing += seps[rng.Intn(len(seps))]
			}
		}
		probes = append(probes, clipboardTypesProbe{Wayland: rng.Intn(2) == 0, Listing: listing, ListingHex: hex.EncodeToString([]byte(listing))})
	}
	var want []clipboardTypesResult
	pioracle.Run(t, `
const { registerHooks } = await import("node:module");
const stubs = new Set();
registerHooks({
	resolve(specifier, context, next) {
		if (context.parentURL?.endsWith("/pi-coding-agent/utils/clipboard-image.js") && (specifier === "./clipboard-command.js" || specifier === "../../../pi-tui.mjs")) {
			stubs.add(specifier);
			return { url: "stub:" + specifier, shortCircuit: true };
		}
		return next(specifier, context);
	},
	load(url, context, next) {
		if (url === "stub:./clipboard-command.js") {
			return { format: "module", source: "export const runClipboardCommand = (...args) => globalThis.clipboardCommand(...args);", shortCircuit: true };
		}
		if (url === "stub:../../../pi-tui.mjs") return { format: "module", source: "export const getNativeClipboard = () => undefined;", shortCircuit: true };
		return next(url, context);
	},
});
const png = Buffer.from("89504e470d0a1a0a30303030", "hex");
let listing, requested;
globalThis.clipboardCommand = async (command, args) => {
	if (args.includes("--list-types") || args.includes("TARGETS")) return listing;
	for (let i = 0; i + 1 < args.length; i++) if (args[i] === "--type" || args[i] === "-t") requested = args[i + 1];
	return png;
};
const { readClipboardImage } = await load("pi-coding-agent/utils/clipboard-image.js");
if (stubs.size !== 2) throw new Error("clipboard-image.js did not import both mocked modules: " + [...stubs]);
const out = [];
for (const p of input) {
	listing = Buffer.from(p.listingHex, "hex");
	requested = "";
	const env = p.wayland ? { WAYLAND_DISPLAY: "wayland-0" } : { DISPLAY: ":0" };
	const image = await readClipboardImage({ env, platform: "linux" });
	out.push({ requested, mimeType: image?.mimeType ?? "", found: image != null });
}
emit(out);
`, probes, &want)
	pngBytes := []byte("\x89PNG\r\n\x1a\n0000")
	oldRun, oldEnv, oldPlatform, oldNative := clipboardRun, clipboardEnv, clipboardGOOS, getNativeClipboard
	t.Cleanup(func() {
		clipboardRun, clipboardEnv, clipboardGOOS, getNativeClipboard = oldRun, oldEnv, oldPlatform, oldNative
	})
	clipboardGOOS = "linux"
	getNativeClipboard = func() *tui.NativeClipboard { return nil }
	bad := 0
	for i, p := range probes {
		env := map[string]string{"DISPLAY": ":0"}
		if p.Wayland {
			env = map[string]string{"WAYLAND_DISPLAY": "wayland-0"}
		}
		clipboardEnv = func(key string) string { return env[key] }
		requested := ""
		clipboardRun = func(_ context.Context, name string, args ...string) ([]byte, error) {
			if slices.Contains(args, "--list-types") || slices.Contains(args, "TARGETS") {
				return []byte(p.Listing), nil
			}
			for j, arg := range args {
				if (arg == "--type" || arg == "-t") && j+1 < len(args) {
					requested = args[j+1]
				}
			}
			_ = name
			return pngBytes, nil
		}
		data, mime, err := ReadClipboardImageContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		got := clipboardTypesResult{Requested: requested, MimeType: mime, Found: data != nil}
		if got != want[i] {
			if bad++; bad <= 6 {
				t.Errorf("probe %d wayland=%v listing=%q:\n got %+v\nwant %+v", i, p.Wayland, strings.ReplaceAll(p.Listing, "\r", "\\r"), got, want[i])
			}
		}
	}
	if bad > 6 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
