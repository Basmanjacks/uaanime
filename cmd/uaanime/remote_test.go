package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/remote"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// remoteEnv підміняє слухача пульта на loopback: у тестах нема чого будити
// діалог фаєрвола macOS, а пісочниця може забороняти навіть 127.0.0.1 —
// тоді сценарій пропускається, як і в internal/player.
func remoteEnv(t *testing.T) *listenLog {
	t.Helper()
	if ln, err := net.Listen("tcp", "127.0.0.1:0"); err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("пісочниця забороняє loopback: %v", err)
		}
		t.Fatalf("Listen: %v", err)
	} else {
		_ = ln.Close()
	}
	log := &listenLog{}
	saved := remoteListen
	remoteListen = log.listen
	t.Cleanup(func() { remoteListen = saved })
	return log
}

type listenLog struct {
	mu    sync.Mutex
	ports []int
}

func (l *listenLog) listen(port int) (net.Listener, bool, error) {
	ephemeral := false
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil && port > 0 {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		ephemeral = true
	}
	if err != nil {
		return nil, false, err
	}
	l.mu.Lock()
	l.ports = append(l.ports, remote.Port(ln))
	l.mu.Unlock()
	return ln, ephemeral, nil
}

func (l *listenLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.ports)
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// remoteIdentity чекає, поки застосунок збереже порт і токен пульта.
func remoteIdentity(t *testing.T, dir string) store.RemoteIdentity {
	t.Helper()
	path := filepath.Join(dir, "state", "remote.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			var id store.RemoteIdentity
			if json.Unmarshal(b, &id) == nil && id.Port > 0 {
				return id
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("remote.json так і не з'явився")
	return store.RemoteIdentity{}
}

func remoteBase(id store.RemoteIdentity) string {
	return "http://127.0.0.1:" + strconv.Itoa(id.Port) + "/r/" + id.Token + "/"
}

// remoteCommand грає роль телефона: дочікується, поки статус скаже «грає» і
// Run запише перший журнал (пульт закриває сесію без додаткового семплу, тож
// збережена позиція залежить від тіка), і надсилає команду.
func remoteCommand(dir, base, cmd string) (remote.Status, int, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	journal := filepath.Join(dir, "state", "current.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(journal); err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		resp, err := client.Get(base + "status")
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var st remote.Status
		err = json.NewDecoder(resp.Body).Decode(&st)
		_ = resp.Body.Close()
		if err == nil && st.Playing {
			resp, err := client.Post(base+cmd, "", nil)
			if err != nil {
				return remote.Status{}, 0, err
			}
			defer func() { _ = resp.Body.Close() }()
			var after remote.Status
			if err := json.NewDecoder(resp.Body).Decode(&after); err != nil {
				return remote.Status{}, resp.StatusCode, err
			}
			return after, resp.StatusCode, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return remote.Status{}, 0, errors.New("сесія так і не заграла")
}

// Пульт просить наступну серію при autoplay: never — намір сильніший за
// налаштування; серія 1 зберігається, але не завершується.
func TestJourneyRemoteNextStartsNextEpisode(t *testing.T) {
	remoteEnv(t)
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	dir, _, fp := journeyEnv(t, held, playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}))
	writeConfig(t, dir, `{"autoplay":"never","remote":"on"}`)

	type reply struct {
		st   remote.Status
		code int
		err  error
	}
	done := make(chan reply, 1)
	go func() {
		st, code, err := remoteCommand(dir, remoteBase(remoteIdentity(t, dir)), "next")
		done <- reply{st, code, err}
	}()

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	r := <-done
	if r.err != nil {
		t.Fatalf("пульт: %v", r.err)
	}
	// справжній ланцюжок handler → перехідник → Live: після next статус уже
	// «нічого не грає», а не 500 на закритій сесії
	if r.code != http.StatusOK || r.st.Playing {
		t.Fatalf("POST next = %d %+v, want 200 і Playing=false", r.code, r.st)
	}
	mustContain(t, "stdout", out, strings.SplitN(i18n.MsgRemoteURL, "%", 2)[0])
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgResolving, 2))

	starts := fp.Starts()
	if len(starts) != 2 || !strings.HasSuffix(starts[1].MediaTitle, " · 2") {
		t.Fatalf("запуски плеєра = %+v, want дві серії", starts)
	}
	lib := loadLibrary(t, dir)
	title := lib.TitleByRef(fixtureRef(t))
	if p := lib.ProgressFor(title.ID, 1); p == nil || p.Completed || p.PositionSec != 40 {
		t.Errorf("серія 1 = %+v, want 40 с без Completed", p)
	}
}

