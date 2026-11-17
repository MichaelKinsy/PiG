package subprocess

import "sync"

// Pi hangs the report of a refused pi.sendUserMessage / pi.sendMessage on the Promise the Session method returns
// (`.catch((err) => runner.emitError(...))`, agent-session.ts:3544-3546 and :3552-3555 at pi b4afdddf44e0). The catch callback runs in a microtask, so
// the report comes after every effect of the code that made the call, which has not yet yielded: the queue_update of a steer sent later in the same run, an
// entry that run appended, a notification. The host makes each such report with the function the Session was given (SendUserMessageOptions.AfterRun) and
// holds it until the code that sent the message ends its run, in the order the sends were made:
//   - a runtime with an event loop (Node) reports the end (NotifyHandlerYielded) for the token the send carried (yield_token), whichever code sent it;
//   - a thread-based SDK's code that runs inside a request ends its run when the request ends (a handler that returns, fails or is cancelled), or at the
//     explicit yield (CallYield) and the calls whose upstream API returns a Promise;
//   - a connection that ends makes the reports it still holds;
//   - a thread outside any request has no boundary: its report is made at once (pig divergence, docs/extension-api-parity.md).

// pendingReport is one held report: the connection and the request or token whose run it waits for.
type pendingReport struct {
	conn    *Conn
	request string
	token   string
	report  func()
}

// syncRunReports are the reports of failed sends that wait for the end of their sender's run.
type syncRunReports struct {
	// runMu serializes the making of reports, so two ends that race make theirs in the order the sends were made. It is taken before mu.
	runMu   sync.Mutex
	mu      sync.Mutex
	pending []pendingReport
	// tokens are the sends of a runtime with an event loop whose run has not ended.
	tokens map[syncRunKey]struct{}
}

// after returns the function that makes report once the run of the code that made a send ends. request is the request whose handler made it (empty
// outside any request); token is the yield_token the send carried. A send whose code has no boundary gets nil: its report is made at once.
func (s *syncRunReports) after(conn *Conn, request, token string) func(report func()) {
	if token != "" {
		s.mu.Lock()
		if s.tokens == nil {
			s.tokens = make(map[syncRunKey]struct{})
		}
		s.tokens[syncRunKey{conn: conn, token: token}] = struct{}{}
		s.mu.Unlock()
	} else if request == "" {
		return nil
	} else if _, inFlight := conn.requestEvent(request); !inFlight {
		return nil
	}
	return func(report func()) { s.add(conn, request, token, report) }
}

// add holds report until the run ends. A run that already ended (the send's own work finished after it) makes it at once.
func (s *syncRunReports) add(conn *Conn, request, token string, report func()) {
	s.mu.Lock()
	var live bool
	if token != "" {
		_, live = s.tokens[syncRunKey{conn: conn, token: token}]
	} else {
		_, live = conn.requestEvent(request)
	}
	if live {
		s.pending = append(s.pending, pendingReport{conn: conn, request: request, token: token, report: report})
	}
	s.mu.Unlock()
	if !live {
		report()
	}
}

// take removes the reports select picks, in the order they were held.
func (s *syncRunReports) take(pick func(pendingReport) bool) []func() {
	var taken []func()
	kept := s.pending[:0]
	for _, p := range s.pending {
		if pick(p) {
			taken = append(taken, p.report)
		} else {
			kept = append(kept, p)
		}
	}
	clear(s.pending[len(kept):])
	s.pending = kept
	return taken
}

func makeReports(reports []func()) {
	for _, report := range reports {
		report()
	}
}

// endToken ends the run of the send that carried token: its reports, and the earlier ones of the connection's event loop, are made.
func (s *syncRunReports) endToken(conn *Conn, token string) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	last := -1
	for i, p := range s.pending {
		if p.conn == conn && p.token == token {
			last = i
		}
	}
	index := 0
	reports := s.take(func(p pendingReport) bool {
		index++
		return index-1 <= last && p.conn == conn && p.token != ""
	})
	delete(s.tokens, syncRunKey{conn: conn, token: token})
	s.mu.Unlock()
	makeReports(reports)
}

// endRequest ends the run of the handler of request: the reports of the sends it made are made.
func (s *syncRunReports) endRequest(conn *Conn, request string) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	reports := s.take(func(p pendingReport) bool { return p.conn == conn && p.request == request })
	s.mu.Unlock()
	makeReports(reports)
}

// closed makes the reports a connection that ended still holds, in the order of the sends: nothing yields on it any more.
func (s *syncRunReports) closed(conn *Conn) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	reports := s.take(func(p pendingReport) bool { return p.conn == conn })
	for key := range s.tokens {
		if key.conn == conn {
			delete(s.tokens, key)
		}
	}
	s.mu.Unlock()
	makeReports(reports)
}

// inAgentSettled reports whether request is an agent_settled event handler, whose sends the Session holds back.
func inAgentSettled(conn *Conn, request string) bool {
	event, _ := conn.requestEvent(request)
	return event == "agent_settled"
}
