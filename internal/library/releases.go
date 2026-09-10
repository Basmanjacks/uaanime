package library

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// ReleaseBaseline distinguishes an unknown baseline (nil) from a known empty
// release set. Intervals keep long-running titles compact without a cutoff.
type ReleaseBaseline struct {
	Groups []ReleaseGroup `json:"groups"`
}
type ReleaseGroup struct {
	Studio   string        `json:"studio"`
	Kind     provider.Kind `json:"kind"`
	Episodes string        `json:"episodes"`
}

// UnmarshalJSON isolates corrupt auxiliary metadata from durable user records.
// A malformed baseline can be reseeded; rejecting an otherwise valid import
// would needlessly prevent recovery of watch progress.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type entry Entry
	var raw struct {
		*entry
		Baseline json.RawMessage `json:"release_baseline"`
	}
	raw.entry = (*entry)(e)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.ReleaseBaseline = nil
	if len(raw.Baseline) > 0 && string(raw.Baseline) != "null" {
		// Pointers distinguish required fields from absent/null JSON values.
		// Otherwise encoding/json turns corrupt shapes into a known empty set,
		// preventing the migration seed and falsely flagging old releases.
		var baseline struct {
			Groups []*struct {
				Studio   *string        `json:"studio"`
				Kind     *provider.Kind `json:"kind"`
				Episodes *string        `json:"episodes"`
			} `json:"groups"`
		}
		if err := json.Unmarshal(raw.Baseline, &baseline); err != nil || baseline.Groups == nil {
			return nil
		}
		decoded := &ReleaseBaseline{Groups: make([]ReleaseGroup, 0, len(baseline.Groups))}
		for _, group := range baseline.Groups {
			if group == nil || group.Studio == nil || group.Kind == nil || group.Episodes == nil {
				return nil
			}
			decoded.Groups = append(decoded.Groups, ReleaseGroup{Studio: *group.Studio, Kind: *group.Kind, Episodes: *group.Episodes})
		}
		e.ReleaseBaseline = normalizeBaseline(decoded, provider.CleanText)
	}
	return nil
}

type interval struct{ first, last int }

func parseIntervals(raw string) ([]interval, bool) {
	if raw == "" {
		return nil, true
	}
	var ranges []interval
	number := func(s string) (int, bool) {
		if s == "" {
			return 0, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	for part := range strings.SplitSeq(raw, ",") {
		a, b, hasRange := strings.Cut(part, "-")
		first, ok := number(a)
		if !ok {
			return nil, false
		}
		last := first
		if hasRange {
			last, ok = number(b)
			if !ok || last < first {
				return nil, false
			}
		}
		ranges = append(ranges, interval{first, last})
	}
	return mergeIntervals(ranges), true
}
func mergeIntervals(ranges []interval) []interval {
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].first < ranges[j].first || (ranges[i].first == ranges[j].first && ranges[i].last < ranges[j].last)
	})
	out := ranges[:0]
	for _, r := range ranges {
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if r.first <= prev.last || (r.first > 0 && r.first-1 == prev.last) {
				prev.last = max(prev.last, r.last)
				continue
			}
		}
		out = append(out, r)
	}
	return out
}
func formatIntervals(ranges []interval) string {
	var b strings.Builder
	for i, r := range ranges {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(r.first))
		if r.first != r.last {
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(r.last))
		}
	}
	return b.String()
}
func normalizeBaseline(b *ReleaseBaseline, clean func(string) string) *ReleaseBaseline {
	if b == nil {
		return nil
	}
	groups := map[provider.Release][]interval{}
	for _, g := range b.Groups {
		ranges, ok := parseIntervals(g.Episodes)
		if !ok {
			return nil
		}
		g.Studio = clean(g.Studio)
		if g.Studio == "" || len(ranges) == 0 {
			continue
		}
		if !provider.ValidKind(g.Kind) {
			g.Kind = provider.KindMulti
		}
		key := provider.Release{Studio: g.Studio, Kind: g.Kind}
		groups[key] = append(groups[key], ranges...)
	}
	return baselineFromGroups(groups)
}
func baselineFromGroups(groups map[provider.Release][]interval) *ReleaseBaseline {
	b := &ReleaseBaseline{Groups: make([]ReleaseGroup, 0, len(groups))}
	for key, ranges := range groups {
		if len(ranges) > 0 {
			b.Groups = append(b.Groups, ReleaseGroup{Studio: key.Studio, Kind: key.Kind, Episodes: formatIntervals(mergeIntervals(ranges))})
		}
	}
	sort.Slice(b.Groups, func(i, j int) bool {
		a, c := b.Groups[i], b.Groups[j]
		return a.Studio < c.Studio || (a.Studio == c.Studio && a.Kind < c.Kind)
	})
	return b
}
func releaseGroups(eps []provider.Episode) map[provider.Release][]interval {
	groups := map[provider.Release][]interval{}
	for _, ep := range eps {
		if ep.Number < 0 {
			continue
		}
		for _, r := range provider.CleanEpisode(ep).Releases {
			groups[r] = append(groups[r], interval{ep.Number, ep.Number})
		}
	}
	return groups
}

