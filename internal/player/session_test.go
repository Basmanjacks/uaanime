package player

import (
	"os/exec"
	"testing"
	"time"
)

// Інтеграційний тест з реальним mpv на синтетичному джерелі (lavfi, без мережі).
// Пропускається, якщо mpv не встановлено (наприклад, у голому CI).
func TestSessionIPC(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv не встановлено")
	}

	sess, err := Start("av://lavfi:testsrc2=duration=60", "тест", nil, 7,
		"--no-config", "--vo=null", "--ao=null")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()
	go func() { _ = sess.Wait() }()

	// --start=7 має бути застосований, а позиція — рухатися
	deadline := time.Now().Add(15 * time.Second)
	var pos float64
	for time.Now().Before(deadline) {
		pos, err = sess.TimePos()
		if err == nil && pos > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("TimePos: %v", err)
	}
	if pos < 6.5 {
		t.Errorf("--start=7 не застосовано: позиція %v", pos)
	}

	// lavfi не знає повної тривалості наперед — достатньо, що властивість читається
	dur, err := sess.Duration()
	if err != nil || dur <= 0 {
		t.Errorf("Duration = (%v, %v), очікував > 0", dur, err)
	}
}
