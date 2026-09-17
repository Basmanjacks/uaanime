package download

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// Ідентичність збереженого на диску тримається на двох прихованих файлах поруч
// із медіа, а не на іменах папок:
//
//   - sidecar «.<ім'я файла>.json» — усе про конкретний файл. Пишеться один раз,
//     своїм процесом, атомарно й ДО публікації медіа: спільного файла-індексу
//     немає, тож два процеси, що качають різні серії одного тайтлу, не роблять
//     read-modify-write над одним і тим самим станом.
//   - marker «.uaanime-title.json» — чий це каталог. Без нього перейменування
//     тайтлу у провайдера створило б другу папку поруч зі старою.
//
// Ім'я папки лишається людським, але пошук іде за provider+slug: headless
// `download` дістає ref без Name, а назва тайтлу на сайті змінюється.
const (
	serviceDirName  = ".uaanime"
	locksDirName    = "locks"
	markerName      = ".uaanime-title.json"
	foldersLockName = "folders.lock"
	// partSuffix — незавершене завантаження. В індекс не потрапляє ніколи.
	partSuffix = ".part"
)

// Sidecar — паспорт одного медіафайла.
type Sidecar struct {
	Provider string        `json:"provider"`
	Slug     string        `json:"slug"`
	Name     string        `json:"name,omitempty"`
	Episode  int           `json:"episode"`
	Studio   string        `json:"studio,omitempty"`
	Kind     provider.Kind `json:"kind,omitempty"`
	Height   int           `json:"height,omitempty"`
}

// Marker — належність папки тайтлу.
type Marker struct {
	Provider string `json:"provider"`
	Slug     string `json:"slug"`
	Name     string `json:"name,omitempty"`
}

// SavedFile — один збережений варіант серії на диску.
type SavedFile struct {
	Path    string
	Studio  string
	Kind    provider.Kind
	Height  int
	Bytes   int64
	ModTime time.Time
}

// SavedTitle — усе збережене для одного тайтлу. Folders може бути кілька:
// після аварії паралельних процесів файли одного ref трапляються у двох
// папках, і показати їх треба разом, а не як два різні тайтли.
type SavedTitle struct {
	Ref     provider.TitleRef
	Folders []string
	Files   map[int][]SavedFile
}

// RefKey — ключ ідентичності тайтлу. Name свідомо не входить: він змінюється.
func RefKey(ref provider.TitleRef) string { return ref.Provider + ":" + ref.Slug }

// LocksDir — службовий каталог із міжпроцесними локами. Лежить у корені папки
// завантажень, а не в папці тайтлу: локи потрібні ще ДО того, як папка тайтлу
// вибрана, і сам вибір папки теж під локом.
func LocksDir(dir string) string { return filepath.Join(dir, serviceDirName, locksDirName) }

// RefLockPath — лок власності на тайтл. Стабільний за provider+slug, а не за
// назвою папки: у headless-режимі Name порожній, у TUI — людський, а лок має
// збігтися. Файли лока ніколи не видаляються (див. store.Lock).
func RefLockPath(dir string, ref provider.TitleRef) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(RefKey(ref)))
	return filepath.Join(LocksDir(dir), fmt.Sprintf("%016x.lock", h.Sum64()))
}

// FoldersLockPath — короткий глобальний лок на розподіл папок, щоб два різні
// ref з однаковою FolderName не зайняли одну папку одночасно.
func FoldersLockPath(dir string) string { return filepath.Join(LocksDir(dir), foldersLockName) }

// sidecarPath — «.<ім'я файла>.json» поруч із медіа. Крапка ховає його від
// Finder і від нашого ж обходу каталогу.
func sidecarPath(mediaPath string) string {
	return filepath.Join(filepath.Dir(mediaPath), "."+filepath.Base(mediaPath)+".json")
}

// WriteSidecar пише паспорт файла атомарно — його читає інший процес, і
// напівзаписаний JSON виглядав би як «файл без sidecar».
func WriteSidecar(mediaPath string, s Sidecar) error {
	return store.WriteAtomic(sidecarPath(mediaPath), s)
}

// WriteMarker закріплює папку за тайтлом.
func WriteMarker(folder string, m Marker) error {
	return store.WriteAtomic(filepath.Join(folder, markerName), m)
}

// ReadMarker повертає false і на відсутній, і на битий marker: обидва випадки
// означають «папка нічия», і поводитися з ними треба однаково.
func ReadMarker(folder string) (Marker, bool) {
	var m Marker
	data, err := os.ReadFile(filepath.Join(folder, markerName))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return Marker{}, false
	}
	if m.Provider == "" || m.Slug == "" {
		return Marker{}, false
	}
	return m, true
}

func readSidecar(mediaPath string) (Sidecar, bool) {
	var s Sidecar
	data, err := os.ReadFile(sidecarPath(mediaPath))
	if err != nil || json.Unmarshal(data, &s) != nil {
		return Sidecar{}, false
	}
	if s.Provider == "" || s.Slug == "" {
		return Sidecar{}, false
	}
	return s, true
}

