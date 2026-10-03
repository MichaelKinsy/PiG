package codingagent

// BuildSessionProjectionCalls returns how many times BuildSessionProjection ran, the count upstream's test reads with vi.spyOn(sessionManager, "buildSessionProjection").
func (s *Session) BuildSessionProjectionCalls() int64 { return s.projectionCalls.Load() }
