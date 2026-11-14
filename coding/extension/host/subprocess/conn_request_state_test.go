package subprocess

import (
	"context"
	"net"
	"testing"
	"time"
)

// A connection makes the map of a kind of per-request state when a request first brings that state. An extension that loads and stops brings none of it, so its connection pays for none of the maps.
func TestNewConnMakesNoPerRequestStateMap(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	defer func() {
		_ = hostEnd.Close()
		_ = extEnd.Close()
	}()
	conn := NewConn("unused", hostEnd)
	for name, made := range map[string]bool{
		"toolRequests":      conn.toolRequests != nil,
		"startWindows":      conn.startWindows != nil,
		"earlierWindows":    conn.earlierWindows != nil,
		"answered":          conn.answered != nil,
		"arrived":           conn.arrived != nil,
		"hostCallCounts":    conn.hostCallCounts != nil,
		"drainable":         conn.drainable != nil,
		"drainCovered":      conn.drainCovered != nil,
		"suspendedRequests": conn.suspendedRequests != nil,
		"hostCalls":         conn.hostCalls != nil,
		"cancelledParent":   conn.cancelledParent != nil,
		"abortedParent":     conn.abortedParent != nil,
		"nestedCallIDs":     conn.nestedCallIDs != nil,
		"reservedCalls":     conn.reservedCalls != nil,
		"abandoned":         conn.abandoned != nil,
	} {
		if made {
			t.Errorf("NewConn made %s before a request brought that state", name)
		}
	}
	conn.markNestedCall("call-1")
	conn.abortNestedHostCalls("request-1")
	if _, nested := conn.nestedCallIDs["call-1"]; !nested {
		t.Error("the first nested call was not recorded")
	}
	if _, aborted := conn.abortedParent["request-1"]; !aborted {
		t.Error("the first abort was not recorded")
	}
}

// The lifetime of a connection ends when the context it was started under ends, so a request no caller owns ends with the host that started the connection.
func TestConnLifetimeEndsWithStartsParent(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	defer func() { _ = extEnd.Close() }()
	conn := NewConn("lifetime", hostEnd)
	parent, endParent := context.WithCancel(t.Context())
	defer endParent()
	conn.Start(parent)
	if err := conn.lifetime.Err(); err != nil {
		t.Fatalf("the lifetime ended before the parent did: %v", err)
	}
	endParent()
	select {
	case <-conn.lifetime.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the lifetime outlived the context the connection was started under")
	}
	<-conn.Done()
}