// Index читає весь стан завантажень з диска: один ReadDir кореня і по одному
// на папку тайтлу. Глибше не спускаємося — підпапки всередині папки тайтлу
// створює не застосунок, і лізти в чужу структуру не наша справа.
//
// Відсутня папка — не помилка: людина ще нічого не завантажувала.
func Index(dir string) map[string]SavedTitle {
	out := make(map[string]SavedTitle)
	if dir == "" {
		return out
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	acc := make(map[string]*titleAcc)
	for _, e := range ents {
		// Приховані каталоги пропускаємо всі, не лише .uaanime: свої папки
		// тайтлів ми ніколи не ховаємо, а .Trash чи .Spotlight-V100 сканувати
		// марно й повільно.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		scanFolder(filepath.Join(dir, e.Name()), acc)
	}
	for key, a := range acc {
		out[key] = SavedTitle{
			Ref:     a.ref,
			Folders: append(append([]string{}, a.marked...), a.plain...),
			Files:   a.sortedFiles(),
		}
	}
	return out
}

type titleAcc struct {
	ref provider.TitleRef
	// marked — папки з marker-ом цього ref, plain — ті, що дали лише файли з
	// sidecar-ами. Перша в списку має бути «рідна», бо саме туди пишуть далі.
	marked []string
	plain  []string
	files  map[int][]SavedFile
}

func (a *titleAcc) add(folder string, marked bool, ep int, f SavedFile) {
	list := &a.plain
	if marked {
		list = &a.marked
	}
	if !contains(a.marked, folder) && !contains(a.plain, folder) {
		*list = append(*list, folder)
	}
	if a.files == nil {
		a.files = make(map[int][]SavedFile)
	}
	a.files[ep] = append(a.files[ep], f)
}

// sortedFiles: у межах серії вищa якість перша — саме її бере відтворення за
// замовчуванням; за однакової висоти порядок за шляхом, щоб знімок був
// стабільним між викликами.
func (a *titleAcc) sortedFiles() map[int][]SavedFile {
	for ep := range a.files {
		list := a.files[ep]
		sort.Slice(list, func(i, j int) bool {
			if list[i].Height != list[j].Height {
				return list[i].Height > list[j].Height
			}
			return list[i].Path < list[j].Path
		})
	}
	return a.files
}

func scanFolder(folder string, acc map[string]*titleAcc) {
	marker, hasMarker := ReadMarker(folder)
	ents, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		// Приховане — це наші sidecar-и й marker; .part — недокачане;
		// каталоги всередині папки тайтлу нас не цікавлять.
		if e.IsDir() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, partSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			// Розмір 0 — резервація під активне завантаження, а не файл.
			continue
		}
		path := filepath.Join(folder, name)
		var (
			ref provider.TitleRef
			ep  int
			f   = SavedFile{Path: path, Bytes: info.Size(), ModTime: info.ModTime()}
		)
		switch sc, ok := readSidecar(path); {
		case ok:
			ref = provider.TitleRef{Provider: sc.Provider, Slug: sc.Slug, Name: sc.Name}
			ep = sc.Episode
			f.Studio, f.Kind, f.Height = sc.Studio, sc.Kind, sc.Height
		case hasMarker:
			// Файл перенесли руками або sidecar загубився: ref дає marker,
			// серію й якість — ім'я, студія й тип лишаються невідомими.
			e2, h, ok2 := parseSavedName(name)
			if !ok2 {
				continue
			}
			ref = provider.TitleRef{Provider: marker.Provider, Slug: marker.Slug, Name: marker.Name}
			ep, f.Height = e2, h
		default:
			// Ні sidecar, ні marker — чужий файл у папці завантажень.
			continue
		}
		key := RefKey(ref)
		a := acc[key]
		if a == nil {
			a = &titleAcc{ref: ref}
			acc[key] = a
		}
		if a.ref.Name == "" && ref.Name != "" {
			a.ref.Name = ref.Name
		}
		markedHere := hasMarker && marker.Provider == ref.Provider && marker.Slug == ref.Slug
		a.add(folder, markedHere, ep, f)
	}
}

// Saved — локальні варіанти серій одного тайтлу: серія → усі якості й релізи.
// Вибір конкретного файла робить library.Pick, як і для мережевих джерел.
func Saved(dir string, ref provider.TitleRef) map[int][]SavedFile {
	return Index(dir)[RefKey(ref)].Files
}

// SavedAll — для екрана «Завантаження»: тайтли за алфавітом, безіменні (лише
// slug у sidecar) — у кінці, але в стабільному порядку.
func SavedAll(dir string) []SavedTitle {
	idx := Index(dir)
	out := make([]SavedTitle, 0, len(idx))
	for _, t := range idx {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref.Name != out[j].Ref.Name {
			return out[i].Ref.Name < out[j].Ref.Name
		}
		return RefKey(out[i].Ref) < RefKey(out[j].Ref)
	})
	return out
}

