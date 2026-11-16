package errs

import (
	"errors"
	"fmt"
)

var (
	// ErrHostUnsupported — релізи серії є на сайті, але жоден екстрактор не
	// вміє їхній відеохост. Підклас ErrNoStream: потоку немає, проте людині й
	// скрипту корисно знати, що причина — непідтримуваний хост, а не порожня серія.
	ErrHostUnsupported = fmt.Errorf("відеохост не підтримується: %w", ErrNoStream)
	// ErrReleaseUnavailable — явно запитаного релізу (--studio/--kind) немає
	// на сайті або всі його потоки мертві. Інший реліз не підставляється.
	ErrReleaseUnavailable = errors.New("запитаний реліз недоступний")
	// ErrQualityMissing — явно запитаної якості в релізі немає.
	ErrQualityMissing = errors.New("запитаної якості немає")
)
