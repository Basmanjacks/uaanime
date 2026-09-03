package ui

import (
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Повідомлення асинхронних команд.
type (
	searchDoneMsg struct {
		cards   []provider.TitleCard
		hasMore bool
		page    int // сторінка, яку просили: 1 замінює список, решта — дозаписує
		err     error
		req     int
	}
	episodesDoneMsg struct {
		ref      provider.TitleRef
		eps      []provider.Episode
		err      error
		offline  bool
		req      int
		navigate bool
	}
	resolvedMsg struct {
		res *playback.Resolved
		err error
		req int
	}
	studiosMsg struct {
		choices []provider.Source
		err     error
		req     int
	}
	playDoneMsg struct {
		reason player.EndReason
		err    error
	}
	// liveMsg — знімок сесії, що грає. gen — покоління, за яким відкидаються
	// відповіді попередньої сесії; periodic позначає відповідь тіка: лише
	// вона має право переозброїти цикл, інакше кожна клавіша плодила б свій.
	liveMsg struct {
		periodic bool
		gen      int
		snap     playback.Snapshot
		err      error
	}
	// catalogMsg і badgesMsg — пасивні: вони не ведуть нікуди й тому не мають
	// req. Фонове оновлення каталогу не має права ні скасувати навігацію,
	// ні перемалювати екран, на якому людина зараз працює.
	catalogMsg struct {
		kind  provider.CatalogKind
		cards []provider.TitleCard // nil — помилка або мережі немає
	}
	badgesMsg struct {
		counts map[string]int // локальний ID тайтла → скільки нових серій
	}
	bookmarkBaselineMsg struct {
		titleID     string
		ref         provider.TitleRef
		provisional int
		maxEp       int
		err         error
	}
)
