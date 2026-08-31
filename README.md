# uaanime

Дивись аніме **українською** просто з термінала: озвучення різних студій і субтитри,
з пам'яттю про те, де ти зупинився і чию озвучку любиш.

```
uaanime → Продовжити → Enter → грає
```

На відміну від ani-cli, uaanime пам'ятає **стан**:

- ▶ **Resume** — вийшов на 14:32, наступний запуск продовжить звідти (±5 с, навіть після `kill -9`);
- 🎙 **Твоя студія** — обрав FanVoxUA один раз, далі всі серії йдуть нею без питань;
- 📚 **Список перегляду й історія** — все локально, офлайн, без акаунтів і телеметрії;
- 🔔 **Оновлення** — бачиш, скільки нових серій вийшло у тайтлів, які дивишся;
- 🔁 **Fallback** — мертвий відеохост підмінюється іншим мовчки.

## Встановлення

**macOS** (mpv підтягнеться сам як залежність):

```bash
brew tap Basmanjacks/uaanime
brew install --cask Basmanjacks/uaanime/uaanime
```

Homebrew 6 не додає сторонні tap автоматично, тому перший рядок обов'язковий.
Хочеш коротке ім'я надалі — виконай `brew trust basmanjacks/uaanime`, і далі
працюватиме просто `brew install --cask uaanime`.

**Linux та решта:** постав [mpv](https://mpv.io) з пакетного менеджера, далі

```bash
go install github.com/Basmanjacks/uaanime/cmd/uaanime@latest
# або візьми готовий бінарник із GitHub Releases
```

`uaanime doctor` перевірить, чи все на місці.

## Використання

```bash
uaanime                # TUI: Продовжити / бібліотека / пошук / історія / оновлення
```

Headless-команди (для скриптів і перевірок):

```bash
uaanime search "фрірен" --json
uaanime episodes <title-id> --json
uaanime resolve <title-id> <серія> --json
uaanime play <title-id> <серія> [--dry-run]
uaanime doctor
uaanime export > backup.json
uaanime import backup.json
```

Дані живуть в одному каталозі (`~/Library/Application Support/uaanime` на macOS) —
`export`/`import` переносять усе.

## Розробка

```bash
make build   # bin/uaanime
make test    # без мережі, фікстури в testdata/
make lint    # gofmt + go vet + golangci-lint
```

`UAANIME_FIXTURES=1` — провайдер читає збережені сторінки замість мережі.
`make record-fixtures` — переписати фікстури з живого сайту (вручну, не в CI).
Як лагодити парсер після зміни сайту — `.claude/skills/provider-repair/SKILL.md`.

## Правові зауваги

uaanime — лише термінальний клієнт до публічно доступних сайтів; він нічого не хостить,
не обходить захисти і не містить контенту. Повага до праці українських студій озвучення:
їхні назви завжди показуються як є.

[MIT](LICENSE)
