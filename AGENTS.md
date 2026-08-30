# AGENTS.md

Instructions for AI coding agents working in this repo (Claude Code, Codex, others).
`CLAUDE.md` is a symlink to this file — edit **this** one.

Keep this file short. It is read on every session. Product spec lives in `docs/`.

> **Status:** everything below describes the *starting* stack. It is confirmed or replaced in
> Phase 0 (`docs/adr-001-stack.md`). If the ADR changes the stack, rewrite this file to match
> before writing code — a stale AGENTS.md is worse than none.

---

## What this is

uaanime — a terminal app for watching anime in Ukrainian (dubs and subtitles).
Go, single static binary, external `mpv` for playback, local JSON storage, no backend, no account.

The differentiator is **state**: resume position, remembered voice-over studio, watchlist.
Not "another ani-cli".

---

## Commands

```bash
make build            # go build -o bin/uaanime ./cmd/uaanime
make test             # go test ./... — must pass with no network
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
internal/provider/  site-specific scraping — the ONLY package that knows HTML
internal/extractor/ video host → playable stream + required headers
internal/player/    mpv process + JSON IPC
internal/store/     library.json, config.json, progress journal, atomic writes
internal/library/   domain logic: progress, completion, studio preference resolution
internal/ui/        bubbletea models and views
internal/i18n/      all user-facing strings
docs/               product spec, architecture notes
```

---

## Hard rules

1. **No HTML, CSS selectors, or site URLs outside `internal/provider` and `internal/extractor`.**
   If domain code needs to know which site something came from, the abstraction is wrong.
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
  No panes, tabs, split layouts, mouse, custom scrollbars, ASCII banners, or animations.
- Nerd Font icons are optional and must degrade to plain text.

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
- Don't add settings. The target is ≤ 8. Adding one means removing one.
- Don't write architecture documents instead of code.