// «Зупинити» з пульта уриває ланцюжок навіть при autoplay: always.
func TestJourneyRemoteStopEndsPlayback(t *testing.T) {
	remoteEnv(t)
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	dir, _, fp := journeyEnv(t, held, playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440}))
	writeConfig(t, dir, `{"autoplay":"always","remote":"on"}`)

	done := make(chan error, 1)
	go func() {
		_, code, err := remoteCommand(dir, remoteBase(remoteIdentity(t, dir)), "stop")
		if err == nil && code != http.StatusOK {
			err = fmt.Errorf("POST stop = %d", code)
		}
		done <- err
	}()

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	if err := <-done; err != nil {
		t.Fatalf("пульт: %v", err)
	}
	if n := len(fp.Starts()); n != 1 {
		t.Fatalf("запусків плеєра = %d, want 1 (stop має зупинити автоплей)", n)
	}
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgProgressSaved, 0, 40))
}

func TestRemoteDisabledDoesNotListen(t *testing.T) {
	log := remoteEnv(t)
	dir, _, _ := journeyEnv(t, playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}))
	writeConfig(t, dir, `{"remote":"off"}`)

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	if log.count() != 0 {
		t.Fatalf("пульт вимкнено, а слухач відкривався %d разів", log.count())
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "remote.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote.json не має існувати: %v", err)
	}
	if strings.Contains(out, strings.SplitN(i18n.MsgRemoteURL, "%", 2)[0]) {
		t.Fatalf("адреса пульта надрукована попри remote: off:\n%s", out)
	}
}

// Закладка на телефоні живе вічно: другий запуск слухає той самий порт із
// тим самим токеном, а файл не змінюється.
func TestRemoteIdentityStickyAcrossRuns(t *testing.T) {
	log := remoteEnv(t)
	dir, _, _ := journeyEnv(t,
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	path := filepath.Join(dir, "state", "remote.json")

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("remote.json після першого запуску: %v", err)
	}
	var id store.RemoteIdentity
	if err := json.Unmarshal(first, &id); err != nil || id.Port == 0 || len(id.Token) != 32 {
		t.Fatalf("remote.json = %s (%v)", first, err)
	}
	mustContain(t, "stdout", out, "/r/"+id.Token)

	code, out, errOut = runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("remote.json змінився між запусками:\n%s\n%s", first, second)
	}
	if log.count() != 2 || log.ports[0] != id.Port || log.ports[1] != id.Port {
		t.Fatalf("порти слухача = %v, want двічі %d", log.ports, id.Port)
	}
	if strings.Contains(out, strings.SplitN(i18n.MsgRemotePortBusy, "%", 2)[0]) {
		t.Fatalf("вільний порт названо зайнятим:\n%s", out)
	}
}

// Зайнятий збережений порт: пульт піднімається на ефемерному, попереджає,
// а remote.json лишається — закладка повернеться наступного разу.
func TestRemoteBusyPortFallsBackWithoutRepin(t *testing.T) {
	remoteEnv(t)
	dir, _, _ := journeyEnv(t,
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	path := filepath.Join(dir, "state", "remote.json")
	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	saved, _ := os.ReadFile(path)
	var id store.RemoteIdentity
	if err := json.Unmarshal(saved, &id); err != nil {
		t.Fatal(err)
	}

	blocker, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(id.Port))
	if err != nil {
		t.Fatalf("не вдалося зайняти порт %d: %v", id.Port, err)
	}
	defer func() { _ = blocker.Close() }()

	code, out, errOut = runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, fmt.Sprintf(strings.SplitN(i18n.MsgRemotePortBusy, " —", 2)[0], id.Port))
	if strings.Contains(out, ":"+strconv.Itoa(id.Port)+"/r/") {
		t.Fatalf("адреса пульта досі на зайнятому порту:\n%s", out)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(saved, after) {
		t.Fatalf("remote.json перезаписано ефемерним портом:\n%s\n%s", saved, after)
	}
}

