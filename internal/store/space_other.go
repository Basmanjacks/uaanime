//go:build !darwin && !linux

package store

// Релізи цілять у Linux і macOS; на решті систем вільне місце невідоме —
// це валідна відповідь, а не помилка: перевірку просто не роблять.
func freeBytes(string) (int64, bool) { return 0, false }

// Без syscall-констант цієї платформи класифікувати нічого: помилка піде
// до користувача як є, обгорнута шляхом.
func diskErrno(error) (noWrite, full bool) { return false, false }
