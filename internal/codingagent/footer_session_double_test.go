package codingagent

// testFooterSession is the FooterSession a footer test binds when it needs particular totals or routed model without a whole InteractiveMode.
type testFooterSession struct {
	totals func() footerUsageTotals
	routed func() *RoutedModelSelection
}

var _ FooterSession = testFooterSession{}

// SessionManager is a session log whose accounting holds the totals the test asked for at the time of the call.
func (s testFooterSession) SessionManager() *Session {
	manager := NewSession("footer-double", "/")
	if s.totals == nil {
		return manager
	}
	totals := s.totals()
	manager.stats.stats.Tokens = SessionTokenStats{Input: totals.input, Output: totals.output, CacheRead: totals.cacheRead, CacheWrite: totals.cacheWrite, Cost: totals.cost}
	manager.stats.stats.LatestCacheHitRate = totals.latestCacheHitRate
	return manager
}

func (s testFooterSession) RoutedModelSelection() *RoutedModelSelection {
	if s.routed == nil {
		return nil
	}
	return s.routed()
}
