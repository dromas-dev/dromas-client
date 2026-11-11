package main

import (
	"context"
	"flag"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var updateDocs = flag.Bool("update", false, "write docs/commands from the command tree")

// commandsDir is the command reference, a page for each command.
var commandsDir = filepath.Join("..", "..", "docs", "commands")

// The command reference in docs/commands is cobra's own Markdown of the
// command tree (cobra/doc), a page for each command: its use, its help and
// its flags. A change to a command, a flag or a help text changes a page,
// and this test fails until the pages are written again:
//
//	go test ./cmd/dromas -run TestTheCommandPagesAreTheHelp -update
func TestTheCommandPagesAreTheHelp(t *testing.T) {
	e := &cli{ctx: context.Background(), stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	root := e.tree()
	e.flags.add(root)
	var all func(*cobra.Command)
	all = func(c *cobra.Command) {
		c.DisableAutoGenTag = true // the pages carry no date
		// cobra writes a command's help as Markdown prose, where <link> reads
		// as an HTML tag that GitHub and the site drop: escaped, it reads as
		// the help prints it.
		c.Short = strings.ReplaceAll(c.Short, "<", `\<`)
		c.Long = strings.ReplaceAll(c.Long, "<", `\<`)
		for _, s := range c.Commands() {
			all(s)
		}
	}
	all(root)
	dir := t.TempDir()
	if err := doc.GenMarkdownTree(root, dir); err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range entries {
		b, err := os.ReadFile(filepath.Join(dir, f.Name())) //nolint:gosec // a file this test wrote
		if err != nil {
			t.Fatal(err)
		}
		want[f.Name()] = b
	}
	if len(want) < 20 || want["dromas.md"] == nil || want["dromas_send.md"] == nil {
		t.Fatalf("cobra wrote %d pages: the tree is broken", len(want))
	}
	if *updateDocs {
		if err := os.RemoveAll(commandsDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(commandsDir, 0o755); err != nil { //nolint:gosec // a directory of this repo
			t.Fatal(err)
		}
		for name, b := range want {
			if err := os.WriteFile(filepath.Join(commandsDir, name), b, 0o644); err != nil { //nolint:gosec // a page of this repo
				t.Fatal(err)
			}
		}
	}
	have, err := os.ReadDir(commandsDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range have {
		seen[f.Name()] = true
		b, err := os.ReadFile(filepath.Join(commandsDir, f.Name())) //nolint:gosec // a page of this repo
		if err != nil {
			t.Fatal(err)
		}
		if w, ok := want[f.Name()]; !ok {
			t.Errorf("docs/commands/%s names no command: run this test with -update", f.Name())
		} else if string(b) != string(w) {
			t.Errorf("docs/commands/%s is not the command's help: run this test with -update", f.Name())
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("docs/commands/%s is missing: run this test with -update", name)
		}
	}
}

// Every relative link in the README and docs/ names a file or directory of
// this repo, so each link resolves on GitHub, and on dromas.dev, which shows
// these pages at the paths they have here. A link's #fragment is not read.
func TestTheDocsLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	link := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	pages := []string{filepath.Join(root, "README.md")}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			pages = append(pages, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, page := range pages {
		b, err := os.ReadFile(page) //nolint:gosec // a page of this repo
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range link.FindAllStringSubmatch(inlineCode.ReplaceAllString(string(b), ""), -1) {
			dest, _, _ := strings.Cut(m[1], "#")
			if dest == "" || strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") {
				continue
			}
			n++
			if _, err := os.Stat(filepath.Join(filepath.Dir(page), filepath.FromSlash(dest))); err != nil {
				t.Errorf("%s links %s, which is not a file of this repo", page, m[1])
			}
		}
	}
	if n < 50 {
		t.Fatalf("read %d relative links: the reading is broken", n)
	}
}
