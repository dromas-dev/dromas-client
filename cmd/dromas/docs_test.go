package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/spf13/cobra"
)

var updateDocs = flag.Bool("update", false, "write docs/commands from the command tree, and the Windows resource files from the manifest")

// commandsDir is the command reference, a page for each command.
var commandsDir = filepath.Join("..", "..", "docs", "commands")

// The command reference in docs/commands is cobra's own Markdown of the
// command tree (cobra/doc), a page for each command: its use, its help and
// its flags. A change to a command, a flag or a help text changes a page,
// and this test fails until the pages are written again:
//
//	go test ./cmd/dromas -run TestTheCommandPagesAreTheHelp -update
func TestTheCommandPagesAreTheHelp(t *testing.T) {
	dir := t.TempDir()
	if err := writeDocs(dir, "", "", time.Time{}); err != nil {
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

// The man pages are the same tree's: a page in man1/ for each page of
// docs/commands, each with its usage line's and its help's placeholders, and
// a page for each of docsManPages, each with a man page's title, NAME and
// SEE ALSO, every heading of its page in docs/, and no link that only
// resolves in this repo; dromas.toml.5 has every key of docs/configuration.md.
// Each is dated by the date it is given, so a release that dates them by its
// commit rebuilds the same bytes.
func TestTheManPagesAreTheCommandsAndTheDocs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	date := time.Date(2000, time.January, 2, 3, 4, 5, 0, time.UTC)
	docs := filepath.Join("..", "..", "docs")
	if err := writeDocs("", dir, docs, date); err != nil {
		t.Fatal(err)
	}
	pages, err := os.ReadDir(commandsDir)
	if err != nil {
		t.Fatal(err)
	}
	man1, err := os.ReadDir(filepath.Join(dir, "man1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(man1) != len(pages) || len(pages) < 20 {
		t.Fatalf("%d man pages for %d command pages", len(man1), len(pages))
	}
	// A page as text, with roff's escapes of a hyphen and of a dot undone.
	plain := strings.NewReplacer(`\-`, "-", `\&`, "")
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a file this test wrote
		if err != nil {
			t.Fatal(err)
		}
		return plain.Replace(string(b))
	}
	for _, p := range pages {
		name := strings.ReplaceAll(strings.TrimSuffix(p.Name(), ".md"), "_", "-") + ".1"
		page := read(filepath.Join("man1", name))
		if !strings.Contains(page, "\n.TH \"") || !strings.Contains(page, `"Jan 2000" "Dromas" "Dromas Manual"`) {
			t.Errorf("man1/%s has no title of its date: %.120q", name, page)
		}
	}
	// A usage line's, a help's or a flag's <placeholder> is kept, never
	// dropped as an HTML tag.
	placeholder := regexp.MustCompile(`<[^>]+>`)
	var uses int
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if !c.Hidden {
			page := read(filepath.Join("man1", strings.ReplaceAll(c.CommandPath(), " ", "-")+".1"))
			for _, p := range placeholder.FindAllString(c.Use, -1) {
				uses++
				if !strings.Contains(page, p) {
					t.Errorf("%s.1 lacks its usage line's %s", strings.ReplaceAll(c.CommandPath(), " ", "-"), p)
				}
			}
		}
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk((&cli{}).tree())
	if uses < 5 {
		t.Fatalf("read %d placeholders from the usage lines: the reading is broken", uses)
	}
	for name, want := range map[string]string{"dromas-account-join.1": "dromas account join <", "dromas-send.1": "<base-dir>/identity"} {
		if page := read(filepath.Join("man1", name)); !strings.Contains(page, want) {
			t.Errorf("%s lacks %q", name, want)
		}
	}
	heading := regexp.MustCompile("(?m)^## (.+)$")
	// go-md2man writes a link's target between \[la] and \[ra].
	link := regexp.MustCompile(`\\\[la\]([^\\]+)\\\[ra\]`)
	for _, p := range docsManPages {
		name := filepath.Join("man"+p.section, p.name+"."+p.section)
		page := read(name)
		src, err := os.ReadFile(filepath.Join(docs, p.page)) //nolint:gosec // a page of this repo
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(page, "\n.TH "+strings.ToUpper(p.name)+" "+p.section+` "Jan 2000" "Dromas" "Dromas Manual"`+"\n") || !strings.Contains(page, ".SH NAME\n"+p.name+" - ") || !strings.Contains(page, ".SH SEE ALSO") {
			t.Errorf("%s has not a man page's title, NAME and SEE ALSO: %.300q", name, page)
		}
		for _, l := range link.FindAllStringSubmatch(page, -1) {
			if !strings.Contains(l[1], "://") {
				t.Errorf("%s links %s, which resolves only in this repo", name, l[1])
			}
		}
		see := page[max(strings.Index(page, ".SH SEE ALSO"), 0):]
		for _, o := range append(docsManPages, struct{ page, name, section, what string }{name: "dromas", section: "1"}) {
			if o.name != p.name && !strings.Contains(see, o.name+`\fP(`+o.section+")") {
				t.Errorf("%s's SEE ALSO lacks %s(%s)", name, o.name, o.section)
			}
		}
		hs := heading.FindAllStringSubmatch(string(src), -1)
		if len(hs) < 3 {
			t.Fatalf("read %d headings from docs/%s: the reading is broken", len(hs), p.page)
		}
		for _, h := range hs {
			if !strings.Contains(page, ".SH "+strings.ReplaceAll(h[1], "`", "")) && !strings.Contains(page, ".SH "+h[1]) {
				t.Errorf("%s lacks the section %q", name, h[1])
			}
		}
	}
	toml := read(filepath.Join("man5", "dromas.toml.5"))
	src, err := os.ReadFile(filepath.Join(docs, "configuration.md")) //nolint:gosec // a page of this repo
	if err != nil {
		t.Fatal(err)
	}
	keys := regexp.MustCompile("(?m)^### `([a-z.-]+)`$").FindAllStringSubmatch(string(src), -1)
	if len(keys) < 20 {
		t.Fatalf("read %d keys from docs/configuration.md: the reading is broken", len(keys))
	}
	for _, k := range keys {
		if !strings.Contains(toml, ".SS \\fB"+k[1]+"\\fR") {
			t.Errorf("dromas.toml.5 lacks the key %s", k[1])
		}
	}
	// The same date writes the same bytes.
	again := t.TempDir()
	if err := writeDocs("", again, docs, date); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Join("man1", "dromas.1"), filepath.Join("man1", "dromas-send.1"), filepath.Join("man5", "dromas.toml.5"), filepath.Join("man7", "dromas-output.7")} {
		b, err := os.ReadFile(filepath.Join(again, name)) //nolint:gosec // a file this test wrote
		if err != nil || plain.Replace(string(b)) != read(name) {
			t.Errorf("%s differs from one run to the next: %v", name, err)
		}
	}
}