// doctor читає стан без злиття журналу: його запускають ПІД ЧАС відтворення,
// щоб дізнатися адресу пульта, і він не має з'їсти журнал активної сесії.
func TestDoctorDoesNotTouchJournal(t *testing.T) {
	dir, _, _ := journeyEnv(t)
	journal := filepath.Join(dir, "state", "current.json")
	library := filepath.Join(dir, "library.json")
	if err := os.MkdirAll(filepath.Dir(journal), 0o700); err != nil {
		t.Fatal(err)
	}
	journalBody := []byte(`{"title_id":"t1","episode":1,"position_sec":125,"duration_sec":1440,"updated_at":"2026-09-02T10:00:00Z"}`)
	libraryBody := []byte(`{"titles":[{"id":"t1","name":"T","sources":[{"provider":"anitube","slug":"1-t","name":"T"}]}]}`)
	if err := os.WriteFile(journal, journalBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, libraryBody, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCLI(t, "doctor", "--json")
	mustExit(t, 0, code, out, errOut)
	for name, want := range map[string][]byte{journal: journalBody, library: libraryBody} {
		got, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s змінився після doctor (%v):\n%s", filepath.Base(name), err, got)
		}
	}
}

func TestDoctorReportsRemote(t *testing.T) {
	remoteEnv(t)
	dir, _, _ := journeyEnv(t, playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}))

	// свіжий каталог: пульт ще не запускався — адреси з портом 0 бути не має
	code, out, errOut := runCLI(t, "doctor", "--json")
	mustExit(t, 0, code, out, errOut)
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, out)
	}
	if !rep.Remote.Enabled || rep.Remote.Port != 0 || rep.Remote.URL != "" {
		t.Fatalf("remote до першого запуску = %+v", rep.Remote)
	}
	code, out, errOut = runCLI(t, "doctor")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, i18n.MsgDoctorRemoteNew)

	code, out, errOut = runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	id := remoteIdentity(t, dir)

	code, out, errOut = runCLI(t, "doctor", "--json")
	mustExit(t, 0, code, out, errOut)
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Remote.Port != id.Port || !strings.HasSuffix(rep.Remote.URL, ":"+strconv.Itoa(id.Port)+"/r/"+id.Token) {
		t.Fatalf("remote після запуску = %+v, want порт %d і токен", rep.Remote, id.Port)
	}
	code, out, errOut = runCLI(t, "doctor")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgDoctorRemote, rep.Remote.URL))

	writeConfig(t, dir, `{"remote":"off"}`)
	code, out, errOut = runCLI(t, "doctor")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, i18n.MsgDoctorRemoteOff)
}

// remote: open — пульт у корені без токена; порт і токен усе одно зберігаються,
// щоб повернення на on дало стару адресу. Doctor знає про режим.
func TestRemoteOpenModeServesAtRoot(t *testing.T) {
	remoteEnv(t)
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	dir, _, _ := journeyEnv(t, held)
	writeConfig(t, dir, `{"remote":"open"}`)

	type reply struct {
		id   store.RemoteIdentity
		st   remote.Status
		code int
		err  error
	}
	done := make(chan reply, 1)
	go func() {
		id := remoteIdentity(t, dir)
		base := "http://127.0.0.1:" + strconv.Itoa(id.Port) + "/"
		st, code, err := remoteCommand(dir, base, "stop")
		done <- reply{id, st, code, err}
	}()

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	r := <-done
	if r.err != nil {
		t.Fatalf("пульт: %v", r.err)
	}
	if r.code != http.StatusOK || r.st.Playing {
		t.Fatalf("POST stop = %d %+v, want 200 і Playing=false", r.code, r.st)
	}
	if len(r.id.Token) != 32 {
		t.Fatalf("токен у remote.json має зберігатися й у відкритому режимі: %+v", r.id)
	}
	mustContain(t, "stdout", out, ":"+strconv.Itoa(r.id.Port)+"/\n")
	mustContain(t, "stdout", out, i18n.MsgRemoteOpen)
	if strings.Contains(out, "/r/") {
		t.Fatalf("адреса з токеном у відкритому режимі:\n%s", out)
	}

	code, out, errOut = runCLI(t, "doctor", "--json")
	mustExit(t, 0, code, out, errOut)
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Remote.Enabled || !rep.Remote.Open || rep.Remote.Port != r.id.Port ||
		!strings.HasSuffix(rep.Remote.URL, ":"+strconv.Itoa(r.id.Port)+"/") {
		t.Fatalf("doctor remote = %+v", rep.Remote)
	}
	code, out, errOut = runCLI(t, "doctor")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgDoctorRemoteOpen, rep.Remote.URL))

	// назад на on — той самий порт і токен
	writeConfig(t, dir, `{"remote":"on"}`)
	code, out, errOut = runCLI(t, "doctor", "--json")
	mustExit(t, 0, code, out, errOut)
	var again doctorReport
	if err := json.Unmarshal([]byte(out), &again); err != nil {
		t.Fatal(err)
	}
	if again.Remote.Open || !strings.HasSuffix(again.Remote.URL, ":"+strconv.Itoa(r.id.Port)+"/r/"+r.id.Token) {
		t.Fatalf("після повернення на on: %+v", again.Remote)
	}
}

