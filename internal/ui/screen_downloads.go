package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// dlKey — ключ знімка черги. Той самий, яким дедуплікує сам менеджер
// (download.jobKey): серія тайтлу або в роботі, або ні, і двох записів про неї
// бути не може.
func dlKey(ref provider.TitleRef, ep int) string {
	return download.RefKey(ref) + ":" + strconv.Itoa(ep)
}

// downloadDir — папка завантажень зі знімка конфігу; порожньо, коли конфігу
// немає взагалі (бенчмарк рендера будує модель напряму).
func (m *Model) downloadDir() string {
	if m.cfg == nil {
		return ""
	}
	return m.cfg.DownloadDir
}

// savedFor — що з цього тайтлу вже лежить на диску. Читання папки коштує
// кількох stat-ів, тому кешується на одну перебудову екрана — як epsScratch.
//
// Без менеджера (Options.Downloads == nil) диск не читаємо взагалі: у такій
// збірці завантажень немає, і бейдж «на диску» нізвідки взятися — натомість
// кожен рядок списку ходив би в чужу файлову систему.
func (m *Model) savedFor(ref provider.TitleRef) map[int][]download.SavedFile {
	dir := m.downloadDir()
	if m.dl == nil || dir == "" {
		return nil
	}
	key := download.RefKey(ref)
	if files, ok := m.savedScratch[key]; ok {
		return files
	}
	files := download.Saved(dir, ref)
	if m.savedScratch != nil {
		m.savedScratch[key] = files
	}
	return files
}

// resetSavedScratch — папка або її вміст змінилися: наступне читання має піти
// на диск. Кличеться при зміні налаштування папки і на кожному Done.
func (m *Model) resetSavedScratch() {
	m.savedScratch = map[string]map[int][]download.SavedFile{}
}

// downloadPercent — цілий відсоток завдання; саме він, а не байти, вирішує, чи
// перемальовувати список (D1 «Тиха черга»: рядок не має миготіти).
func downloadPercent(p download.Progress) int {
	if p.Total <= 0 {
		if p.Segments > 0 {
			return min(100, p.SegmentsDone*100/p.Segments)
		}
		return 0
	}
	return min(100, int(p.Bytes*100/p.Total))
}

// bestSaved — найвища якість серед файлів серії.
func bestSaved(files []download.SavedFile) download.SavedFile {
	var best download.SavedFile
	for i, f := range files {
		if i == 0 || f.Height > best.Height {
			best = f
		}
	}
	return best
}

// ---- бейджі екрана серій ----

// applyDownloadBadge доповнює рядок серії станом завантаження. Бейдж
// перекриває те, що поставив перегляд: «на диску» дієвіше за «переглянуто» —
// саме воно каже, що Enter не піде в мережу. Іконку не чіпаємо: ✓ у колонці
// ліворуч лишається позначкою перегляду.
func (m *Model) applyDownloadBadge(it *item, ep int, saved map[int][]download.SavedFile) {
	if files := saved[ep]; len(files) > 0 {
		it.badge, it.badgeWarn = i18n.TuiDlBadgeSaved, false
		if h := bestSaved(files).Height; h > 0 {
			if it.meta == "" {
				it.meta = i18n.QualityLabel(h)
			} else {
				it.meta += metaSep + i18n.QualityLabel(h)
			}
		}
		return
	}
	p, ok := m.downloads[dlKey(m.ref, ep)]
	if !ok {
		return
	}
	switch p.State {
	case download.StateQueued:
		it.badge, it.badgeWarn = i18n.TuiDlBadgeQueued, false
	case download.StateResolving:
		it.badge, it.badgeWarn = i18n.TuiDlBadgePreparing, false
	case download.StateRunning:
		it.badge, it.badgeWarn = fmt.Sprintf(i18n.TuiDlBadgeActive, downloadPercent(p)), false
	case download.StateFailed:
		it.badge, it.badgeWarn = i18n.TuiDlBadgeFailed, true
	}
}

