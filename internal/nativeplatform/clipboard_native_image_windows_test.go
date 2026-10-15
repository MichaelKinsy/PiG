//go:build windows

package nativeplatform

import (
	"fmt"
	"image"
	"os/exec"
	"strings"
	"testing"
)

// putImageOnDesktopClipboard copies img to the Windows clipboard through System.Windows.Forms, which stores the registered 'PNG'
// format, as screenshot tools do. SetImage is avoided on purpose: the CF_DIBV5 Windows synthesizes from it was measured (CI
// diagnostic) to hold rows top-down under a positive (bottom-up) height, so any header-honouring reader sees the image flipped. Windows PowerShell runs single-threaded-apartment by default, which the clipboard API requires.
func putImageOnDesktopClipboard(t *testing.T, img image.Image) {
	t.Helper()
	bounds := img.Bounds()
	var script strings.Builder
	script.WriteString("Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; ")
	fmt.Fprintf(&script, "$b = New-Object System.Drawing.Bitmap %d,%d; ", bounds.Dx(), bounds.Dy())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			fmt.Fprintf(&script, "$b.SetPixel(%d,%d,[System.Drawing.Color]::FromArgb(%d,%d,%d,%d)); ", x-bounds.Min.X, y-bounds.Min.Y, a>>8, r>>8, g>>8, b>>8)
		}
	}
	script.WriteString("$ms = New-Object System.IO.MemoryStream; $b.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); ")
	script.WriteString("$d = New-Object System.Windows.Forms.DataObject; $d.SetData('PNG', $false, $ms); [System.Windows.Forms.Clipboard]::SetDataObject($d, $true)")
	if out, err := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script.String()).CombinedOutput(); err != nil {
		t.Fatalf("powershell: %v\n%s", err, out)
	}
}
