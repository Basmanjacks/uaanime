// Package extractor — контракт відеохоста: embed-посилання → придатний до
// відтворення потік. Коли ламається один хост, страждають усі провайдери одразу,
// тому екстрактори мають найвищий пріоритет за якістю і тестами.
package extractor

import "context"

// Stream — те, що можна віддати плеєру. URL ніколи не кешується: він протухає.
type Stream struct {
	URL     string            `json:"url"`
	Quality int               `json:"quality"` // 1080, 720; 0 = невідомо/авто (master-плейлист)
	Headers map[string]string `json:"headers"` // Referer, User-Agent — доносимо до плеєра
}

type Extractor interface {
	ID() string
	Handles(embed string) bool
	Extract(ctx context.Context, embed, referer string) ([]Stream, error)
}

// Find обирає перший екстрактор, що вміє цей embed.
func Find(list []Extractor, embed string) (Extractor, bool) {
	for _, e := range list {
		if e.Handles(embed) {
			return e, true
		}
	}
	return nil, false
}
