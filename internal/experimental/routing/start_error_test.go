package routing_test

import "github.com/MichaelKinsy/PiG/internal/experimental/routing"

// startError is Server.Start for tests that only assert its error.
func startError(server *routing.Server) error {
	_, err := server.Start()
	return err
}