// folderWaitStep і folderWaitTotal — скільки чекати на folders.lock. Лок
// тримається на час кількох ReadDir і одного MkdirAll, тобто мілісекунди;
// store.Lock неблокуючий, тому чекаємо ми, а не ядро. Відразу віддавати
// ErrDownloadBusy не можна: це повідомлення про чуже завантаження ЦЬОГО
// тайтлу, а тут конфлікт лише за розподіл папок і він миттєвий.
const (
	folderWaitStep  = 5 * time.Millisecond
	folderWaitTotal = 2 * time.Second
)

// FolderFor вибирає (і за потреби створює) папку тайтлу, сам беручи
// folders.lock: вибір і створення мають бути однією неподільною дією, інакше
// два процеси з однаковою FolderName для різних тайтлів зайняли б одну папку.
//
// Правила по порядку: (1) папка з marker-ом цього ref — де б вона не була;
// (2) FolderName(name), якщо вільна або нічия й без чужих sidecar-ів;
// (3) FolderName(name) + « [id]» за тим самим правилом.
func FolderFor(dir string, ref provider.TitleRef, name string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("папка завантажень не задана: %w", errs.ErrNoWriteAccess)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", writeErr(dir, err)
	}
	lock, err := lockFolders(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()

	if folder, ok := folderByMarker(dir, ref); ok {
		return folder, nil
	}
	if name == "" {
		name = ref.Name
	}
	base := FolderName(name)
	folder, ok, err := claimFolder(dir, base, ref)
	if err != nil || ok {
		return folder, err
	}
	folder, ok, err = claimFolder(dir, base+" ["+folderID(ref)+"]", ref)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%s: не вдалося виділити папку: %w", base, errs.ErrProvider)
	}
	return folder, nil
}

func lockFolders(dir string) (io.Closer, error) {
	deadline := time.Now().Add(folderWaitTotal)
	for {
		lock, err := store.Lock(FoldersLockPath(dir), errs.ErrDownloadBusy)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, errs.ErrDownloadBusy) {
			return nil, writeErr(FoldersLockPath(dir), err)
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(folderWaitStep)
	}
}

// folderByMarker шукає вже закріплену папку. Перша за алфавітом виграє: якщо
// аварія лишила дві папки з одним marker-ом, вибір має бути однаковим у всіх
// процесів, а не «як ReadDir поверне».
func folderByMarker(dir string, ref provider.TitleRef) (string, bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		folder := filepath.Join(dir, e.Name())
		if m, ok := ReadMarker(folder); ok && m.Provider == ref.Provider && m.Slug == ref.Slug {
			return folder, true
		}
	}
	return "", false
}

// claimFolder: ok == false означає «зайнято іншим тайтлом, спробуй наступне
// ім'я»; помилка — що писати нікуди й наступне ім'я не допоможе.
func claimFolder(dir, base string, ref provider.TitleRef) (string, bool, error) {
	folder := filepath.Join(dir, base)
	info, err := os.Stat(folder)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return "", false, writeErr(folder, err)
		}
	case err != nil:
		return "", false, writeErr(folder, err)
	case !info.IsDir():
		// На місці папки лежить файл користувача — не наша територія.
		return "", false, nil
	default:
		if m, ok := ReadMarker(folder); ok {
			return folder, m.Provider == ref.Provider && m.Slug == ref.Slug, nil
		}
		// Папка без marker-а: усиновлюємо, якщо в ній немає файлів чужого
		// тайтлу. Так підхоплюються теки, створені старішою версією або
		// руками, і не забирається чужа.
		if ownedByOther(folder, ref) {
			return "", false, nil
		}
	}
	if err := WriteMarker(folder, Marker{Provider: ref.Provider, Slug: ref.Slug, Name: ref.Name}); err != nil {
		return "", false, writeErr(folder, err)
	}
	return folder, true, nil
}

// ownedByOther — чи є в папці sidecar іншого тайтлу.
func ownedByOther(folder string, ref provider.TitleRef) bool {
	ents, err := os.ReadDir(folder)
	if err != nil {
		return true // не прочитали — вважаємо чужою, це безпечніший бік
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") || name == markerName {
			continue
		}
		var s Sidecar
		data, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil || json.Unmarshal(data, &s) != nil || s.Slug == "" {
			continue
		}
		if s.Provider != ref.Provider || s.Slug != ref.Slug {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// writeErr зводить відмови ФС до одного класу: людині треба або дати права,
// або вибрати іншу папку — дія однакова, текст теж.
func writeErr(path string, err error) error {
	return fmt.Errorf("%s: %w: %w", path, err, errs.ErrNoWriteAccess)
}