// Every relative link in the README and docs/ names a file or directory of
// this repo, so each link resolves on GitHub, and on dromas.dev, which shows
// these pages at the paths they have here. A link's #fragment names a heading
// of the page it links, by the id GitHub gives the heading, which Hugo gives
// it too.
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
			dest, frag, _ := strings.Cut(m[1], "#")
			if strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") {
				continue
			}
			target := page
			if dest != "" {
				n++
				target = filepath.Join(filepath.Dir(page), filepath.FromSlash(dest))
				fi, err := os.Stat(target)
				if err != nil {
					t.Errorf("%s links %s, which is not a file of this repo", page, m[1])
					continue
				}
				if fi.IsDir() {
					target = filepath.Join(target, "README.md")
				}
			}
			if frag == "" || !strings.HasSuffix(target, ".md") {
				continue
			}
			to, err := os.ReadFile(target) //nolint:gosec // a page of this repo
			if err != nil {
				t.Fatal(err)
			}
			if !headingIDs(string(to))[frag] {
				t.Errorf("%s links %s, and %s has no heading #%s", page, m[1], target, frag)
			}
		}
	}
	if n < 50 {
		t.Fatalf("read %d relative links: the reading is broken", n)
	}
}

// inlineCode is a Markdown code span.
var inlineCode = regexp.MustCompile("`[^`\n]*`")

