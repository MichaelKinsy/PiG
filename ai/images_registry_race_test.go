package ai

import (
	"sync"
	"testing"
)

// The image API registry is read and written by every typed-provider composition (RegisterBuiltInImagesAPIProviders runs from each
// built-in provider with image models), and compositions run concurrently, so registration and lookup must be safe together.
// Run with -race.
func TestImagesAPIProviderRegistryIsSafeForConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				RegisterBuiltInImagesAPIProviders()
				if _, ok := GetImagesAPIProvider(APIImagesOpenRouter); !ok {
					t.Error("the built-in OpenRouter image provider is not registered")
				}
			}
		})
	}
	wg.Wait()
}
