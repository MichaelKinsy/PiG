package release

import (
	"context"
	"fmt"
	"net/http"

	_ "github.com/MichaelKinsy/PiG/internal/tlsdistrust" // hardens http.DefaultTransport, which the release clients use (pig additive (D191))
)

// FetchIndex downloads the signed release index that ref names and verifies its signature and that it describes the requested release. It downloads no Binary and applies no trust or continuity policy, so a publisher can read the identity of a release it made and a catalog entry can name its signer without installing anything.
//
// pig additive (D18): npm Piglet source publication records the signed Binary release that matches it.
func FetchIndex(ctx context.Context, ref string, options Options) (VerifiedIndex, error) {
	reference, err := parseReleaseReference(ref, options.Version)
	if err != nil {
		return VerifiedIndex{}, err
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: pullTimeout}
	}
	data, err := fetchBounded(ctx, client, reference.url, maxIndexBytes)
	if err != nil {
		return VerifiedIndex{}, fmt.Errorf("fetch Piglet release index: %w", err)
	}
	verified, err := Verify(data)
	if err != nil {
		return VerifiedIndex{}, err
	}
	if err := reference.match(verified.Index); err != nil {
		return VerifiedIndex{}, err
	}
	return verified, nil
}