// mdLink is a Markdown link, its text first.
var mdLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// headingIDs is the id of each heading of a Markdown page, as GitHub makes one
// and Hugo's default (autoHeadingIDType "github") does: the heading's text in
// lower case, each space a hyphen, every character but a letter, a digit, a
// hyphen and an underscore dropped, and a second heading of the same id given
// -1, a third -2.
func headingIDs(page string) map[string]bool {
	ids, seen := map[string]bool{}, map[string]int{}
	fenced := false
	for l := range strings.SplitSeq(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
			continue
		}
		text, ok := strings.CutPrefix(strings.TrimLeft(l, "#"), " ")
		if fenced || !strings.HasPrefix(l, "#") || !ok {
			continue
		}
		var b strings.Builder
		for _, r := range strings.ToLower(mdLink.ReplaceAllString(strings.TrimSpace(text), "$1")) {
			switch {
			case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
				b.WriteRune(r)
			case r == ' ':
				b.WriteRune('-')
			}
		}
		id := b.String()
		if k := seen[id]; k > 0 {
			ids[fmt.Sprintf("%s-%d", id, k)] = true
		} else {
			ids[id] = true
		}
		seen[id]++
	}
	return ids
}

// Each install page but the index and verify.md ends with the same
// quickstart, so a reader who lands on one page has the whole path: check the
// install, create a mailbox, send to the echo mailbox at the address the code
// carries and read its reply, then the user guide.
func TestEachInstallPageEndsWithTheQuickstart(t *testing.T) {
	link := client.DefaultBase + "/to/" + client.EchoAddress
	dir := filepath.Join("..", "..", "docs", "install")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range entries {
		if f.Name() == "README.md" || f.Name() == "verify.md" || !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		n++
		b, err := os.ReadFile(filepath.Join(dir, f.Name())) //nolint:gosec // a page of this repo
		if err != nil {
			t.Fatal(err)
		}
		page := string(b)
		quick := strings.Index(page, "\n## Quickstart\n")
		next := strings.Index(page, "\n## Next steps\n")
		if quick < 0 || next < quick {
			t.Errorf("docs/install/%s does not end with a Quickstart, then Next steps", f.Name())
			continue
		}
		for _, want := range []string{"   dromas version\n", "   dromas init\n", "   echo hello | dromas send " + link + "\n", "   dromas recv --wait 3m\n"} {
			if !strings.Contains(page[quick:next], want) {
				t.Errorf("docs/install/%s's quickstart has no line %q", f.Name(), strings.TrimSpace(want))
			}
		}
		if !strings.Contains(page[next:], "](../guide.md)") {
			t.Errorf("docs/install/%s's next steps do not link the user guide", f.Name())
		}
	}
	if n < 9 {
		t.Fatalf("read %d install pages: the reading is broken", n)
	}
}

// docs/configuration.md has an entry, a "### `key`" heading, for each key a
// config file may hold (fileConfig) and for no other key, gives the file
// init writes, and names each environment variable the command reads.
func TestTheConfigurationReferenceNamesEveryKey(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	entries := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^### `([^`]+)`$").FindAllStringSubmatch(page, -1) {
		entries[m[1]] = true
	}
	keys := map[string]bool{}
	if err := eachKey(reflect.ValueOf(&fileConfig{}).Elem(), nil, func(path []string, v reflect.Value) error {
		if v.Kind() != reflect.Struct {
			keys[strings.Join(path, ".")] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(keys) < 20 {
		t.Fatalf("read %d keys of fileConfig: the reading is broken", len(keys))
	}
	for k := range keys {
		if !entries[k] {
			t.Errorf("docs/configuration.md has no entry for %s", k)
		}
	}
	for k := range entries {
		if !keys[k] {
			t.Errorf("docs/configuration.md has an entry for %s, which no config file may hold", k)
		}
	}

	// The page gives the file init writes on Linux and macOS.
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	if want := "```toml\n" + templateFor("default", &globals{}) + "```\n"; !strings.Contains(page, want) {
		t.Errorf("docs/configuration.md does not give the file init writes:\n%s", want)
	}

	read, named := map[string]bool{}, map[string]bool{}
	name := regexp.MustCompile(`"(DROMAS_[A-Z_]+)"`)
	for _, dir := range []string{".", filepath.Join("..", "..", "client")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f) //nolint:gosec // a source file of this repo
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range name.FindAllStringSubmatch(string(src), -1) {
				// The tests' own hooks, and the count agent add hands the askpass
				// helper it runs, are no reader's to set.
				if !strings.HasPrefix(m[1], "DROMAS_TEST_") && m[1] != triesEnv {
					read[m[1]] = true
				}
			}
		}
	}
	for _, m := range regexp.MustCompile("`(DROMAS_[A-Z_]+)`").FindAllStringSubmatch(page, -1) {
		named[m[1]] = true
	}
	if len(read) < 10 {
		t.Fatalf("read %d environment variables: the reading is broken", len(read))
	}
	for v := range read {
		if !named[v] {
			t.Errorf("docs/configuration.md does not name $%s", v)
		}
	}
	for v := range named {
		if !read[v] {
			t.Errorf("docs/configuration.md names $%s, which the command does not read", v)
		}
	}
}

