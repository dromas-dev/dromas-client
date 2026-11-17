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
	var markdown, man, config string
	c := command("gen-docs", "write the command reference and the man pages",
		`Write cobra's Markdown of the command tree to --markdown-dir, and its man pages
to --man-dir: man1/ a page for each command, and man5/dromas.toml.5, the config
file's reference made from --config-page. A page is dated by SOURCE_DATE_EPOCH
where it is set, so a rebuild writes the same bytes.`,
		nargs(0, 0), func(*cobra.Command, []string) error {
			date := time.Now()
			if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
				n, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return fmt.Errorf("SOURCE_DATE_EPOCH: %w", err)
				}
				date = time.Unix(n, 0)
			}
			return writeDocs(markdown, man, config, date)
		})
	c.Hidden = true
	c.Flags().StringVar(&markdown, "markdown-dir", "", "the directory for the Markdown pages (none: not written)")
	c.Flags().StringVar(&man, "man-dir", "", "the directory for the man pages (none: not written)")
	c.Flags().StringVar(&config, "config-page", filepath.Join("docs", "configuration.md"), "the config file's reference, in Markdown")
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

// writeDocs writes the Markdown pages to markdown and the man pages to man,
// each where it is not empty: man/man1 a page for each command, and
// man/man5/dromas.toml.5 from the Markdown page config, each dated date.
func writeDocs(markdown, man, config string, date time.Time) error {
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
	man1, man5 := filepath.Join(man, "man1"), filepath.Join(man, "man5")
	for _, d := range []string{man1, man5} {
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // pages anyone may read
			return err
		}
	}
	// A flag's usage is Markdown prose in a man page, and a code block in the
	// Markdown pages, so its <base-dir> is escaped for the man pages alone.
	seen := map[*pflag.Flag]bool{}
	var flags func(*cobra.Command)
	flags = func(c *cobra.Command) {
		escape := func(f *pflag.Flag) {
			if !seen[f] {
				seen[f] = true
				f.Usage = strings.ReplaceAll(f.Usage, "<", `\<`)
			}
		}
		c.Flags().VisitAll(escape)
		c.PersistentFlags().VisitAll(escape)
		for _, s := range c.Commands() {
			flags(s)
		}
	}
	flags(root)
	h := &doc.GenManHeader{Source: "Dromas", Manual: "Dromas Manual", Date: &date}
	if err := doc.GenManTree(root, h, man1); err != nil {
		return err
	}
	src, err := os.ReadFile(config) //nolint:gosec // a page of this repo, named by the build
	if err != nil {
		return err
	}
	page, err := configManPage(src, date)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(man5, "dromas.toml.5"), page, 0o644) //nolint:gosec // a page anyone may read
}

// A link to another page of docs/, which a man page gives as its page on
// dromas.dev.
var docsLink = regexp.MustCompile(`\]\(([a-z][a-z0-9/-]*)\.md(#[a-z0-9-]+)?\)`)

// configManPage is dromas.toml(5): docs/configuration.md, src, as go-md2man
// renders it, with a man page's title, NAME and DESCRIPTION in place of the
// page's own title, and each link to another page of docs/ given as that
// page on dromas.dev.
func configManPage(src []byte, date time.Time) ([]byte, error) {
	title, body, ok := bytes.Cut(src, []byte("\n"))
	if !ok || !bytes.HasPrefix(title, []byte("# ")) {
		return nil, fmt.Errorf("the config page does not open with its title")
	}
	body = docsLink.ReplaceAll(body, []byte("](https://dromas.dev/docs/$1/$2)"))
	head := fmt.Sprintf("# DROMAS.TOML 5 %q \"Dromas\" \"Dromas Manual\"\n\n# NAME\n\ndromas.toml - the dromas config file\n\n# DESCRIPTION\n", date.UTC().Format("Jan 2006"))
	return md2man.Render(append([]byte(head), body...)), nil
}
