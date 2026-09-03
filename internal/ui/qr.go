package ui

import (
	"strings"

	"github.com/Basmanjacks/uaanime/internal/qr"
)

// Зона тиші навколо символу. Стандарт вимагає чотири модулі; два — свідомий
// компроміс для вузького вікна: сканери телефонів читають і таке, а вибір між
// «трохи вужча рамка» і «коду немає взагалі» очевидний.
const (
	qrQuietFull  = 4
	qrQuietTight = 2
)

// qrUpperHalf — напівблок: один рядок терміналу несе два модулі. Колір тексту
// малює верхній модуль, колір фону — нижній, тож символ виходить квадратним
// навіть у клітинці зі співвідношенням 1:2. ASCII-заміни в цього символу
// немає — у режимі UAANIME_ASCII код просто не малюється.
const qrUpperHalf = "▀"

// qrBlock малює QR-код тексту так, щоб він вліз у w колонок і h рядків.
// Зона тиші зменшується з 4 модулів до 2, і лише якщо не вліз і такий —
// повертається false: обрізаний QR не сканується, тому краще не показувати
// нічого.
func qrBlock(url string, w, h int) (string, bool) {
	if url == "" || w <= 0 || h <= 0 {
		return "", false
	}
	m, err := qr.Encode(url)
	if err != nil {
		return "", false
	}
	for _, quiet := range []int{qrQuietFull, qrQuietTight} {
		side := m.Size() + 2*quiet
		if side > w || qrRows(side) > h {
			continue
		}
		return renderQR(m, quiet), true
	}
	return "", false
}

// qrRows — висота символу зі стороною side модулів у рядках терміналу.
// Непарна сторона добирається світлим рядком: він однаково лежить у зоні тиші.
func qrRows(side int) int { return (side + 1) / 2 }

// renderQR склеює символ у рядки напівблоків. Однакові клітинки об'єднуються в
// один прогін: інакше кожен модуль ніс би власну ANSI-послідовність і кадр
// розпухав би вдесятеро.
func renderQR(m qr.Matrix, quiet int) string {
	side := m.Size() + 2*quiet
	var b strings.Builder
	for row := 0; row < side; row += 2 {
		if row > 0 {
			b.WriteByte('\n')
		}
		runStart, upper, lower := 0, false, false
		flush := func(end int) {
			if end > runStart {
				b.WriteString(qrCellStyle(upper, lower).Render(strings.Repeat(qrUpperHalf, end-runStart)))
			}
		}
		for col := 0; col < side; col++ {
			// Координати матриці зсунуті на зону тиші; поза символом At
			// повертає «світло», тому рамка малюється тим самим циклом.
			up, low := m.At(col-quiet, row-quiet), m.At(col-quiet, row+1-quiet)
			if col == 0 {
				upper, lower = up, low
				continue
			}
			if up != upper || low != lower {
				flush(col)
				runStart, upper, lower = col, up, low
			}
		}
		flush(side)
	}
	return b.String()
}