// testName is a test or fuzz test named in backticks.
var testName = regexp.MustCompile("`((?:Test|Fuzz)[A-Za-z0-9_]+)`")

// testsOf maps each test and fuzz test of the repo at root to the files,
// relative to root, that define it.
func testsOf(t *testing.T, root string) map[string][]string {
	t.Helper()
	def := regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)[A-Za-z0-9_]+)\(`)
	tests := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // a file of this repo
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		for _, m := range def.FindAllStringSubmatch(string(b), -1) {
			tests[m[1]] = append(tests[m[1]], filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tests) < 100 {
		t.Fatalf("found %d tests under %s: the walk is broken", len(tests), root)
	}
	return tests
}

// maxClaimWords is the longest a claim in SECURITY.md's table may run: one
// sentence, whose whole rule is the PROTOCOL.md section or the code it names.
const maxClaimWords = 35

// Each claim in SECURITY.md § Checking the claims yourself is one sentence of
// at most maxClaimWords words, and its "Held by" cell names the tests that
// hold the claim or says "no test". Each path the cell names is a file or
// directory of this repo, and each test it names is a test of this repo,
// defined in the test file the cell names it under.
func TestEachSecurityClaimIsOneSentence(t *testing.T) {
	root := filepath.Join("..", "..")
	tests := testsOf(t, root)
	b, err := os.ReadFile(filepath.Join(root, "SECURITY.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(b), "\n## Checking the claims yourself\n")
	if !ok {
		t.Fatal("SECURITY.md has no § Checking the claims yourself")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	word := regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9'’\-.+]*`)
	stop := regexp.MustCompile(`[.!?]["”]?\s`)
	path := regexp.MustCompile("`([^` ]+)`")
	n := 0
	for l := range strings.SplitSeq(section, "\n") {
		if !strings.HasPrefix(l, "| **") {
			continue
		}
		n++
		cells := strings.Split(strings.Trim(l, "| "), " | ")
		if len(cells) != 3 {
			t.Errorf("a claim's row has %d cells, not 3: %.60s", len(cells), l)
			continue
		}
		claim := mdLink.ReplaceAllString(cells[0], "$1")
		if w := len(word.FindAllString(claim, -1)); w > maxClaimWords {
			t.Errorf("a claim runs %d words, over %d: %.60s", w, maxClaimWords, claim)
		}
		if stop.MatchString(claim) {
			t.Errorf("a claim is more than one sentence: %.60s", claim)
		}
		held := cells[2]
		names := testName.FindAllStringSubmatch(held, -1)
		if len(names) == 0 && !strings.Contains(held, "no test") {
			t.Errorf("a claim names no test and does not say \"no test\": %.60s", claim)
		}
		for _, m := range names {
			if len(tests[m[1]]) == 0 {
				t.Errorf("a claim is held by %s, which is no test of this repo: %.60s", m[1], claim)
			}
		}
		for _, part := range strings.Split(held, "; ") {
			file, list, ok := strings.Cut(part, "`: ")
			if !ok || !strings.HasSuffix(file, "_test.go") {
				continue
			}
			file = strings.TrimPrefix(file, "`")
			for _, m := range testName.FindAllStringSubmatch(list, -1) {
				if len(tests[m[1]]) > 0 && !slices.Contains(tests[m[1]], file) {
					t.Errorf("a claim names %s under %s, which does not define it: %.60s", m[1], file, claim)
				}
			}
		}
		for _, m := range path.FindAllStringSubmatch(cells[2], -1) {
			p := m[1]
			if !strings.Contains(p, "/") || strings.Contains(p, "://") {
				continue
			}
			if found, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(p, "/")))); err != nil || len(found) == 0 {
				t.Errorf("a claim is held by %s, which is not in this repo: %.60s", p, claim)
			}
		}
	}
	if n < 10 {
		t.Fatalf("read %d claims: the reading is broken", n)
	}
}
