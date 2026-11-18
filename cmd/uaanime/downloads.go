package main

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/basmanjacks/uaanime/internal/download"
	"github.com/basmanjacks/uaanime/internal/i18n"
	"github.com/basmanjacks/uaanime/internal/provider"
)

// downloadsTitle — один тайтл у виводі `downloads --json`. Поля завжди
// присутні (без omitempty): скрипт читає стабільну форму, а невідоме значення
// — це порожній рядок чи 0, а не відсутній ключ.
type downloadsTitle struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Folders  []string           `json:"folders"`
	Episodes []downloadsEpisode `json:"episodes"`
}

type downloadsEpisode struct {
	Number int             `json:"number"`
	Files  []downloadsFile `json:"files"`
}

type downloadsFile struct {
	Path   string        `json:"path"`
	Studio string        `json:"studio"`
	Kind   provider.Kind `json:"kind"`
	Height int           `json:"height"`
	Bytes  int64         `json:"bytes"`
	// Modified — час зміни файла, RFC 3339 в UTC.
	Modified string `json:"modified"`
	// Known — false для файла без достовірного sidecar-а: серія взята з імені,
	// студія й тип невідомі.
	Known bool `json:"known"`
}

// savedListing перетворює знімок download.SavedAll на стабільний вивід:
// тайтли — у порядку SavedAll (за назвою, далі за id), серії — за номером,
// файли — за шляхом. Порядок файлів у SavedAll інший (найвища якість перша,
// бо так їх бере відтворення); скриптам потрібен порядок, що не залежить від
// якості, тож тут сортуємо наново.
func savedListing(dir string) []downloadsTitle {
	titles := download.SavedAll(dir)
	out := make([]downloadsTitle, 0, len(titles))
	for _, t := range titles {
		dt := downloadsTitle{
			ID:       titleID(t.Ref),
			Name:     t.Ref.Name,
			Folders:  append([]string{}, t.Folders...),
			Episodes: make([]downloadsEpisode, 0, len(t.Files)),
		}
		for num, files := range t.Files {
			de := downloadsEpisode{Number: num, Files: make([]downloadsFile, 0, len(files))}
			for _, f := range files {
				de.Files = append(de.Files, downloadsFile{
					Path:     f.Path,
					Studio:   f.Studio,
					Kind:     f.Kind,
					Height:   f.Height,
					Bytes:    f.Bytes,
					Modified: f.ModTime.UTC().Format(time.RFC3339),
					Known:    f.Known,
				})
			}
			sort.Slice(de.Files, func(i, j int) bool { return de.Files[i].Path < de.Files[j].Path })
			dt.Episodes = append(dt.Episodes, de)
		}
		sort.Slice(dt.Episodes, func(i, j int) bool { return dt.Episodes[i].Number < dt.Episodes[j].Number })
		out = append(out, dt)
	}
	return out
}

// cmdDownloads — що лежить у папці завантажень. Лише читає диск: ні мережі,
// ні каталогу даних, ні локів завантажень. Відсутня папка — порожній список,
// а не помилка: людина ще нічого не завантажувала.
func (a *app) cmdDownloads(opt options) int {
	dir, ok := downloadTargetDir(opt.dir, a.cfg.DownloadDir)
	if !ok {
		return usageError(a.json)
	}
	listing := savedListing(dir)
	if opt.json {
		return printJSON(listing)
	}
	if len(listing) == 0 {
		outln(i18n.MsgDownloadsEmpty)
		return 0
	}
	for _, t := range listing {
		for _, ep := range t.Episodes {
			for _, f := range ep.Files {
				outf("%s\t%d\t%s\t%s\t%s\n", t.ID, ep.Number, savedRelease(f), savedQuality(f.Height), f.Path)
			}
		}
	}
	return 0
}

// savedRelease — «Студія (тип)» або позначка невідомого перекладу для файла
// без sidecar-а.
func savedRelease(f downloadsFile) string {
	if !f.Known {
		return i18n.MsgDownloadsUnknownRelease
	}
	return fmt.Sprintf("%s (%s)", i18n.StudioName(f.Studio), f.Kind)
}

// savedQuality — «1080p»; невідома якість — порожня колонка, щоб рядок
// лишався розбірним по табуляціях.
func savedQuality(h int) string {
	if h <= 0 {
		return ""
	}
	return strconv.Itoa(h) + "p"
}
