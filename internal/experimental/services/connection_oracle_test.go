package services

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestConnectionMatchesPinnedSourceWithLocalBindings(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "node", "testdata/connection-oracle.mjs").Output()
	if err != nil {
		t.Fatalf("pinned connection probe: %v", err)
	}
	client := &localClient{state: "connecting"}
	active, inactive := &localBinding{}, &localBinding{}
	server := CreateServerServiceSource(client, localOptions(active, inactive))
	binding, err := server.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Open(openOptions()); err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	client.connect("connected", nil)
	client.connect("disconnected", errors.New("wire closed"))
	disconnected := server.Connection.Value()
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", disconnected.Since); err != nil {
		t.Fatalf("ISO millisecond timestamp: %v", err)
	}
	client.connect("connecting", nil)
	connecting := server.Connection.Value()
	client.connect("connected", nil)
	if err := server.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}

	sessionClient := &localClient{state: "connecting"}
	local := &localBinding{}
	session := CreateSessionServiceSource(sessionClient, localOptions(local))
	sessionBinding, err := session.Open(openOptions())
	if err != nil {
		t.Fatal(err)
	}
	var states []SessionAttachmentState
	stop := session.Attachment.Subscribe(func(state SessionAttachmentState, _ context.Context, _ ReplicatedStateDelivery) {
		states = append(states, state)
	})
	defer stop()
	if err := sessionBinding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	sessionClient.attach(&SessionTarget{ServerID: "server", SessionID: "session", AttachmentID: "a"})
	if err := session.WhenAttached("session", t.Context()); err != nil {
		t.Fatal(err)
	}
	catalogue, err := session.Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sessionClient.attach(nil)
	if err := session.WhenDetached(t.Context()); err != nil {
		t.Fatal(err)
	}
	cached, err := session.Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(map[string]any{
		"bounds":             [][]bool{active.history(), append([]bool{}, inactive.history()...), local.history()},
		"closed":             []int{active.closed, inactive.closed, local.closed},
		"connecting":         connecting,
		"disconnected":       map[string]any{"status": disconnected.Status, "reason": disconnected.Reason, "retryAt": disconnected.RetryAt},
		"states":             states,
		"catalogue":          catalogue,
		"cached":             cached,
		"acceptsUnavailable": session.AcceptsUnavailableServices(),
		"listenersRemoved":   client.connectionListener == nil && sessionClient.attachmentListener == nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("upstream JSON: %v\n%s", err, output)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connection differs\nGo: %s\nPi: %s", actual, output)
	}
}
