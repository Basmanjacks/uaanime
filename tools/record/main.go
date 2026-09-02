// record — перезапис канонічних фікстур з живих сайтів.
// Запускається вручну (make record-fixtures), ніколи в CI.
// Списки URL живуть у пакетах провайдерів/екстракторів, не тут.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Basmanjacks/uaanime/internal/extractor/ashdi"
	"github.com/Basmanjacks/uaanime/internal/extractor/moonanime"
	"github.com/Basmanjacks/uaanime/internal/extractor/tortuga"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider/anitube"
)

func main() { os.Exit(run()) }

// run повертає код виходу замість os.Exit посеред тіла: інакше `defer cancel()`
// не виконується (контекст лишається живим до завершення процесу).
func run() int {
	onlyNew := flag.Bool("new", false, "записати лише нові фікстури AniTube")
	only := flag.String("only", "", "записати лише один крок за іменем (anitube, ashdi, tortuga, moonanime)")
	flag.Parse()
	if *onlyNew && *only != "" {
		fmt.Fprintln(os.Stderr, "-new і -only взаємовиключні")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Той самий клієнт, що й у бою: запис фікстури не має ходити за редиректом
	// на чужий хост і має нести наскрізний User-Agent застосунку.
	client := httpx.NewClient(nil)
	if *onlyNew {
		dir := "internal/provider/anitube/testdata"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := anitube.RecordNewFixtures(ctx, client, dir); err != nil {
			fmt.Fprintf(os.Stderr, "anitube: %v\n", err)
			return 1
		}
		fmt.Printf("anitube: нові фікстури оновлено в %s\n", dir)
		return 0
	}

	steps := []struct {
		name string
		dir  string
		run  func(context.Context, *http.Client, string) error
	}{
		{"anitube", "internal/provider/anitube/testdata", anitube.RecordFixtures},
		{"ashdi", "internal/extractor/ashdi/testdata", ashdi.RecordFixtures},
		{"tortuga", "internal/extractor/tortuga/testdata", tortuga.RecordFixtures},
		{"moonanime", "internal/extractor/moonanime/testdata", moonanime.RecordFixtures},
	}
	if *only != "" {
		// запис однієї фікстури не має переписувати решту з живих сайтів
		var picked []struct {
			name string
			dir  string
			run  func(context.Context, *http.Client, string) error
		}
		for _, s := range steps {
			if s.name == *only {
				picked = append(picked, s)
			}
		}
		if len(picked) == 0 {
			fmt.Fprintf(os.Stderr, "невідомий крок %q\n", *only)
			return 2
		}
		steps = picked
	}
	for _, s := range steps {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := s.run(ctx, client, s.dir); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", s.name, err)
			return 1
		}
		fmt.Printf("%s: фікстури оновлено в %s\n", s.name, s.dir)
	}
	return 0
}
