package catalog

// Wait blocks until every started sync finished, without cancelling them (tests only).
func (a *AutoSyncer) Wait() { a.wg.Wait() }
