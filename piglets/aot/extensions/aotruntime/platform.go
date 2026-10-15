package aotruntime

import (
	"bytes"
	"os"
	"runtime"
)

// platformName names the running platform as PiG's release targets do.
func platformName() string {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	goos := runtime.GOOS
	if goos == "linux" && os.Getenv("TERMUX_VERSION") != "" {
		goos = "android"
	}
	return goos + "-" + arch
}

// supportedPlatform reports whether Pi-Bolt builds a compiler for platform: linux-x64 and darwin-arm64.
func supportedPlatform(platform string) bool {
	_, ok := archiveFor(platform)
	return ok
}

// cpuVariant returns "native" when the machine has the instruction set the native build uses and "baseline" for an x86-64 CPU without AVX2.
func cpuVariant(platform string) string {
	if platform != "linux-x64" {
		return "native"
	}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil || bytes.Contains(data, []byte(" avx2")) {
		return "native"
	}
	return "baseline"
}
