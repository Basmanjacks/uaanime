---
name: provider-repair
description: Diagnose and fix a broken uaanime provider or video-host extractor. Use this whenever search returns nothing, an episode list is empty, a title page parses wrong, a stream fails to play, a parser test starts failing, or the user says a site "stopped working", "changed", or "is broken" — even if they do not mention providers or scraping. Also use when adding support for a new Ukrainian anime site or a new video host.
---

# Provider repair

Sites change their HTML without warning. This is the recurring maintenance task in uaanime, and it has
a fixed procedure. Follow it — do not start by editing selectors.

## Rule zero

**Never fix a parser against a live page.** Record a fixture first, fix against the fixture, keep the
fixture as a regression test. Otherwise the next person cannot reproduce your bug and CI cannot protect
your fix.

## 1. Localise the failure

Run the headless commands in order and stop at the first one that misbehaves:

```bash
UAANIME_FIXTURES=1 ./bin/uaanime search "фрірен" --json     # fixtures still pass?
./bin/uaanime search "фрірен" --json                       # live search
./bin/uaanime episodes <title-id> --json                   # title page → episodes + releases
./bin/uaanime resolve <title-id> 1 --json                  # release → stream URL + headers
./bin/uaanime play <title-id> 1 --dry-run                  # final mpv command
```

This tells you the layer:

| Symptom | Layer |
|---|---|
| Fixtures pass, live fails | site changed → `internal/provider` |
| Fixtures also fail | our regression → `git log` the parser |
| Episodes found, releases empty | studio/voice parsing → `internal/provider` |
| Stream URL returned, mpv fails | `internal/extractor` or missing headers |
| Works in browser, 403 for us | headers (`Referer`, `User-Agent`) not propagated |

Do not skip ahead. A missing `Referer` looks exactly like a dead site if you guess.

## 2. Record a fixture

```bash
make record-fixtures                      # all
go run ./tools/record -url <page-url> -name <fixture-name>   # one page
```

Fixtures live in `internal/provider/<name>/testdata/`. Name them by what they cover, not by date:
`search-multiword.html`, `title-multi-studio.html`, `title-single-release.html`, `title-ongoing.html`.

Strip nothing from the HTML except analytics scripts. Trimmed fixtures hide the bug you will have next month.

## 3. Diff before editing

```bash
diff <(cat testdata/title-old.html) <(cat testdata/title.html) | head -50
```

Look for the actual change: class renamed, node nesting changed, player list moved into a JS payload,
data moved from HTML into a JSON blob or an API call. If the data moved to an API, **use the API** —
it is more stable than HTML and usually cheaper.

## 4. Fix

- Prefer stable anchors: `id`, `data-*`, semantic structure, text content.
  Avoid generated class names and positional selectors like `div > div:nth-child(3)`.
- Parse defensively. A missing optional field is `zero value + continue`, never a panic and never an
  aborted page. One malformed episode must not lose the other 23.
- Update the comment above the parser with the structure you now assume and today's date.

## 5. Ukrainian release parsing — the part that is easy to get wrong

The whole product depends on this, so check all of these against the fixture:

- one episode commonly has **several** releases from different studios plus subtitles;
- studio names appear in inconsistent forms (`FanVoxUA`, `FanVox UA`, `ФанВокс`) — normalise for
  comparison, but **store and display the original string**;
- distinguish dub / voice-over / subtitles correctly; when the page is ambiguous, mark it `multi`
  rather than guessing `sub` — guessing `sub` silently violates the never-downgrade rule;
- a release can exist with no working stream. That is not the same as the release not existing.

## 6. Prove it

```bash
go test ./internal/provider/... -run TestParse -v
UAANIME_FIXTURES=1 ./bin/uaanime episodes <title-id> --json
./bin/uaanime play <title-id> 1 --dry-run
```

Add a test case for the shape that broke. Keep the old fixture *and* the new one — they are both real
site states and both must parse.

## 7. When the site is simply gone

Do not patch around it. Make the failure honest:

- `doctor` reports the provider as unavailable with the last successful check time;
- the UI says the site is unavailable and that local data is safe — not an HTTP code;
- watchlist, history and progress must still open.

Then open an issue rather than silently degrading.

## Adding a new site

Implement the `Provider` interface, add fixtures for the four canonical pages (search, multi-studio
title, single-release title, ongoing title), register it, done. If you find yourself changing anything
outside `internal/provider/` to support it, stop — that is the abstraction leaking, and it is a design
bug worth fixing before the new provider ships.