// Гарячий перезапуск з екрана налаштувань: on → open → off → on тримає той
// самий порт і токен, remote.json не змінюється.
func TestRestartRemoteSwitchesMode(t *testing.T) {
	log := remoteEnv(t)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	live := &playback.Live{}

	run, err := startRemote(st, live, "on")
	if err != nil || run.srv == nil || !strings.Contains(run.URL, "/r/") {
		t.Fatalf("on: %+v, %v", run, err)
	}
	id := remoteIdentity(t, dir)
	saved, err := os.ReadFile(filepath.Join(dir, "state", "remote.json"))
	if err != nil {
		t.Fatal(err)
	}
	run.Close()

	run, err = startRemote(st, live, "open")
	if err != nil || run.srv == nil || !strings.HasSuffix(run.URL, ":"+strconv.Itoa(id.Port)+"/") || strings.Contains(run.URL, "/r/") {
		t.Fatalf("open: %+v, %v", run, err)
	}
	info := run.info(nil)
	if info.URL != run.URL || info.Err != nil || info.Warn != nil {
		t.Fatalf("info(open) = %+v", info)
	}
	run.Close()

	run, err = startRemote(st, live, "off")
	if err != nil || run.srv != nil || run.URL != "" {
		t.Fatalf("off: %+v, %v", run, err)
	}
	run.Close() // на порожньому run — без паніки

	run, err = startRemote(st, live, "on")
	if err != nil || !strings.HasSuffix(run.URL, ":"+strconv.Itoa(id.Port)+"/r/"+id.Token) {
		t.Fatalf("on again: %+v, %v", run, err)
	}
	run.Close()

	for _, p := range log.ports {
		if p != id.Port {
			t.Fatalf("порти слухача = %v, want усі %d", log.ports, id.Port)
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "state", "remote.json"))
	if err != nil || !bytes.Equal(saved, after) {
		t.Fatalf("remote.json змінився:\n%s\n%s (%v)", saved, after, err)
	}
	if _, err := startRemote(st, nil, "on"); err != nil {
		t.Fatalf("без Live: %v", err)
	}
}

// Слухач не піднявся: startRemote повертає помилку з порожнім run; headless
// play друкує її й грає далі.
func TestStartRemoteListenError(t *testing.T) {
	remoteEnv(t)
	boom := errors.New("listen: boom")
	remoteListen = func(int) (net.Listener, bool, error) { return nil, false, boom }

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := startRemote(st, &playback.Live{}, "on")
	if !errors.Is(err, boom) || run.srv != nil || run.URL != "" {
		t.Fatalf("startRemote = %+v, %v", run, err)
	}
	if info := run.info(err); !errors.Is(info.Err, boom) || info.Warn != nil || info.URL != "" {
		t.Fatalf("info = %+v", info)
	}

	dir, _, _ := journeyEnv(t, playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}))
	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stderr", errOut, strings.SplitN(i18n.MsgRemoteFailed, "%", 2)[0])
	if strings.Contains(out, strings.SplitN(i18n.MsgRemoteURL, "%", 2)[0]) {
		t.Fatalf("адреса надрукована без пульта:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "remote.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote.json не має існувати: %v", err)
	}
}

// remote.json не записався: пульт працює, адреса є, помилка — попередження,
// а Close звільняє порт.
func TestStartRemoteIdentitySaveWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ігнорує права каталогу")
	}
	remoteEnv(t)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(state, 0o755) })

	run, err := startRemote(st, &playback.Live{}, "on")
	if err == nil || run.srv == nil || run.URL == "" {
		t.Fatalf("startRemote = %+v, %v", run, err)
	}
	info := run.info(err)
	if info.Warn == nil || info.Err != nil || info.URL != run.URL {
		t.Fatalf("info = %+v", info)
	}
	port := run.Port
	run.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("порт %d не звільнено після Close: %v", port, err)
	}
	_ = ln.Close()
}

