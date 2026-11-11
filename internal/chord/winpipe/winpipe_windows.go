//go:build windows

package winpipe

import (
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

// closeRetry is how long Close waits for go-winio's accept goroutine to end before it makes its request again.
const closeRetry = 50 * time.Millisecond

// Listen is go-winio's ListenPipe with a Close that returns (the package doc): when Close returns, the listener's accept
// goroutine has ended and its instances of the pipe are closed. Accept, Addr and the config are go-winio's.
func Listen(path string, config *winio.PipeConfig) (net.Listener, error) {
	inner, err := winio.ListenPipe(path, config)
	if err != nil {
		return nil, err
	}
	return &listener{inner}, nil
}

type listener struct{ net.Listener }

// Close ends the listener: go-winio's request, made again every closeRetry until the accept goroutine has ended. Every
// request goroutine it started has returned when it does (they return at once once that goroutine has ended), and the error
// is go-winio's, which is always nil. It may be called more than once.
func (l *listener) Close() error {
	var requests sync.WaitGroup
	ended := make(chan struct{})
	request := func() {
		defer requests.Done()
		_ = l.Listener.Close()
	}
	requests.Add(1)
	go func() {
		request()
		close(ended)
	}()
	retry := time.NewTicker(closeRetry)
	defer retry.Stop()
	for {
		select {
		case <-ended:
			requests.Wait()
			return nil
		case <-retry.C:
			requests.Add(1)
			go request()
		}
	}
}
