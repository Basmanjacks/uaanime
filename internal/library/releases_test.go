package library

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

func releaseLib() *Library {
	return &Library{Titles: []*LocalTitle{{ID: "t", Sources: []provider.TitleRef{{Provider: "p", Slug: "1-title"}}}}, Entries: []*Entry{{TitleID: "t", StudioPin: "A"}}}
}
func releaseEP(n int, studio string, kind provider.Kind) provider.Episode {
	return provider.Episode{Number: n, Releases: []provider.Release{{Studio: studio, Kind: kind}}}
}
func TestReleaseBaselineRoundTripAndNormalize(t *testing.T) {
	for _, raw := range []string{`{}`, `{"release_baseline":{"groups":[]}}`, `{"release_baseline":{"groups":[{"studio":"A","kind":"dub","episodes":"3,0-2,2-5"},{"studio":" A ","kind":"dub","episodes":"8,7"}]}}`, `{"release_baseline":{"groups":[{"studio":"A","kind":"dub","episodes":"-1"}]}}`, `{"release_baseline":42}`} {
		l := releaseLib()
		if err := json.Unmarshal([]byte(raw), l.Entries[0]); err != nil {
			t.Fatal(err)
		}
		l.Entries[0].TitleID = "t"
		if got := l.Normalize(provider.CleanText); got != 0 {
			t.Fatalf("dropped auxiliary data: %d", got)
		}
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		var copy Library
		if err = json.Unmarshal(b, &copy); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(l, &copy) {
			t.Fatalf("roundtrip changed %s", b)
		}
		baseline := l.Entries[0].ReleaseBaseline
		switch {
		case strings.Contains(raw, "3,0-2"):
			if baseline == nil || len(baseline.Groups) != 1 || baseline.Groups[0].Episodes != "0-5,7-8" {
				t.Fatalf("baseline=%+v", baseline)
			}
		case strings.Contains(raw, `"groups":[]`):
			if baseline == nil {
				t.Fatal("known empty became unknown")
			}
		default:
			if baseline != nil {
				t.Fatalf("invalid/absent baseline=%+v", baseline)
			}
		}
	}
}
func TestReleaseIntervalsOverflowAndNoExpansion(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		raw, want string
		valid     bool
	}{{"9,1-3,3-8", "1-9", true}, {"0-" + strconv.Itoa(maxInt), "0-" + strconv.Itoa(maxInt), true}, {"1--2", "", false}, {"2-1", "", false}, {"18446744073709551616", "", false}, {"+1", "", false}} {
		got, ok := parseIntervals(tc.raw)
		if ok != tc.valid {
			t.Fatalf("%q valid=%v", tc.raw, ok)
		}
		if ok && formatIntervals(got) != tc.want {
			t.Fatalf("%q => %q", tc.raw, formatIntervals(got))
		}
	}
}
func TestPreferredFreshLateReleaseAndAcknowledgement(t *testing.T) {
	l := releaseLib()
	eps := []provider.Episode{releaseEP(1, "B", provider.KindDub), releaseEP(20, "A", provider.KindDub)}
	if !l.SeedReleaseBaseline("t", eps) || l.SeedReleaseBaseline("t", nil) {
		t.Fatal("seed must initialize exactly once")
	}
	eps = append(eps, releaseEP(1, "A", provider.KindDub), releaseEP(1, "A", provider.KindDub), releaseEP(2, "A", provider.KindDub))
	l.SetWatched("t", 2, true, time.Time{})
	if studio, n := l.PreferredFresh("t", eps, Prefs{}); studio != "A" || n != 1 {
		t.Fatalf("got %s/%d", studio, n)
	}
	if !l.AcknowledgeReleases("t", []provider.Episode{releaseEP(1, "A", provider.KindDub)}) {
		t.Fatal("ack no change")
	}
	if _, n := l.PreferredFresh("t", eps, Prefs{}); n != 0 {
		t.Fatal(n)
	}
	if l.AcknowledgeReleases("t", []provider.Episode{releaseEP(1, "A", provider.KindDub)}) {
		t.Fatal("repeat ack changed")
	}
	// A new kind from the same studio is a new release even for an old number.
	eps = []provider.Episode{releaseEP(1, "A", provider.KindVoiceover)}
	if _, n := l.PreferredFresh("t", eps, Prefs{}); n != 1 {
		t.Fatal(n)
	}
	// Do not attribute another studio's voiced fallback to the target's subtitles.
	eps = []provider.Episode{{Number: 3, Releases: []provider.Release{{Studio: "A", Kind: provider.KindSub}, {Studio: "B", Kind: provider.KindDub}}}}
	if _, n := l.PreferredFresh("t", eps, Prefs{}); n != 0 {
		t.Fatal(n)
	}
	l.Entries[0].StudioPin = "B"
	if _, n := l.PreferredFresh("t", eps, Prefs{}); n != 1 {
		t.Fatal(n)
	}
	l.Entries[0].StudioPin = ""
	if s, n := l.PreferredFresh("t", eps, Prefs{FavoriteStudio: "B"}); s != "B" || n != 1 {
		t.Fatalf("%s/%d", s, n)
	}
}
func TestReleaseCloneIsolation(t *testing.T) {
	l := releaseLib()
	l.SeedReleaseBaseline("t", []provider.Episode{releaseEP(1, "A", provider.KindDub)})
	clone := l.Clone()
	clone.Entries[0].ReleaseBaseline.Groups[0].Episodes = "9"
	clone.Titles[0].Sources[0].Name = "changed"
	if l.Entries[0].ReleaseBaseline.Groups[0].Episodes != "1" || l.Titles[0].Sources[0].Name != "" {
		t.Fatal("clone aliases original")
	}
}
func largeReleaseLibrary() *Library {
	l := &Library{}
	for title := range 50 {
		id := fmt.Sprint(title)
		l.Titles = append(l.Titles, &LocalTitle{ID: id, Sources: []provider.TitleRef{{Provider: "p", Slug: "1-title"}}})
		l.Entries = append(l.Entries, &Entry{TitleID: id})
		var eps []provider.Episode
		for n := range 1500 {
			for group := range 6 {
				if n%2 == group%2 {
					eps = append(eps, releaseEP(n, fmt.Sprint(group), provider.KindDub))
				}
			}
		}
		l.SeedReleaseBaseline(id, eps)
	}
	return l
}
func TestReleaseBaselineSize(t *testing.T) {
	l := largeReleaseLibrary()
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= 2<<20 {
		t.Fatalf("baseline library: %d bytes", len(data))
	}
	t.Logf("50 titles × 1500 episode numbers × 6 alternating groups: %d bytes", len(data))
}
func BenchmarkReleaseLibrary(b *testing.B) {
	l := largeReleaseLibrary()
	b.Run("clone", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = l.Clone()
		}
	})
	b.Run("normalize", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			l.Normalize(provider.CleanText)
		}
	})
	b.Run("serialize", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := json.MarshalIndent(l, "", "  "); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestMalformedReleaseBaselineShapesRemainUnknown(t *testing.T) {
	for _, baseline := range []string{`{}`, `{"groups":null}`, `{"groups":[null]}`, `{"groups":[{}]}`, `{"groups":[{"studio":"A","kind":"dub"}]}`, `{"groups":[{"studio":null,"kind":"dub","episodes":"1"}]}`} {
		t.Run(baseline, func(t *testing.T) {
			l := releaseLib()
			if err := json.Unmarshal([]byte(`{"title_id":"t","release_baseline":`+baseline+`}`), l.Entries[0]); err != nil {
				t.Fatal(err)
			}
			if n := l.Normalize(provider.CleanText); n != 0 {
				t.Fatalf("discarded %d user records", n)
			}
			if l.Entries[0].ReleaseBaseline != nil {
				t.Fatal("malformed baseline became known empty")
			}
			eps := []provider.Episode{releaseEP(1, "A", provider.KindDub)}
			if !l.SeedReleaseBaseline("t", eps) {
				t.Fatal("corrupt baseline prevented seeding")
			}
			if _, n := l.PreferredFresh("t", eps, Prefs{FavoriteStudio: "A"}); n != 0 {
				t.Fatalf("old release incorrectly counted new: %d", n)
			}
		})
	}
}
