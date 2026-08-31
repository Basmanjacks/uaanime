// record — перезапис канонічних фікстур з живих сайтів.
// Запускається вручну (make record-fixtures), ніколи в CI.
// Списки URL живуть у пакетах провайдерів/екстракторів, не тут.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Basmanjacks/uaanime/internal/extractor/ashdi"
	"github.com/Basmanjacks/uaanime/internal/provider/anitube"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 20 * time.Second}

	steps := []struct {
		name string
		dir  string
		run  func(context.Context, *http.Client, string) error
	}{
		{"anitube", "internal/provider/anitube/testdata", anitube.RecordFixtures},
		{"ashdi", "internal/extractor/ashdi/testdata", ashdi.RecordFixtures},
	}
	for _, s := range steps {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := s.run(ctx, client, s.dir); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", s.name, err)
			os.Exit(1)
		}
		fmt.Printf("%s: фікстури оновлено в %s\n", s.name, s.dir)
	}
}
