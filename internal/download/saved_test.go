package download

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

var (
	refFrieren = provider.TitleRef{Provider: "anitube", Slug: "4465-frieren", Name: "Фрірен"}
	refOther   = provider.TitleRef{Provider: "anitube", Slug: "9001-other", Name: "Фрірен"}
)

// writeMedia кладе медіафайл заданого розміру і (за потреби) його sidecar.
func writeMedia(t *testing.T, folder, name string, size int, sc *Sidecar) string {
	t.Helper()
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", folder, err)
	}
	path := filepath.Join(folder, name)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("запис %s: %v", path, err)
	}
	if sc != nil {
		if err := WriteSidecar(path, *sc); err != nil {
			t.Fatalf("sidecar %s: %v", path, err)
		}
	}
	return path
}

func mustMarker(t *testing.T, folder string, ref provider.TitleRef) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", folder, err)
	}
	if err := WriteMarker(folder, Marker{Provider: ref.Provider, Slug: ref.Slug, Name: ref.Name}); err != nil {
		t.Fatalf("marker %s: %v", folder, err)
	}
}

func sidecarFor(ref provider.TitleRef, ep int, studio string, kind provider.Kind, h int) *Sidecar {
	return &Sidecar{
		Provider: ref.Provider, Slug: ref.Slug, Name: ref.Name,
		Episode: ep, Studio: studio, Kind: kind, Height: h,
	}
}

func TestIndexMissingDir(t *testing.T) {
	if got := Index(filepath.Join(t.TempDir(), "нема")); len(got) != 0 {
		t.Fatalf("Index неіснуючої папки = %v, очікував порожньо", got)
	}
	if got := Index(""); len(got) != 0 {
		t.Fatalf("Index(\"\") = %v, очікував порожньо", got)
	}
	if got := Saved(filepath.Join(t.TempDir(), "нема"), refFrieren); got != nil {
		t.Fatalf("Saved неіснуючої папки = %v, очікував nil", got)
	}
}

// Аварія паралельних процесів лишає файли одного тайтлу у двох папках —
// показати їх треба як один тайтл, а не як два.
func TestIndexMergesFoldersOfSameRef(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "Фрірен")
	b := filepath.Join(dir, "Frieren")
	mustMarker(t, a, refFrieren)
	mustMarker(t, b, refFrieren)
	writeMedia(t, a, "Фрірен - 05 - FanVoxUA [Озв, 1080p].ts", 10, sidecarFor(refFrieren, 5, "FanVoxUA", provider.KindVoiceover, 1080))
	writeMedia(t, b, "Фрірен - 06 - FanVoxUA [Озв, 720p].ts", 10, sidecarFor(refFrieren, 6, "FanVoxUA", provider.KindVoiceover, 720))

	idx := Index(dir)
	if len(idx) != 1 {
		t.Fatalf("Index дав %d тайтлів, очікував 1: %v", len(idx), idx)
	}
	title := idx[RefKey(refFrieren)]
	if title.Ref.Name != "Фрірен" {
		t.Fatalf("Ref.Name = %q", title.Ref.Name)
	}
	if len(title.Folders) != 2 || !contains(title.Folders, a) || !contains(title.Folders, b) {
		t.Fatalf("Folders = %v, очікував обидві папки", title.Folders)
	}
	if len(title.Files) != 2 || len(title.Files[5]) != 1 || len(title.Files[6]) != 1 {
		t.Fatalf("Files = %v", title.Files)
	}
	f := title.Files[5][0]
	if f.Studio != "FanVoxUA" || f.Kind != provider.KindVoiceover || f.Height != 1080 || f.Bytes != 10 || f.ModTime.IsZero() {
		t.Fatalf("SavedFile = %+v", f)
	}
}

// Файл перенесли руками: sidecar-а нема, ref дає marker, серію й якість — ім'я.
func TestIndexFallbackToName(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Фрірен")
	mustMarker(t, folder, refFrieren)
	writeMedia(t, folder, "Фрірен - 03 - FanVoxUA [Озв, 720p].ts", 10, nil)

	files := Saved(dir, refFrieren)
	if len(files[3]) != 1 {
		t.Fatalf("Files = %v, очікував серію 3", files)
	}
	f := files[3][0]
	if f.Height != 720 {
		t.Fatalf("Height = %d, очікував 720", f.Height)
	}
	if f.Studio != "" || f.Kind != "" {
		// Студію й тип з імені не відновлюємо: у нас нема зворотної мапи
		// «Озв» → студія, а вгадувати — гірше, ніж не знати.
		t.Fatalf("Studio/Kind = %q/%q, очікував порожні", f.Studio, f.Kind)
	}
}

