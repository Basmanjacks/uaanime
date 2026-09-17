//go:build darwin || linux

package store

import (
	"errors"
	"math"
	"syscall"
)

// freeBytes — скільки байтів на ФС каталогу доступно звичайному процесу.
// Саме Bavail, а не Bfree: різницю тримає резерв root-а, і завантаження в неї
// не влізе. false — «невідомо»; викликач тоді просто не робить перевірки.
func freeBytes(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	// Типи полів різні на darwin (Bsize uint32) і linux (int64), тому
	// звужуємо до int64 явно й перевіряємо вже результат.
	bsize, bavail := int64(st.Bsize), int64(st.Bavail)
	if bsize <= 0 || bavail < 0 {
		return 0, false
	}
	if bavail > math.MaxInt64/bsize {
		return math.MaxInt64, true
	}
	return bavail * bsize, true
}

// diskErrno класифікує помилки ФС, для яких у нас є окремий сентинел.
// EACCES ловиться ще й через os.ErrPermission, але EROFS і ENOSPC свого
// відповідника в пакеті os не мають — потрібен syscall, а він платформо-
// залежний, тому живе тут, поруч зі Statfs, а не в портабельному файлі.
func diskErrno(err error) (noWrite, full bool) {
	noWrite = errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EROFS)
	full = errors.Is(err, syscall.ENOSPC)
	return noWrite, full
}
