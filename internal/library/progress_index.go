package library

// IndexProgress preserves ProgressFor's first-match semantics even for duplicate
// imported records. Use only within one render on the library owner's goroutine:
// the index borrows records and must not be cached or published to other goroutines.
func IndexProgress(titleID string, progress []*Progress) map[int]*Progress {
	if titleID == "" {
		return nil
	}
	index := make(map[int]*Progress)
	for _, p := range progress {
		if p.TitleID == titleID {
			if _, exists := index[p.Episode]; !exists {
				index[p.Episode] = p
			}
		}
	}
	return index
}