func TestIndexIgnoresNoise(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Фрірен")
	mustMarker(t, folder, refFrieren)
	// Плейсхолдер активного завантаження.
	writeMedia(t, folder, "Фрірен - 01 [Озв, 1080p].ts", 0, nil)
	// Недокачане.
	writeMedia(t, folder, "Фрірен - 02 [Озв, 1080p].ts.part", 10, nil)
	// Приховане службове.
	writeMedia(t, folder, ".Фрірен - 04 [Озв, 1080p].ts", 10, nil)
	// Невпізнане ім'я без sidecar-а.
	writeMedia(t, folder, "readme.txt", 10, nil)
	// Службовий каталог кореня.
	if err := os.MkdirAll(filepath.Join(dir, serviceDirName, locksDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, serviceDirName, locksDirName, "x.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Папка без marker-а і файл без sidecar-а — чужа тека в медіатеці.
	writeMedia(t, filepath.Join(dir, "Чуже"), "Чуже - 01 [Озв, 1080p].ts", 10, nil)

	if got := Index(dir); len(got) != 0 {
		t.Fatalf("Index = %v, очікував порожньо", got)
	}
}

func TestIndexSortsHeightsDesc(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Фрірен")
	mustMarker(t, folder, refFrieren)
	writeMedia(t, folder, "Фрірен - 05 - FanVoxUA [Озв, 720p].ts", 10, sidecarFor(refFrieren, 5, "FanVoxUA", provider.KindVoiceover, 720))
	writeMedia(t, folder, "Фрірен - 05 - FanVoxUA [Озв, 1080p].ts", 10, sidecarFor(refFrieren, 5, "FanVoxUA", provider.KindVoiceover, 1080))

	files := Saved(dir, refFrieren)[5]
	if len(files) != 2 || files[0].Height != 1080 || files[1].Height != 720 {
		t.Fatalf("варіанти = %+v, очікував 1080 перед 720", files)
	}
}

func TestSavedAllSorted(t *testing.T) {
	dir := t.TempDir()
	b := provider.TitleRef{Provider: "anitube", Slug: "1-b", Name: "Б"}
	a := provider.TitleRef{Provider: "anitube", Slug: "2-a", Name: "А"}
	for _, ref := range []provider.TitleRef{b, a} {
		folder := filepath.Join(dir, FolderName(ref.Name))
		mustMarker(t, folder, ref)
		writeMedia(t, folder, Filename(Parts{Title: ref.Name, Episode: 1, Ext: ".ts"}), 10, sidecarFor(ref, 1, "", "", 0))
	}
	all := SavedAll(dir)
	if len(all) != 2 || all[0].Ref.Name != "А" || all[1].Ref.Name != "Б" {
		t.Fatalf("SavedAll = %+v", all)
	}
}

func TestFolderForCreatesAndReuses(t *testing.T) {
	dir := t.TempDir()
	folder, err := FolderFor(dir, refFrieren, "Фрірен")
	if err != nil {
		t.Fatalf("FolderFor: %v", err)
	}
	if want := filepath.Join(dir, "Фрірен"); folder != want {
		t.Fatalf("folder = %q, очікував %q", folder, want)
	}
	m, ok := ReadMarker(folder)
	if !ok || m.Slug != refFrieren.Slug || m.Name != "Фрірен" {
		t.Fatalf("marker = %+v, ok=%v", m, ok)
	}
	// Той самий ref із іншою (або порожньою) назвою — та сама папка: headless
	// `download` дає ref без Name, а тайтл на сайті могли перейменувати.
	again, err := FolderFor(dir, provider.TitleRef{Provider: refFrieren.Provider, Slug: refFrieren.Slug}, "")
	if err != nil {
		t.Fatalf("повторний FolderFor: %v", err)
	}
	if again != folder {
		t.Fatalf("друга папка %q, очікував %q", again, folder)
	}
	third, err := FolderFor(dir, refFrieren, "Frieren: Beyond Journey's End")
	if err != nil {
		t.Fatalf("третій FolderFor: %v", err)
	}
	if third != folder {
		t.Fatalf("третя папка %q, очікував %q", third, folder)
	}
}

// Два різні тайтли з однаковою назвою: другий іде в папку з [id].
func TestFolderForCollision(t *testing.T) {
	dir := t.TempDir()
	first, err := FolderFor(dir, refFrieren, "Фрірен")
	if err != nil {
		t.Fatalf("FolderFor: %v", err)
	}
	second, err := FolderFor(dir, refOther, "Фрірен")
	if err != nil {
		t.Fatalf("FolderFor 2: %v", err)
	}
	if second == first {
		t.Fatalf("обидва тайтли отримали одну папку %q", first)
	}
	if want := filepath.Join(dir, "Фрірен ["+folderID(refOther)+"]"); second != want {
		t.Fatalf("друга папка = %q, очікував %q", second, want)
	}
	if m, ok := ReadMarker(second); !ok || m.Slug != refOther.Slug {
		t.Fatalf("marker другої папки = %+v, ok=%v", m, ok)
	}
}

// Папку створили руками (або старіша версія без marker-а) — усиновлюємо.
func TestFolderForAdoptsUnmarked(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Фрірен")
	writeMedia(t, folder, "Фрірен - 01 [Озв, 1080p].ts", 10, nil)

	got, err := FolderFor(dir, refFrieren, "Фрірен")
	if err != nil {
		t.Fatalf("FolderFor: %v", err)
	}
	if got != folder {
		t.Fatalf("folder = %q, очікував %q", got, folder)
	}
	if m, ok := ReadMarker(folder); !ok || m.Slug != refFrieren.Slug {
		t.Fatalf("marker = %+v, ok=%v", m, ok)
	}
}

// Папка без marker-а, але з sidecar-ом чужого тайтлу — не усиновлюється.
func TestFolderForSkipsForeignSidecars(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Фрірен")
	writeMedia(t, folder, "Фрірен - 01 [Озв, 1080p].ts", 10, sidecarFor(refOther, 1, "", "", 1080))

	got, err := FolderFor(dir, refFrieren, "Фрірен")
	if err != nil {
		t.Fatalf("FolderFor: %v", err)
	}
	if got == folder {
		t.Fatalf("забрали чужу папку %q", folder)
	}
	if m, ok := ReadMarker(folder); ok {
		t.Fatalf("чужій папці дописали marker: %+v", m)
	}
}

// Два різні ref з однаковою FolderName одночасно: folders.lock має розвести їх
// по різних папках. Кожен виклик store.Lock відкриває власний дескриптор, тож
// flock серіалізує навіть горутини одного процесу (він per open-file-description,
// а не per-process).
func TestFolderForConcurrent(t *testing.T) {
	dir := t.TempDir()
	refs := []provider.TitleRef{refFrieren, refOther}
	got := make([]string, len(refs))
	errsOut := make([]error, len(refs))

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, ref := range refs {
		wg.Add(1)
		go func(i int, ref provider.TitleRef) {
			defer wg.Done()
			<-start
			got[i], errsOut[i] = FolderFor(dir, ref, "Фрірен")
		}(i, ref)
	}
	close(start)
	wg.Wait()

	for i, err := range errsOut {
		if err != nil {
			t.Fatalf("FolderFor(%s): %v", refs[i].Slug, err)
		}
	}
	if got[0] == got[1] {
		t.Fatalf("обидва тайтли зайняли одну папку %q", got[0])
	}
	for i, ref := range refs {
		m, ok := ReadMarker(got[i])
		if !ok || m.Provider != ref.Provider || m.Slug != ref.Slug {
			t.Fatalf("marker %q = %+v, ok=%v; очікував %s", got[i], m, ok, ref.Slug)
		}
	}
}

func TestLockPaths(t *testing.T) {
	dir := "/downloads"
	if want := filepath.Join(dir, ".uaanime", "locks"); LocksDir(dir) != want {
		t.Fatalf("LocksDir = %q", LocksDir(dir))
	}
	if want := filepath.Join(LocksDir(dir), "folders.lock"); FoldersLockPath(dir) != want {
		t.Fatalf("FoldersLockPath = %q", FoldersLockPath(dir))
	}
	// Лок стабільний за provider+slug і не залежить від назви.
	a := RefLockPath(dir, refFrieren)
	b := RefLockPath(dir, provider.TitleRef{Provider: refFrieren.Provider, Slug: refFrieren.Slug})
	if a != b {
		t.Fatalf("лок залежить від Name: %q != %q", a, b)
	}
	if a == RefLockPath(dir, refOther) {
		t.Fatalf("різні тайтли ділять один лок: %q", a)
	}
	if filepath.Dir(a) != LocksDir(dir) {
		t.Fatalf("лок поза службовою папкою: %q", a)
	}
}

func TestSidecarPath(t *testing.T) {
	got := sidecarPath(filepath.Join("/d", "Фрірен", "Фрірен - 05 [Озв, 1080p].ts"))
	want := filepath.Join("/d", "Фрірен", ".Фрірен - 05 [Озв, 1080p].ts.json")
	if got != want {
		t.Fatalf("sidecarPath = %q, очікував %q", got, want)
	}
}