// ---- підготовка й екран якості ----

// requestDownload — клавіша D на екрані серій. Два свідомі натискання й
// жодного третього екрана: тут лише підготовка, вибір якості робить Enter.
func (m Model) requestDownload() (tea.Model, tea.Cmd) {
	if m.dl == nil {
		m.status = i18n.TuiDlUnavailable
		m.statusKind = statusWarning
		m.statusGen++
		return m, nil
	}
	it, ok := m.list.SelectedItem().(item)
	if !ok || it.header {
		return m, nil
	}
	p, ok := it.payload.(payloadEp)
	if !ok {
		return m, nil
	}
	// Навігація в польоті або сесія плеєра: D не має права перебивати ні те,
	// ні інше — резолв усе одно пішов би в ту саму чергу джерел. Відмова
	// завжди зі статусом: мовчазне «нічого не сталося» не відрізнити від
	// клавіші, що не дійшла.
	if m.pending != nil {
		m.status = i18n.TuiDlBusyNav
		m.statusKind = statusWarning
		m.statusGen++
		return m, nil
	}
	if m.playCancel != nil {
		m.status = i18n.TuiDlBusyPlaying
		m.statusKind = statusWarning
		m.statusGen++
		return m, nil
	}
	if files := m.savedFor(m.ref)[p.num]; len(files) > 0 {
		m.status = fmt.Sprintf(i18n.TuiDlAlready, p.num)
		m.statusKind = statusInfo
		m.statusGen++
		return m, nil
	}
	if job, ok := m.downloads[dlKey(m.ref, p.num)]; ok && !job.State.Terminal() {
		m.status = fmt.Sprintf(i18n.TuiDlAlreadyQueued, p.num)
		m.statusKind = statusInfo
		m.statusGen++
		return m, nil
	}
	snap := m.snapshot()
	req := m.beginNav()
	m.pending = &snap
	m.pendingReq = req
	m.pendingEp = p.num
	m.errText = ""
	m.status = i18n.TuiDlPreparing
	m.statusKind = statusLoading
	m.statusGen++
	return m, m.downloadPlanCmd(m.ref, p.num, req, m.eng.ResolveHints(m.ref, p.num))
}

// showDownloadQuality — екран вибору якості: рядки якостей, курсор на
// найвищій, і два довідкові рядки під ними — куди ляже файл і чому розмір
// приблизний. Заголовок каже, ЩО саме зберігається: серія, тип, студія.
func (m *Model) showDownloadQuality(res *playback.Resolved, plan *download.Plan) {
	m.setScreen(screenDownloadQuality)
	m.dlTitle = m.downloadQualityTitle(res)
	items := make([]item, 0, len(plan.Qualities)+3)
	for _, q := range plan.Qualities {
		items = append(items, item{
			title:   i18n.QualityLabel(q.Height),
			meta:    qualitySize(q.Bytes, q.Exact),
			payload: payloadQuality{height: q.Height, bytes: q.Bytes, exact: q.Exact},
		})
	}
	approx := false
	for _, q := range plan.Qualities {
		if !q.Exact {
			approx = true
		}
	}
	items = append(items, item{header: true, spacer: true})
	items = append(items, m.note(fmt.Sprintf(i18n.TuiDlFolder, m.downloadFolderHint(res.Name))))
	if approx {
		items = append(items, m.note(i18n.TuiDlSizeNote))
	}
	_ = m.setItems(items, 0)
}

func (m *Model) downloadQualityTitle(res *playback.Resolved) string {
	title := fmt.Sprintf(i18n.TuiDlQualityTitle, res.Episode)
	if res.Source.Kind != "" {
		title += metaSep + i18n.KindShort(res.Source.Kind)
	}
	if res.Source.Studio != "" {
		title += metaSep + res.Source.Studio
	}
	return title
}

