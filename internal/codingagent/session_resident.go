package codingagent

// residentStore holds the decoded entries of the slots that are resident, by slot index, in pages of residentPage slots allocated when a slot of the page
// gets an entry and freed when its last one goes, so a session that keeps its message bodies out of memory holds a page for the newest messages and for
// each page with an entry of another type, and no word for each slot besides.
// pig additive (D112): Pi keeps every entry as an object; which entries the Go index decodes is a memory choice, and every reader gets the entry decoded.
type residentStore struct {
	pages []*[residentPage]SessionEntry
	// live counts the entries of each page.
	live []int32
}

const (
	residentPageBits = 8
	residentPage     = 1 << residentPageBits
)

// get is the resident entry of slot i, nil when it is not resident.
func (r *residentStore) get(i int) SessionEntry {
	p := i >> residentPageBits
	if p >= len(r.pages) || r.pages[p] == nil {
		return nil
	}
	return r.pages[p][i&(residentPage-1)]
}

// set makes entry the resident entry of slot i, or none when it is nil.
func (r *residentStore) set(i int, entry SessionEntry) {
	p := i >> residentPageBits
	if entry == nil {
		if p >= len(r.pages) || r.pages[p] == nil || r.pages[p][i&(residentPage-1)] == nil {
			return
		}
		r.pages[p][i&(residentPage-1)] = nil
		if r.live[p]--; r.live[p] == 0 {
			r.pages[p] = nil
		}
		return
	}
	for p >= len(r.pages) {
		r.pages, r.live = append(r.pages, nil), append(r.live, 0)
	}
	if r.pages[p] == nil {
		r.pages[p] = new([residentPage]SessionEntry)
	}
	if r.pages[p][i&(residentPage-1)] == nil {
		r.live[p]++
	}
	r.pages[p][i&(residentPage-1)] = entry
}
