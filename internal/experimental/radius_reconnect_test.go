package experimental

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type fakeReconnectClient struct {
	mu                 sync.Mutex
	connected          bool
	attachment         *RadiusClientAttachment
	connectionListener func(string)
	attachmentListener func(*RadiusClientAttachment)
	reconnect          func(context.Context) error
}

func (c *fakeReconnectClient) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }
func (c *fakeReconnectClient) ConnectionState() string {
	if c.Connected() {
		return "connected"
	}
	return "disconnected"
}
func (c *fakeReconnectClient) Attachment() *RadiusClientAttachment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attachment
}
func (c *fakeReconnectClient) Disconnect(string) { c.setConnected(false) }
func (c *fakeReconnectClient) setConnected(connected bool) {
	c.mu.Lock()
	c.connected = connected
	listener := c.connectionListener
	c.mu.Unlock()
	if listener != nil {
		state := "disconnected"
		if connected {
			state = "connected"
		}
		listener(state)
	}
}
func (c *fakeReconnectClient) setAttachment(attachment *RadiusClientAttachment) {
	c.mu.Lock()
	c.attachment = attachment
	listener := c.attachmentListener
	c.mu.Unlock()
	if listener != nil {
		listener(attachment)
	}
}
func (c *fakeReconnectClient) OnConnectionStateChange(listener func(string)) func() {
	c.mu.Lock()
	c.connectionListener = listener
	c.mu.Unlock()
	return func() { c.mu.Lock(); c.connectionListener = nil; c.mu.Unlock() }
}
func (c *fakeReconnectClient) OnAttachmentChange(listener func(*RadiusClientAttachment)) func() {
	c.mu.Lock()
	c.attachmentListener = listener
	c.mu.Unlock()
	return func() { c.mu.Lock(); c.attachmentListener = nil; c.mu.Unlock() }
}
func (c *fakeReconnectClient) Reconnect(ctx context.Context) error { return c.reconnect(ctx) }

// The pinned upstream test retries at 1s then 2s and restores demo-1 only after connection succeeds.
func TestRadiusReconnectRestoresSelectedSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &fakeReconnectClient{connected: true, attachment: &RadiusClientAttachment{SessionID: "demo-1"}}
		var attempts atomic.Int32
		client.reconnect = func(context.Context) error {
			if attempts.Add(1) == 1 {
				return errors.New("temporary failure")
			}
			client.setConnected(true)
			return nil
		}
		restored := make(chan string, 2)
		reconnect := NewRadiusClientReconnect(t.Context(), client, func(_ context.Context, id string) error { restored <- id; return nil })
		client.Disconnect("lost")
		client.setAttachment(nil)
		client.Disconnect("duplicate")
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if attempts.Load() != 1 || len(restored) != 0 {
			t.Fatalf("after 1s: %d restores=%d", attempts.Load(), len(restored))
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if attempts.Load() != 2 || len(restored) != 1 {
			t.Fatalf("after 3s: %d restores=%d", attempts.Load(), len(restored))
		}
		if id := <-restored; id != "demo-1" {
			t.Fatal(id)
		}
		reconnect.Dispose()
		reconnect.Dispose()
		if client.Connected() || client.connectionListener != nil || client.attachmentListener != nil {
			t.Fatal("dispose retained connection/listeners")
		}
	})
}

func TestRadiusReconnectSelectionAndReattachFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &fakeReconnectClient{connected: true, attachment: &RadiusClientAttachment{SessionID: "original"}}
		attempts := 0
		client.reconnect = func(context.Context) error { attempts++; client.setConnected(true); return nil }
		var restored []string
		reconnect := NewRadiusClientReconnect(t.Context(), client, func(_ context.Context, id string) error {
			restored = append(restored, id)
			if len(restored) == 1 {
				return errors.New("attach failed")
			}
			return nil
		})
		defer reconnect.Dispose()
		client.setAttachment(&RadiusClientAttachment{SessionID: "selected"})
		client.Disconnect("lost")
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if client.Connected() {
			t.Fatal("failed reattach left client connected")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if attempts != 2 || len(restored) != 2 || restored[0] != "selected" || restored[1] != "selected" {
			t.Fatalf("retry = %d %v", attempts, restored)
		}
		client.setAttachment(nil)
		client.Disconnect("lost")
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if attempts != 3 || len(restored) != 2 {
			t.Fatalf("explicit detach restored session: %d %v", attempts, restored)
		}
	})
}

func TestRadiusReconnectCancellationJoinsInflight(t *testing.T) {
	for _, phase := range []string{"delay", "reconnect", "reattach"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := &fakeReconnectClient{connected: true, attachment: &RadiusClientAttachment{SessionID: "selected"}}
				finished := false
				client.reconnect = func(ctx context.Context) error {
					if phase == "reconnect" {
						<-ctx.Done()
						finished = true
						return ctx.Err()
					}
					client.setConnected(true)
					return nil
				}
				reconnect := NewRadiusClientReconnect(t.Context(), client, func(ctx context.Context, _ string) error { <-ctx.Done(); finished = true; return ctx.Err() })
				client.Disconnect("lost")
				synctest.Wait()
				if phase != "delay" {
					time.Sleep(time.Second)
					synctest.Wait()
				}
				reconnect.Dispose()
				if phase != "delay" && !finished {
					t.Fatal("dispose did not join operation")
				}
				if client.Connected() {
					t.Fatal("dispose left connected")
				}
			})
		})
	}
}
