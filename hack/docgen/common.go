package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// generator is the function shape every regen subcommand satisfies.
type generator func(root string) ([]byte, error)

// runGenerator executes gen and either writes the result to outPath or, in
// check mode, fails if the bytes on disk differ from the freshly generated
// output. The diff hint always names `make docs` so contributors get a clear
// remediation path on CI failure.
func runGenerator(root, outPath string, check bool, gen generator) error {
	if outPath == "" {
		return fmt.Errorf("--out is required")
	}
	got, err := gen(root)
	if err != nil {
		return err
	}
	resolved := outPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, outPath)
	}
	if check {
		want, err := os.ReadFile(resolved)
		if err != nil {
			return fmt.Errorf("read existing %s: %w", outPath, err)
		}
		if !bytes.Equal(want, got) {
			return fmt.Errorf("%s is out of date — run 'make docs' to regenerate", outPath)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return err
	}
	return os.WriteFile(resolved, got, 0o644)
}
