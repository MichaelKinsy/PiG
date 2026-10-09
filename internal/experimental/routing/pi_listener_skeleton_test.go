//go:build portmap_skeleton

package routing

import "testing"

// pi-unproven: packages/server/src/listener.ts
//
// Skeleton for the marker test of packages/server/src/listener.ts. Port its cases against internal/experimental/routing/types.go: call the entry point, assert the Pi result, then
// delete the build tag line, change `pi-unproven` to `pi`, and prove the test red by mutating a covered line of the cited file.
//
// Pi tests of this file (port these cases):
//   - packages/server/test/listener.test.ts
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
func TestPiServerSrcListenerSkeleton(t *testing.T) {
	t.Fatal("skeleton: port the listener.ts cases against internal/experimental/routing/types.go")
}
