// Command fixturegen writes the KMZ test fixtures into testdata. The
// archives are built from hand-written content with fixed timestamps, so
// running it again produces identical bytes. Run it from the repo root:
//
//	go run ./internal/fixturegen
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/softdevstudio/ikml/internal/fixtures"
)

func main() {
	for _, f := range fixtures.KMZ() {
		path := filepath.Join("testdata", f.Dir, f.Name)
		if err := os.WriteFile(path, f.Data(), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", path)
	}
}