// downloadFolderHint — куди ляже файл. Точну папку обирає job під локом
// (FolderFor може додати суфікс на колізії назв), тому це саме підказка, і
// будується вона тим самим FolderName, що й ім'я папки.
func (m *Model) downloadFolderHint(name string) string {
	if name == "" {
		name = m.currentTitleName()
	}
	return shortenHome(filepath.Join(m.downloadDir(), download.FolderName(name)))
}

func qualitySize(bytes int64, exact bool) string {
	switch {
	case bytes <= 0:
		return i18n.TuiDlSizeUnknown
	case exact:
		return i18n.Bytes(bytes)
	default:
		return fmt.Sprintf(i18n.TuiDlSizeApprox, i18n.Bytes(bytes))
	}
}

// enqueueQuality — Enter на рядку якості. Кадр екрана якості знімається зі
// стека (як після пікера студій): повернення Esc з наступного екрана має вести
// до списку серій, а не назад у вибір якості, який уже зроблено.
func (m Model) enqueueQuality(height int) (tea.Model, tea.Cmd) {
	if m.dl == nil {
		m.status = i18n.TuiDlUnavailable
		m.statusKind = statusWarning
		m.statusGen++
		return m, nil
	}
	ref, ep := m.ref, m.pendingEp
	if _, err := m.dl.Enqueue(m.downloadJob(ref, ep, height)); err != nil {
		m.errText = m.errorText(err)
		return m, nil
	}
	// Знімок оновлюємо ДО повернення: рядки серій перебудовує сам back(), і
	// бейдж «у черзі» має бути в моделі вже на цей момент.
	m.applyDownloadSnapshot()
	m.back()
	m.status = fmt.Sprintf(i18n.TuiDlQueued, ep, i18n.Downloads(len(m.downloads)))
	m.statusKind = statusSuccess
	m.statusGen++
	return m, nil
}

// downloadJob збирає завдання черги. Резолюція відкладена в замикання: URL
// потоків протухають (правило 5), тож їх здобувають на старті завдання, а не
// в момент постановки. Замикання тримає лише знімок підказок і рушій —
// бібліотеки воно не торкається (правило 10).
func (m *Model) downloadJob(ref provider.TitleRef, ep, height int) download.Job {
	eng := m.eng
	h := m.eng.ResolveHints(ref, ep)
	h.NoLocal = true
	h.StartSec = 0
	return download.Job{
		Ref:     ref,
		Episode: ep,
		Height:  height,
		Dir:     m.downloadDir(),
		Resolve: func(ctx context.Context) (download.Target, error) {
			res, err := eng.ResolveWith(ctx, ref, ep, h, nil)
			if err != nil {
				return download.Target{}, err
			}
			return download.Target{
				Streams: res.Streams,
				Studio:  res.Source.Studio,
				Kind:    res.Source.Kind,
				Name:    res.Name,
			}, nil
		},
	}
}

// ---- екран «Завантаження» ----

func (m *Model) showDownloads() {
	m.setScreen(screenDownloads)
	m.resetSavedScratch()
	rows := m.downloadRows()
	_ = m.setItems(rows, firstRow(rows))
	m.status = ""
	m.statusKind = statusInfo
	m.statusGen++
}

// downloadsTitle — «Завантаження · 1 активне · 2 у черзі». Нулі не
// показуються: заголовок без хвоста і є «нічого не качається».
func (m *Model) downloadsTitle() string {
	active, queued := 0, 0
	for _, p := range m.downloads {
		switch p.State {
		case download.StateRunning, download.StateResolving:
			active++
		case download.StateQueued:
			queued++
		}
	}
	title := i18n.TuiDlTitle
	if active > 0 {
		title += metaSep + i18n.ActiveDownloads(active)
	}
	if queued > 0 {
		title += metaSep + i18n.QueuedDownloads(queued)
	}
	return title
}

