package providertest

import (
	"context"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Stub — provider.Provider, зібраний із полів-функцій: тести домену (playback,
// ui, cmd) потребують керованого провайдера, а не сайту, і раніше кожен пакет
// тримав власну копію тих самих семи методів.
//
// Кожне поле необов'язкове: nil-функція означає «нульове значення і нема
// помилки», тому тест задає лише те, на що справді дивиться.
type Stub struct {
	IDValue   string
	NameValue string
	CapsValue provider.Caps

	SearchFn   func(ctx context.Context, query string, page int) (provider.Page, error)
	CatalogFn  func(ctx context.Context, kind provider.CatalogKind) ([]provider.TitleCard, error)
	EpisodesFn func(ctx context.Context, ref provider.TitleRef) ([]provider.Episode, error)
	SourcesFn  func(ctx context.Context, ref provider.TitleRef, episode int) ([]provider.Source, error)
}

func (s Stub) ID() string   { return s.IDValue }
func (s Stub) Name() string { return s.NameValue }

func (s Stub) Caps() provider.Caps { return s.CapsValue }

func (s Stub) Search(ctx context.Context, query string, page int) (provider.Page, error) {
	if s.SearchFn == nil {
		return provider.Page{}, nil
	}
	return s.SearchFn(ctx, query, page)
}

func (s Stub) Catalog(ctx context.Context, kind provider.CatalogKind) ([]provider.TitleCard, error) {
	if s.CatalogFn == nil {
		return nil, nil
	}
	return s.CatalogFn(ctx, kind)
}

func (s Stub) Episodes(ctx context.Context, ref provider.TitleRef) ([]provider.Episode, error) {
	if s.EpisodesFn == nil {
		return nil, nil
	}
	return s.EpisodesFn(ctx, ref)
}

func (s Stub) Sources(ctx context.Context, ref provider.TitleRef, episode int) ([]provider.Source, error) {
	if s.SourcesFn == nil {
		return nil, nil
	}
	return s.SourcesFn(ctx, ref, episode)
}
