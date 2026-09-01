package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

type candidateProvider struct {
	sources []provider.Source
}

func (candidateProvider) ID() string          { return "stub" }
func (candidateProvider) Name() string        { return "Stub" }
func (candidateProvider) Caps() provider.Caps { return provider.Caps{} }
func (candidateProvider) Search(context.Context, string, int) (provider.Page, error) {
	return provider.Page{}, nil
}
func (candidateProvider) Catalog(context.Context, provider.CatalogKind) ([]provider.TitleCard, error) {
	return nil, nil
}
func (candidateProvider) Episodes(context.Context, provider.TitleRef) ([]provider.Episode, error) {
	return nil, nil
}
func (p candidateProvider) Sources(context.Context, provider.TitleRef, int) ([]provider.Source, error) {
	return p.sources, nil
}

type candidateExtractor struct {
	err error
}

func (candidateExtractor) ID() string          { return "stub" }
func (candidateExtractor) Handles(string) bool { return true }
func (e candidateExtractor) Extract(context.Context, string, string) ([]extractor.Stream, error) {
	return nil, e.err
}

func TestCandidatesPreservesOfflineClassification(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "video.invalid", IsNotFound: true}
	a := &app{
		provider: candidateProvider{sources: []provider.Source{{Embed: "https://video.invalid/embed"}}},
		extractors: []extractor.Extractor{
			candidateExtractor{err: fmt.Errorf("embed: %w", dnsErr)},
		},
	}

	candidates, err := a.candidates(t.Context(), provider.TitleRef{}, 1)
	if len(candidates) != 0 || !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("candidates = %v, error = %v; очікував ErrOffline", candidates, err)
	}
}

func TestCandidatesClassifiesUnsupportedHostAsNoStream(t *testing.T) {
	a := &app{provider: candidateProvider{sources: []provider.Source{{Embed: "https://unsupported.invalid/embed"}}}}

	candidates, err := a.candidates(t.Context(), provider.TitleRef{}, 1)
	if len(candidates) != 0 || !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("candidates = %v, error = %v; очікував ErrNoStream", candidates, err)
	}
}

func TestCommandErrText(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "offline", err: fmt.Errorf("search: %w", errs.ErrOffline), want: i18n.MsgOffline},
		{name: "no stream", err: fmt.Errorf("resolve: %w", errs.ErrNoStream), want: i18n.MsgNoPlayableHost},
		{
			name: "provider",
			err:  fmt.Errorf("episodes: %w", errs.ErrProvider),
			want: fmt.Sprintf(i18n.MsgProviderFailed, fmt.Errorf("episodes: %w", errs.ErrProvider)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandErrText(tt.err); got != tt.want {
				t.Fatalf("commandErrText(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
