//go:build portmap_skeleton

package routing

import "testing"

// pi-unproven: packages/server/src/types.ts
//
// Skeleton for the marker test of packages/server/src/types.ts. Port its cases against internal/experimental/routing/types.go: call the entry point, assert the Pi result, then
// delete the build tag line, change `pi-unproven` to `pi`, and prove the test red by mutating a covered line of the cited file.
//
// Entry points in the cited file:
//   - BasicSessionMetadata.SessionID
//   - type BasicSessionMetadata
//   - type ByteConnection
//   - type ByteConnectionAcceptor
//   - type ByteConnectionHandler
//   - type RoutedServerPresentation
//   - type RoutedServerServiceAttachment
//   - type RoutedServerServiceHost
//   - type RoutedSessionAttachment
//   - type RoutedSessionHandle
//   - type ServerHost
//   - type ServerListener
//   - type ServerOptions
//   - type SessionMetadata
func TestPiServerSrcTypesSkeleton(t *testing.T) {
	t.Fatal("skeleton: port the types.ts cases against internal/experimental/routing/types.go")
}
