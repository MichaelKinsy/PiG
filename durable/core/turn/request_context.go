// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// RequestContext is the committed context a model request is prepared from: what generation.ts reads of runtime.context
// before it sends a request. The Host gives one either from the core's own derivation (ViewContext) or from the context the
// host derived (Host.RequestContext), so a request never needs the whole transcript in the core.
type RequestContext interface {
	// PromptView is what planning reads: the head marker, the active entries when there is a marker, the system messages.
	PromptView() PromptView
	// SystemMessages are the context's system messages, in order.
	SystemMessages() []*jv.Object
	// Messages are the model messages; the beforeRequest hooks read them. A host that cannot answer fails the handle.
	Messages() []*jv.Object
	// Bounds are the head marker entry (0: none) and the newest entry that fix the context: what a model request names.
	Bounds() (head, tail int64)
	// Threshold is generation.ts thresholdCompaction over the context followed by the planned messages.
	Threshold(planned [][]byte, window float64, p history.Policy) history.Threshold
}

// ViewContext is the context the core derived itself.
type ViewContext struct{ View *history.View }

// PromptView implements RequestContext.
func (c ViewContext) PromptView() PromptView { return promptView(c.View) }

// SystemMessages implements RequestContext.
func (c ViewContext) SystemMessages() []*jv.Object { return systemMessagesOf(c.View) }

// Messages implements RequestContext.
func (c ViewContext) Messages() []*jv.Object { return messagesOf(c.View) }

// Bounds implements RequestContext.
func (c ViewContext) Bounds() (head, tail int64) {
	if c.View.HasHead() {
		head = c.View.HeadID()
	}
	return head, c.View.Tail()
}

// Threshold implements RequestContext.
func (c ViewContext) Threshold(planned [][]byte, window float64, p history.Policy) history.Threshold {
	return c.View.ThresholdCompaction(planned, window, p)
}

// requestContext is runtime.context(conversation, {at}) for a request: like context, with the context the Host serves a
// request from. A Host without RequestContext serves it from the view Context derives.
func (r *run) requestContext(at ID, then func(RequestContext)) {
	var ctx RequestContext
	var scan int
	var keep func(ended bool) error
	var kept KeptRange
	if r.g.Host.KeptRange != nil {
		kept = r.g.Host.KeptRange(r.conv)
	}
	r.read(func() error {
		var err error
		if r.g.Host.RequestContext == nil {
			var view *history.View
			view, scan, keep, err = r.g.Host.Context(r.conv, at, kept)
			ctx = ViewContext{View: view}
			return err
		}
		ctx, scan, keep, err = r.g.Host.RequestContext(r.conv, at, kept)
		return err
	}, func() {
		resolve := func() {
			if keep != nil {
				if err := keep(r.rt.Ended()); err != nil {
					r.fail(err)
					return
				}
			}
			then(ctx)
		}
		if scan > 0 {
			r.scanHops(scan, resolve)
			return
		}
		resolve()
	})
}
