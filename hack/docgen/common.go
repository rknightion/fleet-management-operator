package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// generator is the function shape every regen subcommand satisfies.
type generator func(root string) ([]byte, error)

// sourceBaseURL prefixes every "where this came from" link in the generated
// docs. Repo-relative links (`../internal/...`) resolve on GitHub but 404 on
// the published docs site, which serves docs/ alone — so the links must be
// absolute. Hand-editing them after each run is what put these files out of
// sync with the generator in the first place.
const sourceBaseURL = "https://github.com/rknightion/fleet-management-operator/blob/main/"

// docFuncs is shared by every generator template.
var docFuncs = template.FuncMap{"srcURL": srcURL}

// srcURL turns a repo-relative path — optionally suffixed ":<line>", which is
// how the flags and metrics generators emit it — into an absolute GitHub blob
// URL with a line anchor.
func srcURL(target string) string {
	path, line, hasLine := strings.Cut(target, ":")
	url := sourceBaseURL + path
	if hasLine && line != "" {
		url += "#L" + line
	}
	return url
}

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
