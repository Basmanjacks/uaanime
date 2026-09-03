package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/remote"
	"github.com/Basmanjacks/uaanime/internal/store"
	"github.com/Basmanjacks/uaanime/internal/ui"
)

// remoteControl — перехідник між пультом і рушієм: playback не знає про HTTP,
// remote не знає про playback, тому обидва інтерфейси зустрічаються лише тут.
type remoteControl struct{ live *playback.Live }

func (c remoteControl) Status() (remote.Status, error) {
	snap, err := c.live.Snapshot()
	if err != nil {
		return remote.Status{}, err
	}
	return remote.Status{
		Playing:     snap.Playing,
		Title:       snap.Title,
		Episode:     snap.Episode,
		PositionSec: snap.PositionSec,
		DurationSec: snap.DurationSec,
		Paused:      snap.Paused,
		VolumePct:   snap.VolumePct,
		StopAfter:   snap.StopAfter,
		// PlaylistGen лишається нулем («списку немає»), поки плейлист не
		// з'явиться в Live.
		PlaylistGen: 0,
	}, nil
}

func (c remoteControl) TogglePause() error          { return mapRemoteErr(c.live.TogglePause()) }
func (c remoteControl) Seek(deltaSec float64) error { return mapRemoteErr(c.live.Seek(deltaSec)) }
func (c remoteControl) SeekTo(posSec float64) error { return mapRemoteErr(c.live.SeekTo(posSec)) }
func (c remoteControl) Next() error                 { return mapRemoteErr(c.live.Next()) }
func (c remoteControl) Stop() error                 { return mapRemoteErr(c.live.Stop()) }

func (c remoteControl) AddVolume(delta float64) error {
	return mapRemoteErr(c.live.AddVolume(delta))
}

func mapRemoteErr(err error) error {
	if errors.Is(err, playback.ErrNotPlaying) {
		return fmt.Errorf("%w", remote.ErrNotPlaying)
	}
	return err
}

// remoteListen — шов для тестів: наскрізні сценарії слухають лише loopback,
// щоб не будити діалог фаєрвола macOS і не залежати від пісочниці.
var remoteListen = remote.Listen

// remoteRun — усе, що викликачам треба знати про запущений пульт. Нульове
// значення = пульт не запущено (вимкнено або не зміг стартувати).
type remoteRun struct {
	srv       *remote.Server
	URL       string // основна адреса (mDNS-ім'я)
	AltURL    string // за IP; "" якщо збігається чи мережі нема
	Open      bool   // без токена — попередити один раз
	Port      int    // фактичний порт
	Ephemeral bool   // збережений порт був зайнятий — закладка цього разу не спрацює
	savedPort int    // той зайнятий порт, який треба назвати у попередженні
}

// remoteShutdown — скільки чекати на відкриті запити при виході; більше не
// варто: користувач уже виходить, а пульт і так лише читає стан.
const remoteShutdown = 2 * time.Second

func (r *remoteRun) Close() {
	if r.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteShutdown)
	defer cancel()
	_ = r.srv.Close(ctx)
}

// announce друкує адресу для headless-режиму (TUI показує її сам).
func (r *remoteRun) announce() {
	if r.URL == "" {
		return
	}
	outf(i18n.MsgRemoteURL+"\n", r.URL)
	if r.AltURL != "" {
		outf(i18n.MsgRemoteURLAlt+"\n", r.AltURL)
	}
	if r.Open {
		outln(i18n.MsgRemoteOpen)
	}
	if r.Ephemeral {
		outf(i18n.MsgRemotePortBusy+"\n", r.savedPort, r.URL)
	}
}

// startRemote піднімає пульт під режим mode ("on" | "open" | "off"). Невдача
// не фатальна для застосунку: без пульта він працює рівно так, як працював
// досі, тому помилку повертаємо, а не друкуємо — під TUI stderr це
// альтернативний екран. Контракт: помилка з порожнім run — пульта немає;
// помилка з живим run — попередження (remote.json не записався: пульт працює,
// але закладка може не пережити перезапуск). Збережений порт зайнятий →
// беремо ефемерний, а remote.json НЕ перезаписуємо: закладка повернеться
// наступного запуску. У режимі open токен у remote.json усе одно
// зберігається, щоб повернення на on дало стару адресу.
func startRemote(st *store.Store, live *playback.Live, mode string) (remoteRun, error) {
	if live == nil || mode == "off" {
		return remoteRun{}, nil
	}
	id, err := st.LoadRemoteIdentity()
	if err != nil {
		return remoteRun{}, err
	}
	ln, ephemeral, err := remoteListen(id.Port)
	if err != nil {
		return remoteRun{}, err
	}
	port := remote.Port(ln)
	var warn error
	if !ephemeral && id.Port != port {
		warn = st.SaveRemoteIdentity(store.RemoteIdentity{Port: port, Token: id.Token})
	}
	ctrl := remoteControl{live: live}
	open := mode == "open"
	token := id.Token
	var h http.Handler
	if open {
		token = ""
		h, err = remote.NewOpenHandler(ctrl)
	} else {
		h, err = remote.NewHandler(token, ctrl)
	}
	if err != nil {
		_ = ln.Close()
		return remoteRun{}, err
	}
	return remoteRun{
		srv:       remote.Start(ln, h),
		URL:       remote.URL(port, token),
		AltURL:    remote.AltURL(port, token),
		Open:      open,
		Port:      port,
		Ephemeral: ephemeral,
		savedPort: id.Port,
	}, warn
}

// info — стан пульта для TUI; err — те, що повернув startRemote, і саме
// наявність слухача каже, фатальна це помилка чи попередження.
func (r *remoteRun) info(err error) ui.RemoteInfo {
	info := ui.RemoteInfo{URL: r.URL, AltURL: r.AltURL, Ephemeral: r.Ephemeral, SavedPort: r.savedPort}
	if err == nil {
		return info
	}
	if r.srv == nil {
		info.Err = err
	} else {
		info.Warn = err
	}
	return info
}

// reportRemoteErr друкує помилку старту пульта у headless-режимі.
func (r *remoteRun) reportRemoteErr(err error) {
	if err == nil {
		return
	}
	if r.srv == nil {
		errf(i18n.MsgRemoteFailed+"\n", err)
	} else {
		errf(i18n.MsgRemoteIdentityUnsaved+"\n", err)
	}
}

// doctorRemote — стан пульта для doctor: адреса відновлюється без сокета,
// лише з remote.json, тож команда безпечна під час відтворення.
type doctorRemote struct {
	Enabled bool   `json:"enabled"`
	Open    bool   `json:"open,omitempty"`
	Port    int    `json:"port,omitempty"`
	URL     string `json:"url,omitempty"`
}

func (a *app) doctorRemoteReport() doctorRemote {
	rep := doctorRemote{Enabled: a.cfg.Remote != "off", Open: a.cfg.Remote == "open"}
	if !rep.Enabled {
		return rep
	}
	id, err := a.store.LoadRemoteIdentity()
	if err != nil || id.Port == 0 {
		return rep
	}
	token := id.Token
	if rep.Open {
		token = ""
	}
	rep.Port = id.Port
	rep.URL = remote.URL(id.Port, token)
	return rep
}

func printDoctorRemote(rep doctorRemote) {
	switch {
	case !rep.Enabled:
		outln(i18n.MsgDoctorRemoteOff)
	case rep.URL == "":
		outln(i18n.MsgDoctorRemoteNew)
	case rep.Open:
		outf(i18n.MsgDoctorRemoteOpen+"\n", rep.URL)
	default:
		outf(i18n.MsgDoctorRemote+"\n", rep.URL)
	}
}