// «Досидіти й зупинитись» діє й у headless: кнопку тисне справжній пульт по
// HTTP зі своєї горутини, а цикл play після EOF не йде за автоплеєм далі.
func TestJourneyStopAfterEndsHeadlessChain(t *testing.T) {
	remoteEnv(t)
	held := playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440})
	held.Hold = true
	dir, _, fp := journeyEnv(t, held, playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440}))
	writeConfig(t, dir, `{"autoplay":"always","remote":"on"}`)

	done := make(chan error, 1)
	go func() {
		st, code, err := remoteCommand(dir, remoteBase(remoteIdentity(t, dir)), "stopafter")
		// серія має дограти до кінця — інакше перевірятиметься не той сценарій
		held.Release()
		switch {
		case err != nil:
			done <- err
		case code != http.StatusOK || !st.StopAfter:
			done <- fmt.Errorf("POST stopafter = %d %+v", code, st)
		default:
			done <- nil
		}
	}()

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	if err := <-done; err != nil {
		t.Fatalf("пульт: %v", err)
	}
	if n := len(fp.Starts()); n != 1 {
		t.Fatalf("запусків плеєра = %d, want 1 (ланцюжок мав зупинитися)", n)
	}
	if strings.Contains(out, fmt.Sprintf(i18n.MsgResolving, 2)) {
		t.Fatalf("цикл пішов за наступною серією:\n%s", out)
	}
}

// remotePlaylistTap грає роль телефона: дочікується сесії, читає список серій
// звичайним GET episodes і тапає по рядку — усе через справжній HTTP-хендлер.
func remotePlaylistTap(dir, base string, n int) error {
	client := &http.Client{Timeout: 2 * time.Second}
	journal := filepath.Join(dir, "state", "current.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// журнал уже має позицію: тап закриває сесію без додаткового семплу
		if _, err := os.Stat(journal); err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		var st remote.Status
		if err := getJSON(client, base+"status", &st); err != nil || !st.Playing {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var pl remote.Playlist
		if err := getJSON(client, base+"episodes", &pl); err != nil {
			return err
		}
		switch {
		case pl.Gen == 0 || pl.Gen != st.PlaylistGen:
			return fmt.Errorf("список серій = %+v, а статус каже про покоління %d", pl, st.PlaylistGen)
		case len(pl.Episodes) == 0:
			return fmt.Errorf("список серій порожній: %+v", pl)
		case !pl.Episodes[0].Current:
			return fmt.Errorf("серія, що грає, не позначена: %+v", pl.Episodes[0])
		}
		resp, err := client.Post(fmt.Sprintf("%splay/%d/%d", base, pl.Gen, n), "", nil)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("POST play/%d/%d = %d", pl.Gen, n, resp.StatusCode)
		}
		return nil
	}
	return errors.New("сесія так і не заграла")
}

func getJSON(client *http.Client, url string, v any) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s = %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// Плейлист працює й у headless: цикл play публікує список серій перед кожним
// запуском, пульт читає його по HTTP, а тап по рядку переводить ланцюжок на
// названу серію (а не на наступну — автоплей тут вимкнено).
func TestJourneyRemotePlaylistSwitchesHeadlessEpisode(t *testing.T) {
	remoteEnv(t)
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	dir, _, fp := journeyEnv(t, held, playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}))
	writeConfig(t, dir, `{"autoplay":"never","remote":"on"}`)

	done := make(chan error, 1)
	go func() { done <- remotePlaylistTap(dir, remoteBase(remoteIdentity(t, dir)), 3) }()

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	if err := <-done; err != nil {
		t.Fatalf("пульт: %v", err)
	}
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgResolving, 3))
	starts := fp.Starts()
	if len(starts) != 2 || !strings.HasSuffix(starts[1].MediaTitle, " · 3") {
		t.Fatalf("запуски плеєра = %+v, want другу серію 3", starts)
	}
	lib := loadLibrary(t, dir)
	title := lib.TitleByRef(fixtureRef(t))
	if p := lib.ProgressFor(title.ID, 1); p == nil || p.PositionSec != 40 {
		t.Errorf("серія 1 = %+v, want 40 с", p)
	}
}
