# AGENTS.md

Instructions for AI coding agents working in this repo (Claude Code, Codex, others).
`CLAUDE.md` is a symlink to this file — edit **this** one.

Keep this file short. It is read on every session. Product spec lives in `docs/`.

> **Status:** the stack is decided and closed — see `docs/adr-001-stack.md` (2026-08-31).
> Go **1.25+** (dictated by Charm v2 go directives), layout `internal/` only, no `pkg/anisrc`.
> Do not reopen stack questions.

---

## What this is

uaanime — a terminal app for watching anime in Ukrainian (dubs and subtitles).
Go, single static binary, зовнішній плеєр (VLC за замовчуванням або mpv), local JSON storage, no backend, no account.

The differentiator is **state**: resume position, remembered voice-over studio, watchlist.
Not "another ani-cli".

---

## Commands

```bash
make build            # go build -o bin/uaanime ./cmd/uaanime
make test             # go test -race ./... — must pass with no network
make lint             # gofmt -l . && go vet ./... && golangci-lint run
make run              # build + run TUI
make record-fixtures  # refresh testdata/ from the live site (manual, never in CI)
```

Headless commands — use these to verify your own work, the TUI needs a TTY:

```bash
./bin/uaanime search "фрірен" --json
./bin/uaanime episodes <title-id> --json
./bin/uaanime resolve <title-id> <ep> --json
./bin/uaanime play <title-id> <ep> --dry-run
./bin/uaanime doctor --json
```

`UAANIME_FIXTURES=1` makes the provider read `testdata/` instead of the network.

---

## Layout

```
cmd/uaanime/          entrypoint, flag parsing, headless commands
internal/provider/    site-specific scraping — the ONLY package that knows HTML
internal/extractor/   video host → playable stream + required headers
internal/providertest/ shared contract tests every provider must pass
internal/extractortest/ the same for extractors — every video host passes it
internal/playertest/   controllable fake player + session for end-to-end tests (cmd, ui)
internal/playback/    orchestration: preference pick → extract → player session → journal
internal/player/      зовнішні плеєри: VLC (RC/TCP) і mpv (JSON IPC)
internal/store/       library.json, config.json, progress journal, metadata cache, atomic writes
internal/library/     domain logic: progress, completion, studio preference resolution
internal/ui/          bubbletea models and views
internal/remote/      web remote for the phone: OUR OWN embedded page, not scraping
internal/i18n/        all user-facing strings
internal/httpx/       shared HTTP client identity + fixture transport plumbing
internal/qr/          QR encoder for the remote address (stdlib, byte mode, ECC L, v1–6)
tools/record/         manual fixture recorder (never in CI)
docs/               product spec, architecture notes
```

---

## Hard rules

1. **No HTML, CSS selectors, or site URLs outside `internal/provider` and `internal/extractor`.**
   If domain code needs to know which site something came from, the abstraction is wrong.
   (`internal/remote` renders its own `html/template` page — that is serving, not parsing.)
2. **No network in unit tests.** Fixtures only. A test that fails when a site is down is a broken test.
3. **No panic reaches the user.** Recover at the root; restore the terminal via `defer` on every exit path.
   A terminal left in raw mode after a crash is a P0 bug.
4. **Never silently switch a user from Ukrainian dub to subtitles** when another Ukrainian dub exists.
   See `internal/library/preference.go` — that resolution order is product behaviour, not an implementation detail.
5. **Stream URLs are never cached.** They expire. Metadata is cached with a TTL.
6. **Errors are distinguished**: offline / provider failed / no stream exists. Three different messages.
   Raw HTTP codes and stack traces only under `--debug`.
7. **No new dependencies** without a note in the PR description explaining why the stdlib is not enough.
8. **No code that bypasses authentication, paywalls, DRM or CAPTCHA.** Treat all remote data as untrusted
   and never execute it.
9. **UI strings go through `internal/i18n`.** No literal Ukrainian text in `internal/ui`.
   Studio and provider names are never translated.
10. **`library.Library` is not goroutine-safe:** Engine methods marked `sync:` in `internal/playback`
    run only on the TUI Update goroutine or sequentially in the CLI; `tea.Cmd` closures never touch `Lib`.

---

## TUI: Charm v2 only

Import paths are `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`.
**Not** `github.com/charmbracelet/...` — that is v1 and most training data still shows it.
Check the v2 upgrade guide before writing UI code.

- Colour profile detection and ANSI downsampling are built in and on by default. Do not roll your own.
- `AdaptiveColor` no longer exists. Pick light/dark explicitly via `compat.HasDarkBackground`.
  A light terminal is not an edge case.
- All colours and spacing live in `internal/ui/theme.go`. No hardcoded colours anywhere else.
- One list component, reused. If a screen does not fit "title + list + one hint line", it does not get built.
  No panes, tabs, split layouts, mouse, custom scrollbars, or animations. The only sanctioned ASCII art is
  the home-screen brand banner in `internal/ui/brand.go`, with a mandatory one-line fallback on small terminals.
- Nerd Font icons are optional and must degrade to plain text.
- Settings are edited on the settings screen (`internal/ui/screen_settings.go`), never by hand-holding the
  user into `config.json`. Engine fields that background commands read (`Prefs`) are snapshotted into
  `playback.Hints`; the settings screen opens only when nothing is pending or playing (see rule 10).
- The list delegate is custom (`internal/ui/delegate.go`): fixed row height, fixed-width icon column.
  Do not switch back to `list.NewDefaultDelegate()` — it is hardcoded dark and pink.
- Always call `DisableQuitKeybindings()` on a new list: bubbles v2.2.1 binds list Quit to the key `v`
  (upstream bug), which would quit the app from any screen that forwards keys to the list.

## Style

- `gofmt` is the formatter; no debate.
- Errors wrapped with `%w` and context; sentinel errors in `internal/errs`.
- No `interface{}`/`any` in domain types.
- One abstraction per real reason. If an interface has one implementation and does not make a test simpler, delete it.
- Comments explain *why*, not *what*. Site-specific parsing gets a comment with the page structure it assumes
  and the date it was last verified.

---

## When a provider breaks

This is the recurring maintenance task. Follow `.claude/skills/provider-repair/SKILL.md`.
Short version: record a fresh fixture, fix the parser against it, keep the old fixture as a regression test.

---

## Don't

- Don't add features from `docs/future.md` unless asked.
- Don't turn this into a library manager, downloader, or tracker sync client.
- Don't add settings. The cap is 8 (`docs/build-brief.md`); adding a ninth means removing one.
- Don't write architecture documents instead of code.
