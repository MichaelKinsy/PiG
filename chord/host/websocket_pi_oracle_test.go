package host_test

// pi: packages/chord/src/websocket.ts
// Pi's acceptWebSockets run under Node 24 on a fake server, sockets and host (testdata/pi_websocket.mjs regenerates
// testdata/pi_websocket.json from the pinned mirror). Each case is one authenticator outcome and what Pi did to the
// socket: the frames it sent, how it closed it, and whether it accepted the link on the host. Pi's own websocket test
// closes refused sockets without looking at the reason, so these are the only checks of the two refusal reasons.

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/host"
)

func TestAcceptWebSocketsMatchesPiOnEveryAuthenticatorOutcome(t *testing.T) {
	data, err := os.ReadFile("testdata/pi_websocket.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Records []struct {
			Request  string     `json:"request"`
			Sent     []string   `json:"sent"`
			Closes   [][]any    `json:"closes"`
			Accepted []struct{} `json:"accepted"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Records) != 5 {
		t.Fatalf("Pi oracle has %d records", len(oracle.Records))
	}
	var reportedMu sync.Mutex
	var reported []error
	chord := host.CreateHost(host.HostOptions{Self: map[string]any{"kind": "host"}, Name: "server", OnError: func(err error) {
		reportedMu.Lock()
		reported = append(reported, err)
		reportedMu.Unlock()
	}})
	server := &fakeWebSocketServer{}
	// The authenticator settles as the request says: the token after "token=" names the outcome, as in the Pi script.
	host.AcceptWebSockets[string](server, chord, host.WebSocketAcceptOptions[string]{
		Principal: func(request string) (host.Principal, bool, error) {
			switch {
			case strings.HasSuffix(request, "=bad"):
				return nil, false, nil
			case strings.HasSuffix(request, "=error"):
				return nil, false, errors.New("token service down")
			case strings.HasSuffix(request, "=falsy"):
				// Pi refuses only `undefined`: a falsy principal such as 0 is accepted.
				return float64(0), true, nil
			default:
				return map[string]any{"guest": "web"}, true, nil
			}
		},
	})
	sockets := make([]*fakeSocket, len(oracle.Records))
	for index, record := range oracle.Records {
		sockets[index] = &fakeSocket{state: 1}
		server.connect(sockets[index], record.Request)
	}
	// Close drains the authentication tasks the host owns.
	if err := chord.Close(); err != nil {
		t.Fatal(err)
	}
	for index, record := range oracle.Records {
		socket := sockets[index]
		refused := len(record.Accepted) == 0
		reasons := socket.byeReasons(t)
		socket.mu.Lock()
		closes, closeReasons := append([]int(nil), socket.closes...), append([]string(nil), socket.closeReasons...)
		socket.mu.Unlock()
		if refused {
			// Pi's refused link sends one bye with its reason and closes the socket with 1000 and no reason.
			var wantReasons []string
			for _, frame := range record.Sent {
				var bye struct {
					Reason string `json:"reason"`
				}
				if err := json.Unmarshal([]byte(frame), &bye); err != nil {
					t.Fatal(err)
				}
				wantReasons = append(wantReasons, bye.Reason)
			}
			if strings.Join(reasons, "|") != strings.Join(wantReasons, "|") {
				t.Errorf("%s: bye reasons %v, Pi %v", record.Request, reasons, wantReasons)
			}
			if len(closes) != len(record.Closes) || (len(closes) > 0 && (float64(closes[0]) != record.Closes[0][0] || closeReasons[0] != "")) {
				t.Errorf("%s: socket closes %v %v, Pi %v", record.Request, closes, closeReasons, record.Closes)
			}
			continue
		}
		// Pi accepted the link: nothing refused it. Closing the host afterwards may close the link, so only a refusal
		// reason would be wrong.
		for _, reason := range reasons {
			if reason == "unauthorized" || reason == "authentication failed" {
				t.Errorf("%s: Pi accepted the client, PiG refused it with %q", record.Request, reason)
			}
		}
	}
	reportedMu.Lock()
	defer reportedMu.Unlock()
	if len(reported) != 0 {
		t.Fatalf("reported %v", reported)
	}
}
