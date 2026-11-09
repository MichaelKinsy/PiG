//go:build !(pig_strip_extension_sdk_go && pig_strip_extension_sdk_rust && pig_strip_extension_sdk_python && pig_strip_extension_sdk_lisp)

package pigsdk

import (
	"embed"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/sdkpack"
	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

// decodeBundle expands the one zstd frame a bundle's Source holds into the blob sdkpack.Walk reads: an exactly sized buffer that the caller drops when it has written the files. It lives here, apart from pigsdk.go, so a Piglet Binary that strips every SDK links no zstd decoder through this package.
func decodeBundle(source embed.FS) ([]byte, error) {
	frame, err := source.ReadFile(sdkpack.ArchiveName)
	if err != nil {
		return nil, err
	}
	blob, err := zstdblob.Decode(frame)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sdkpack.ArchiveName, err)
	}
	return blob, nil
}
