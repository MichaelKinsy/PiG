// Package cloudflare is the image processor for Cloudflare Workers and Durable Objects.
//
// Ports packages/durable/src/images/cloudflare.ts.
package cloudflare

import (
	"github.com/MichaelKinsy/PiG/durable/images"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// CreateCloudflarePhotonImages is images.CreatePhotonImages for Cloudflare Workers and Durable Objects. The Go processor
// needs no compiled WebAssembly module, so it is the same processor as images.CreatePhotonImages.
func CreateCloudflarePhotonImages() tools.ImageProcessor {
	return images.CreatePhotonImages()
}