// SeedReleaseBaseline captures all current studios and kinds exactly once.
func (l *Library) SeedReleaseBaseline(titleID string, episodes []provider.Episode) bool {
	entry := l.EntryLookup(titleID)
	if entry == nil || entry.ReleaseBaseline != nil {
		return false
	}
	entry.ReleaseBaseline = baselineFromGroups(releaseGroups(episodes))
	return true
}

// AcknowledgeReleases adds only supplied releases and retains historical groups,
// so temporary provider omissions never resurrect acknowledged badges.
func (l *Library) AcknowledgeReleases(titleID string, episodes []provider.Episode) bool {
	entry := l.EntryLookup(titleID)
	if entry == nil {
		return false
	}
	groups := releaseGroups(episodes)
	if entry.ReleaseBaseline != nil {
		for _, g := range entry.ReleaseBaseline.Groups {
			ranges, ok := parseIntervals(g.Episodes)
			if ok {
				key := provider.Release{Studio: g.Studio, Kind: g.Kind}
				groups[key] = append(groups[key], ranges...)
			}
		}
	}
	next := baselineFromGroups(groups)
	if reflect.DeepEqual(entry.ReleaseBaseline, next) {
		return false
	}
	entry.ReleaseBaseline = next
	return true
}

// PreferredFresh counts unique unfinished episode numbers whose chosen release
// belongs to the pinned (or global favorite) studio and has not been acknowledged.
// Pick receives metadata-only sources, preserving playback's fallback rules.
func (l *Library) PreferredFresh(titleID string, episodes []provider.Episode, prefs Prefs) (studio string, count int) {
	entry := l.EntryLookup(titleID)
	pin := Pin{}
	if entry != nil {
		pin = Pin{Studio: entry.StudioPin, Kind: entry.KindPin}
	}
	studio = pin.Studio
	if studio == "" {
		studio = prefs.FavoriteStudio
	}
	if studio == "" || entry == nil || entry.ReleaseBaseline == nil {
		return studio, 0
	}
	baseline := map[provider.Release][]interval{}
	for _, g := range entry.ReleaseBaseline.Groups {
		ranges, ok := parseIntervals(g.Episodes)
		if !ok {
			return studio, 0
		}
		baseline[provider.Release{Studio: g.Studio, Kind: g.Kind}] = ranges
	}
	completed := map[int]bool{}
	for _, p := range l.Progress {
		if p != nil && p.TitleID == titleID && p.Completed {
			completed[p.Episode] = true
		}
	}
	sources := map[int][]provider.Source{}
	for _, ep := range episodes {
		if ep.Number < 0 || completed[ep.Number] {
			continue
		}
		for _, r := range provider.CleanEpisode(ep).Releases {
			sources[ep.Number] = append(sources[ep.Number], provider.Source{Episode: ep.Number, Studio: r.Studio, Kind: r.Kind})
		}
	}
	for number, releases := range sources {
		chosen, _ := Pick(releases, pin, prefs)
		if chosen == nil || chosen.Studio != studio {
			continue
		}
		ranges := baseline[provider.Release{Studio: chosen.Studio, Kind: chosen.Kind}]
		i := sort.Search(len(ranges), func(i int) bool { return ranges[i].last >= number })
		if i == len(ranges) || ranges[i].first > number {
			count++
		}
	}
	return studio, count
}

// Clone copies every mutable layer so failed staged writes cannot change the
// currently published library or leave retained pointers partially modified.
func (l *Library) Clone() *Library {
	if l == nil {
		return nil
	}
	out := *l
	if l.Titles != nil {
		out.Titles = make([]*LocalTitle, len(l.Titles))
		for i, t := range l.Titles {
			if t != nil {
				c := *t
				if t.Sources != nil {
					c.Sources = append([]provider.TitleRef{}, t.Sources...)
				}
				out.Titles[i] = &c
			}
		}
	}
	if l.Entries != nil {
		out.Entries = make([]*Entry, len(l.Entries))
		for i, e := range l.Entries {
			if e != nil {
				c := *e
				if e.ReleaseBaseline != nil {
					b := *e.ReleaseBaseline
					if b.Groups != nil {
						b.Groups = append([]ReleaseGroup{}, b.Groups...)
					}
					c.ReleaseBaseline = &b
				}
				out.Entries[i] = &c
			}
		}
	}
	if l.Progress != nil {
		out.Progress = make([]*Progress, len(l.Progress))
		for i, p := range l.Progress {
			if p != nil {
				c := *p
				out.Progress[i] = &c
			}
		}
	}
	return &out
}