// downloadRows — дві частини одного екрана: що відбувається зараз і що вже
// лежить на диску. Черга — зі знімка менеджера (авторитетний стан), збережене —
// з папки (істина на диску, а не з нашої пам'яті).
func (m *Model) downloadRows() []item {
	var items []item
	if now := m.nowRows(); len(now) > 0 {
		items = append(items, item{header: true, title: i18n.TuiDlBlockNow})
		items = append(items, now...)
	}
	saved := m.savedRows()
	if len(saved) > 0 && len(items) > 0 {
		items = append(items, item{header: true, spacer: true})
	}
	items = append(items, saved...)
	if len(items) == 0 {
		items = append(items, m.note(i18n.TuiDlEmpty))
	}
	return items
}

// nowRows — черга цієї сесії: активне, те, що чекає, і невдалі/скасовані, поки
// їх не прибрали клавішею X. Завершені сюди не потрапляють: їхній файл уже
// видно в секції тайтлу нижче, і дубль читався б як два різні файли.
func (m *Model) nowRows() []item {
	if m.dl == nil {
		return nil
	}
	var items []item
	for _, p := range m.dl.Snapshot() {
		if p.State == download.StateDone {
			continue
		}
		it := item{
			icon:    m.ic.Pending,
			title:   fmt.Sprintf(i18n.TuiDlEpisodeRow, m.refName(p.Ref), p.Episode),
			payload: payloadDownload{id: p.ID, state: p.State, ref: p.Ref, ep: p.Episode},
		}
		parts := []string{}
		if p.Height > 0 {
			parts = append(parts, i18n.QualityLabel(p.Height))
		}
		switch p.State {
		case download.StateRunning:
			it.icon = m.ic.Play
			if p.BytesPerSec > 0 {
				parts = append(parts, fmt.Sprintf(i18n.TuiDlMetaRate,
					i18n.Bytes(int64(p.BytesPerSec)), i18n.HumanDuration(p.ETA.Seconds())))
			}
			it.badge = fmt.Sprintf(i18n.TuiDlBadgePercent, downloadPercent(p))
		case download.StateResolving:
			it.badge = i18n.TuiDlBadgePreparing
		case download.StateQueued:
			if p.Total > 0 {
				parts = append(parts, qualitySize(p.Total, p.Exact))
			}
			it.badge = i18n.TuiDlBadgeQueued
		case download.StateFailed:
			parts = append(parts, m.errorText(p.Err))
			it.badge, it.badgeWarn = i18n.TuiDlBadgeFailed, true
		case download.StateCancelled:
			it.badge, it.badgeWarn = i18n.TuiDlBadgeCancelled, true
		}
		it.meta = strings.Join(parts, metaSep)
		items = append(items, it)
	}
	return items
}

// savedRows — по секції на тайтл: «ФРІРЕН · 3 серії · 3,6 ГБ» і рядки серій.
// Кілька файлів однієї серії (720p, докачане потім 1080p) дають один рядок —
// найвищої якості: обирати між ними все одно буде library.Pick у резолві.
func (m *Model) savedRows() []item {
	dir := m.downloadDir()
	if dir == "" {
		return nil
	}
	titles := download.SavedAll(dir)
	sort.Slice(titles, func(i, j int) bool { return m.refName(titles[i].Ref) < m.refName(titles[j].Ref) })
	var items []item
	for i, t := range titles {
		eps := make([]int, 0, len(t.Files))
		var total int64
		for ep, files := range t.Files {
			eps = append(eps, ep)
			total += bestSaved(files).Bytes
		}
		if len(eps) == 0 {
			continue
		}
		sort.Ints(eps)
		if i > 0 {
			items = append(items, item{header: true, spacer: true})
		}
		items = append(items, item{header: true, title: m.refName(t.Ref) + metaSep + i18n.Episodes(len(eps)) + metaSep + i18n.Bytes(total)})
		width := len(strconv.Itoa(eps[len(eps)-1]))
		for _, ep := range eps {
			f := bestSaved(t.Files[ep])
			parts := []string{}
			if f.Height > 0 {
				parts = append(parts, i18n.QualityLabel(f.Height))
			}
			parts = append(parts, i18n.Bytes(f.Bytes))
			if !f.ModTime.IsZero() {
				parts = append(parts, humanDate(f.ModTime, m.now()))
			}
			items = append(items, item{
				icon:    m.ic.Done,
				title:   fmt.Sprintf(i18n.TuiEpisodeNoPad, width, ep),
				meta:    strings.Join(parts, metaSep),
				role:    download.RefKey(t.Ref),
				payload: payloadSavedFile{ref: t.Ref, ep: ep, path: f.Path},
			})
		}
	}
	return items
}

