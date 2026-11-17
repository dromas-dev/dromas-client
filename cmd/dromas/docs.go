package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cpuguy83/go-md2man/v2/md2man"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/spf13/pflag"
)

// genDocsCmd writes the command reference and the man pages from the
// command tree, as GoReleaser's own hidden man command does for its release
// (.goreleaser.yaml's before hook). It is in no help: it is a build step,
// and reads no mailbox.
func (e *cli) genDocsCmd() *cobra.Command {
	var markdown, man, docs string
	c := command("gen-docs", "write the command reference and the man pages",
		`Write cobra's Markdown of the command tree to --markdown-dir, and its man pages
to --man-dir: man1/ a page for each command, man5/dromas.toml.5 from
configuration.md in --docs-dir, and man7/dromas-output.7 from output.md there.
A page is dated by SOURCE_DATE_EPOCH where it is set, so a rebuild writes the
same bytes.`,
		nargs(0, 0), func(*cobra.Command, []string) error {
			date := time.Now()
			if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
				n, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return fmt.Errorf("SOURCE_DATE_EPOCH: %w", err)
				}
				date = time.Unix(n, 0)
			}
			return writeDocs(markdown, man, docs, date)
		})
	c.Hidden = true
	c.Flags().StringVar(&markdown, "markdown-dir", "", "the directory for the Markdown pages (none: not written)")
	c.Flags().StringVar(&man, "man-dir", "", "the directory for the man pages (none: not written)")
	c.Flags().StringVar(&docs, "docs-dir", "docs", "the directory of configuration.md and output.md")
	return c
}

// docTree is the command tree as its pages describe it: with its flags, no
// date, and a help's <link> escaped, which Markdown, and go-md2man for the
// man pages, would otherwise read as an HTML tag and drop.
func docTree() *cobra.Command {
	e := &cli{}
	root := e.tree()
	e.flags.add(root)
	var all func(*cobra.Command)
	all = func(c *cobra.Command) {
		c.DisableAutoGenTag = true
		c.Short = strings.ReplaceAll(c.Short, "<", `\<`)
		c.Long = strings.ReplaceAll(c.Long, "<", `\<`)
		for _, s := range c.Commands() {
			all(s)
		}
	}
	all(root)
	return root
}

// docsManPages are the man pages made from pages of docs/.
var docsManPages = []struct{ page, name, section, what string }{
	{"configuration.md", "dromas.toml", "5", "the dromas config file"},
	{"output.md", "dromas-output", "7", "the lines, exit codes and refusal codes that dromas prints"},
}

// writeDocs writes the Markdown pages to markdown and the man pages to man,
// each where it is not empty: man/man1 a page for each command, and a page
// for each of docsManPages from its page in docs, each dated date.
func writeDocs(markdown, man, docs string, date time.Time) error {
	root := docTree()
	if markdown != "" {
		if err := os.MkdirAll(markdown, 0o755); err != nil { //nolint:gosec // pages anyone may read
			return err
		}
		if err := doc.GenMarkdownTree(root, markdown); err != nil {
			return err
		}
	}
	if man == "" {
		return nil
	}
	// A command's usage line and a flag's usage are Markdown prose in a man
	// page, and code in the Markdown pages, so their <placeholders> are
	// escaped for the man pages alone. A command's name is the first word of
	// its usage line, which has none.
	seen := map[*pflag.Flag]bool{}
	var escape func(*cobra.Command)
	escape = func(c *cobra.Command) {
		c.Use = strings.ReplaceAll(c.Use, "<", `\<`)
		flag := func(f *pflag.Flag) {
			if !seen[f] {
				seen[f] = true
				f.Usage = strings.ReplaceAll(f.Usage, "<", `\<`)
			}
		}
		c.Flags().VisitAll(flag)
		c.PersistentFlags().VisitAll(flag)
		for _, s := range c.Commands() {
			escape(s)
		}
	}
	escape(root)
	man1 := filepath.Join(man, "man1")
	if err := os.MkdirAll(man1, 0o755); err != nil { //nolint:gosec // pages anyone may read
		return err
	}
	h := &doc.GenManHeader{Source: "Dromas", Manual: "Dromas Manual", Date: &date}
	if err := doc.GenManTree(root, h, man1); err != nil {
		return err
	}
	for _, p := range docsManPages {
		src, err := os.ReadFile(filepath.Join(docs, p.page)) //nolint:gosec // a page of this repo, named by the build
		if err != nil {
			return err
		}
		page, err := docsManPage(src, p.name, p.section, p.what, date)
		if err != nil {
			return fmt.Errorf("%s: %w", p.page, err)
		}
		dir := filepath.Join(man, "man"+p.section)
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // pages anyone may read
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, p.name+"."+p.section), page, 0o644); err != nil { //nolint:gosec // a page anyone may read
			return err
		}
	}
	return nil
}

// A link to another page of docs/, which a man page gives as its page on
// dromas.dev.
var docsLink = regexp.MustCompile(`\]\(([a-z][a-z0-9/-]*)\.md(#[a-z0-9-]+)?\)`)

// docsManPage is the man page name(section), which says what it is: a page
// of docs/, src, as go-md2man renders it, with a man page's title, NAME and
// DESCRIPTION in place of the page's own title, each link to another page of
// docs/ given as that page on dromas.dev, and a SEE ALSO of dromas(1) and the
// other docsManPages.
func docsManPage(src []byte, name, section, what string, date time.Time) ([]byte, error) {
	title, body, ok := bytes.Cut(src, []byte("\n"))
	if !ok || !bytes.HasPrefix(title, []byte("# ")) {
		return nil, fmt.Errorf("the page does not open with its title")
	}
	body = docsLink.ReplaceAll(body, []byte("](https://dromas.dev/docs/$1/$2)"))
	see := []string{"**dromas**(1)"}
	for _, p := range docsManPages {
		if p.name != name {
			see = append(see, "**"+p.name+"**("+p.section+")")
		}
	}
	head := fmt.Sprintf("# %s %s %q \"Dromas\" \"Dromas Manual\"\n\n# NAME\n\n%s - %s\n\n# DESCRIPTION\n",
		strings.ToUpper(name), section, date.UTC().Format("Jan 2006"), name, what) //nolint:forbidigo // a man page's title, never a protocol token
	page := append([]byte(head), body...)
	page = append(page, "\n# SEE ALSO\n\n"+strings.Join(see, ", ")+"\n"...)
	return md2man.Render(page), nil
}
