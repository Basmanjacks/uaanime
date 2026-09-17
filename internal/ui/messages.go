package ui

import (
	"github.com/Basmanjacks/uaanime/internal/download"
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
	// episodesDoneMsg — список серій приїхав. purpose каже, навіщо його
	// просили (див. epsPurpose): навігаційні відповіді відсікає req, а
	// оновлення (після перегляду, клавіша r) — refreshGen.
	episodesDoneMsg struct {
		ref        provider.TitleRef
		eps        []provider.Episode
		err        error
		offline    bool
		req        int
		purpose    epsPurpose
		refreshGen int
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
	journalMsg struct {
		gen  int
		err  error
		open bool
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
	// remotePlayMsg — адресний запит пульта, що прийшов у простої. Ціль
	// перевіряється ще раз в Update: між постановкою в скриньку й обробкою
	// список міг змінитися.
	remotePlayMsg struct {
		req playback.PlayRequest
	}
	// catalogMsg і libraryEpisodesMsg — пасивні: вони не ведуть нікуди й тому
	// не мають req. Фонове оновлення каталогу не має права ні скасувати
	// навігацію, ні перемалювати екран, на якому людина зараз працює.
	catalogMsg struct {
		kind  provider.CatalogKind
		cards []provider.TitleCard // nil — помилка або мережі немає
	}
	// libraryEpisodesMsg — кеш списків серій бібліотеки оновлено; рядки домівки
	// перечитають його самі.
	libraryEpisodesMsg struct{ seeds []playback.ReleaseSeed }
	// refreshTickMsg — черговий крок фонового циклу оновлення; переозброюється
	// лише зі свого обробника.
	refreshTickMsg struct{}
	// refreshDoneMsg — відповідь на ручне «оновити зараз» із домівки: усі
	// тайтли бібліотеки й блоки каталогу однією операцією. err — перша
	// помилка будь-якої складової; кеш на диску до цього моменту вже оновлено.
	refreshDoneMsg struct {
		seeds      []playback.ReleaseSeed
		catalog    map[provider.CatalogKind][]provider.TitleCard
		err        error
		refreshGen int
	}
	// downloadMsg — менеджер завантажень каже «щось змінилося»; авторитетний
	// стан читається зі Snapshot. ok == false — канал закрито (Close), і
	// переозброювати підписку більше нікуди.
	downloadMsg struct{ ok bool }
	// downloadPlanMsg — відповідь підготовки: реліз обрано, потоки зондовано,
	// список якостей зібрано. req відсікає застарілі, як і в решті навігації.
	downloadPlanMsg struct {
		req  int
		res  *playback.Resolved
		plan *download.Plan
		err  error
	}
	// nyaOffMsg — час кота вийшов; банер повертається до сезонного.
	nyaOffMsg           struct{}
	bookmarkBaselineMsg struct {
		titleID     string
		ref         provider.TitleRef
		provisional int
		maxEp       int
		eps         []provider.Episode
		err         error
	}
)