// refName — людська назва тайтлу для рядків і заголовків: бібліотека
// найсвіжіша, посилання (marker або ref завдання) — наступне, slug — остання
// межа, щоб рядок не був порожнім.
func (m *Model) refName(ref provider.TitleRef) string {
	if m.eng != nil && m.eng.Lib != nil {
		if t := m.eng.Lib.TitleByRef(ref); t != nil && t.Name != "" {
			return t.Name
		}
	}
	if ref.Name != "" {
		return ref.Name
	}
	return ref.Slug
}

// openDownloads — вхід на екран «Завантаження»: рядок домівки й клавіша D поза
// екраном серій.
func (m Model) openDownloads() (tea.Model, tea.Cmd) {
	m.beginNav()
	m.stack = append(m.stack, m.snapshot())
	m.showDownloads()
	m.errText = ""
	return m, nil
}

// removeDownload — клавіша X на екрані «Завантаження». Активне скасовується
// (job прибере .part і плейсхолдер), неактивне просто зникає зі знімка.
// Збережені файли не чіпаємо взагалі: істина на диску, і видаляє їх людина
// там, де вони лежать.
func (m Model) removeDownload() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok || it.header || m.dl == nil {
		return m, nil
	}
	p, ok := it.payload.(payloadDownload)
	if !ok {
		return m, nil
	}
	if p.state == download.StateRunning || p.state == download.StateResolving {
		m.dl.Cancel(p.id)
		return m, nil
	}
	m.dl.Forget(p.id)
	m.applyDownloadSnapshot()
	return m, m.setItems(m.downloadRows(), m.list.Index())
}

// retryDownload — Enter на невдалому чи скасованому завданні: та сама серія в
// ту саму чергу. Стара картка лишається до наступного знімка й зникає разом із
// нею — менеджер повертає той самий ключ.
func (m Model) retryDownload(p payloadDownload) (tea.Model, tea.Cmd) {
	if m.dl == nil {
		return m, nil
	}
	if p.state != download.StateFailed && p.state != download.StateCancelled {
		return m, nil
	}
	m.dl.Forget(p.id)
	height := 0
	if prog, ok := m.downloads[dlKey(p.ref, p.ep)]; ok {
		height = prog.Height
	}
	if _, err := m.dl.Enqueue(m.downloadJob(p.ref, p.ep, height)); err != nil {
		m.errText = m.errorText(err)
		return m, nil
	}
	m.applyDownloadSnapshot()
	cmd := m.setItems(m.downloadRows(), m.list.Index())
	m.status = fmt.Sprintf(i18n.TuiDlQueued, p.ep, i18n.Downloads(len(m.downloads)))
	m.statusKind = statusSuccess
	m.statusGen++
	return m, cmd
}

// playSaved — Enter на збереженій серії: звичайний резолв, у якому спрацює
// короткий шлях «файл на диску» (playback.ResolveWith). Жодної окремої гілки
// відтворення: локальний файл проходить тими самими Begin/Run/Finish.
func (m Model) playSaved(p payloadSavedFile) (tea.Model, tea.Cmd) {
	m.beginChain(p.ref)
	req := m.beginNav()
	m.ref = p.ref
	m.pendingEp = p.ep
	m.status = i18n.TuiResolving
	m.statusKind = statusLoading
	m.statusGen++
	return m, m.resolveCmd(p.ref, p.ep, req, m.eng.ResolveHints(p.ref, p.ep))
}

