package playback

import (
	"testing"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestParseKindFilter(t *testing.T) {
	for _, s := range []string{"dub", "voiceover", "sub", "voiced"} {
		if k, ok := ParseKindFilter(s); !ok || string(k) != s {
			t.Errorf("ParseKindFilter(%q) = %q, %v", s, k, ok)
		}
	}
	for _, s := range []string{"", "multi", "Dub", "voice"} {
		if _, ok := ParseKindFilter(s); ok {
			t.Errorf("ParseKindFilter(%q) прийняв", s)
		}
	}
}

func TestReleaseFilterApply(t *testing.T) {
	a := provider.Source{Studio: "FANVOXUA", Kind: provider.KindVoiceover, Embed: "a"}
	aSub := provider.Source{Studio: "FANVOXUA", Kind: provider.KindSub, Embed: "a-sub"}
	b := provider.Source{Studio: "Рідний Голос", Kind: provider.KindDub, Embed: "b"}
	c := provider.Source{Studio: "K0wbassa", Kind: provider.KindSub, Embed: "c"}
	m := provider.Source{Studio: "M", Kind: provider.KindMulti, Embed: "m"}
	all := []provider.Source{a, aSub, b, c, m}
	tests := []struct {
		name     string
		f        ReleaseFilter
		playable []provider.Source
		prefs    library.Prefs
		want     []provider.Source
	}{
		{"voiced без sub і multi", ReleaseFilter{Kind: KindVoiced}, all, library.Prefs{}, []provider.Source{a, b}},
		{"тип", ReleaseFilter{Kind: "sub"}, all, library.Prefs{}, []provider.Source{aSub, c}},
		{"студія без регістру, без sub", ReleaseFilter{Studio: " fanvoxua "}, all, library.Prefs{}, []provider.Source{a}},
		{"студія, людина хоче sub", ReleaseFilter{Studio: "FanVoxUA"}, all, library.Prefs{PreferKind: provider.KindSub}, []provider.Source{a, aSub}},
		{"студія лише в sub, озвучка не грає", ReleaseFilter{Studio: "k0wbassa"}, []provider.Source{c}, library.Prefs{}, []provider.Source{c}},
		{"студія лише в sub, озвучка грає", ReleaseFilter{Studio: "k0wbassa"}, all, library.Prefs{}, nil},
		{"студія і тип sub", ReleaseFilter{Studio: "fanvoxua", Kind: "sub"}, all, library.Prefs{}, []provider.Source{aSub}},
		{"немає", ReleaseFilter{Studio: "X"}, all, library.Prefs{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.f.apply(all, tt.playable, tt.prefs)
			if len(got) != len(tt.want) {
				t.Fatalf("apply = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("apply = %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

// Пін для Pick: студія — так, як її пише сайт; тип фільтра, voiced —
// wildcard; без студії лишається пін тайтлу.
func TestReleaseFilterPin(t *testing.T) {
	filtered := []provider.Source{{Studio: "FANVOXUA", Kind: provider.KindVoiceover}}
	titlePin := library.Pin{Studio: "Other", Kind: provider.KindSub}
	for _, tt := range []struct {
		f    ReleaseFilter
		want library.Pin
	}{
		{ReleaseFilter{Studio: "fanvoxua"}, library.Pin{Studio: "FANVOXUA"}},
		{ReleaseFilter{Studio: "fanvoxua", Kind: KindVoiced}, library.Pin{Studio: "FANVOXUA"}},
		{ReleaseFilter{Studio: "fanvoxua", Kind: "voiceover"}, library.Pin{Studio: "FANVOXUA", Kind: provider.KindVoiceover}},
		{ReleaseFilter{Kind: KindVoiced}, titlePin},
	} {
		if got := tt.f.pin(filtered, titlePin); got != tt.want {
			t.Errorf("%+v: pin = %+v, want %+v", tt.f, got, tt.want)
		}
	}
}
