//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"debug/elf"
	"testing"
)

// .upstream/v0.87.1/packages/tui/test/native-platform.test.ts:8 (Linux ARM64 prebuild supports 64 KB system pages). PiG embeds Pi's prebuilt addon in runtime-node.pack, so the same ELF assertions run on the shipped bytes.
func TestUpstreamNativePlatformLinuxArm64PrebuildSupportsLargePages(t *testing.T) {
	data, err := nodeRuntimeFS.ReadFile("runtime-node/shims/pi-dist/pi-tui/native/linux/prebuilds/linux-arm64/linux-platform-x11.node")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("\x7fELF")) {
		t.Fatal("addon is not an ELF file")
	}
	file, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if file.Machine != elf.EM_AARCH64 {
		t.Fatalf("machine = %v, want AArch64", file.Machine)
	}
	loadSegments := 0
	for _, program := range file.Progs {
		if program.Type != elf.PT_LOAD {
			continue
		}
		loadSegments++
		if (program.Vaddr-program.Off)%65536 != 0 {
			t.Fatalf("PT_LOAD vaddr %#x and offset %#x are not congruent modulo 64 KB", program.Vaddr, program.Off)
		}
		if program.Align < 65536 {
			t.Fatalf("PT_LOAD alignment %#x is below 64 KB", program.Align)
		}
	}
	if loadSegments == 0 {
		t.Fatal("no PT_LOAD segments")
	}
}