// ---- знімок черги ----

// downloadsActive — чи є в черзі щось, що не завершилося. Від цього залежать
// бейдж домівки, вихід із застосунку й заборона міняти папку.
func (m *Model) downloadsActive() bool {
	if m.dl == nil {
		return false
	}
	for _, p := range m.dl.Snapshot() {
		if !p.State.Terminal() {
			return true
		}
	}
	return false
}

// applyDownloadSnapshot перечитує авторитетний стан менеджера й каже, чи
// змінилося щось видиме. Видиме — це стан завдання або цілий відсоток:
// оновлювати екран на кожні 64 КБ означало б миготіння замість «Тихої черги».
func (m *Model) applyDownloadSnapshot() bool {
	if m.dl == nil {
		return false
	}
	snap := m.dl.Snapshot()
	next := make(map[string]download.Progress, len(snap))
	changed := len(snap) != len(m.downloads)
	for _, p := range snap {
		key := dlKey(p.Ref, p.Episode)
		next[key] = p
		old, had := m.downloads[key]
		if !had || old.State != p.State || downloadPercent(old) != downloadPercent(p) {
			changed = true
		}
		if had && old.State == p.State {
			continue
		}
		switch p.State {
		case download.StateDone:
			// Файл з'явився на диску — кеш папки більше не описує правду.
			m.resetSavedScratch()
			m.status = fmt.Sprintf(i18n.TuiDlSaved, p.Episode, i18n.QualityLabel(p.Height), i18n.Bytes(p.Bytes))
			m.statusKind = statusSuccess
			m.statusGen++
		case download.StateFailed:
			m.status = fmt.Sprintf(i18n.TuiDlFailed, p.Episode, m.errorText(p.Err))
			m.statusKind = statusWarning
			m.statusGen++
		case download.StateCancelled:
			m.status = fmt.Sprintf(i18n.TuiDlCancelled, p.Episode)
			m.statusKind = statusInfo
			m.statusGen++
		}
	}
	m.downloads = next
	return changed
}

// updateDownloads — обробник сигналу менеджера. Рядки перемальовуються лише на
// тих екранах, які показують стан завантаження, і лише коли він справді
// змінився; курсор тримається за ключем рядка, а не за індексом.
func (m Model) updateDownloads(msg downloadMsg) (tea.Model, tea.Cmd) {
	if !msg.ok {
		// Канал закрито (Close): переозброюватись нікуди.
		return m, nil
	}
	rearm := m.downloadEventsCmd()
	if !m.applyDownloadSnapshot() {
		return m, rearm
	}
	key := m.selectedKey()
	var cmd tea.Cmd
	switch m.screen {
	case screenEpisodes:
		cmd = m.setItems(m.episodeRows(), m.list.Index())
		m.selectKey(key, m.list.Index())
	case screenDownloads:
		cmd = m.setItems(m.downloadRows(), m.list.Index())
		m.selectKey(key, m.list.Index())
	case screenHome:
		m.refreshHome()
	}
	return m, tea.Batch(rearm, cmd)
}

// downloadHomeBadge — «1 з 3 · 43%» у рядку домівки. Показується лише поки
// щось качається: у спокої рядок каже саме «Завантаження», без чисел.
func (m *Model) downloadHomeBadge() string {
	if m.dl == nil {
		return ""
	}
	active, done, pct := 0, 0, 0
	for _, p := range m.downloads {
		switch {
		case !p.State.Terminal():
			active++
			if p.State == download.StateRunning {
				pct = downloadPercent(p)
			}
		case p.State == download.StateDone:
			done++
		}
	}
	if active == 0 {
		return ""
	}
	return fmt.Sprintf(i18n.TuiDlHomeBadge, done+1, done+active, pct)
}
