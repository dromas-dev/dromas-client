package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every page and comment says what holds now, never how it came to be
// (AGENTS.md): a date is allowed only where it says when a source was read,
// a measurement measured, a review made or an outside document dated, and
// in the tag URIs of the protocol's error types, whose date is part of the
// URI; a time written out (2006-01-02T15:04:05Z) is an example of a format,
// not a date of anything.
// PRECEDENTS.md dates its claims by its own rule (AGENTS.md).
func TestEveryPageSaysWhatHoldsNow(t *testing.T) {
	date := regexp.MustCompile(`20[0-9]{2}-[01][0-9]-[0-3][0-9]`)
	allowed := regexp.MustCompile(`(?i)(\b(read|measured|review|dated) |tag:dromas\.dev,)20[0-9]{2}-[01][0-9]-[0-3][0-9]|20[0-9]{2}-[01][0-9]-[0-3][0-9]T[0-9]{2}:`)
	root := filepath.Join("..", "..")
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (name == "testdata" || name == "dist" || strings.HasPrefix(name, ".") && name != ".github") {
				return filepath.SkipDir
			}
			return nil
		}
		// The part of each line a reader reads as the page's or a comment's words.
		var words func(string) string
		switch ext := filepath.Ext(name); {
		case ext == ".md":
			if rel, _ := filepath.Rel(root, p); rel == "PRECEDENTS.md" {
				return nil
			}
			words = func(l string) string { return l }
		case ext == ".go":
			words = func(l string) string { _, c, _ := strings.Cut(l, "//"); return c }
		case ext == ".sh" || ext == ".yml" || ext == ".yaml" || name == "Dockerfile":
			words = func(l string) string { _, c, _ := strings.Cut(l, "#"); return c }
		default:
			return nil
		}
		data, err := os.ReadFile(p) //nolint:gosec // a file of this repo
		if err != nil {
			return err
		}
		n++
		for i, l := range strings.Split(string(data), "\n") {
			w := words(l)
			if date.MatchString(allowed.ReplaceAllString(w, "")) {
				t.Errorf("%s:%d dates something other than a source, a measurement or a review: %s", p, i+1, strings.TrimSpace(l))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 20 {
		t.Fatalf("read %d files from %s: the walk is broken", n, root)
	}
}

// plainPages are the pages AGENTS.md § How a page is written holds by
// measure. A page joins the list once it is rewritten by those rules.
var plainPages = []string{"PROTOCOL.md"}

// The measurable rules of AGENTS.md § How a page is written. Comparable specs
// have 3 to 10 "it", "its" or "itself" per 1,000 words (C2SP age, RFC 8555,
// RFC 9180, Signal's X3DH and NIP-44, measured 2026-11-02).
const (
	maxSentenceWords       = 40
	maxPronounsPerThousand = 15
)

// retiredWords are words a listed page does not use, each with the plain word
// that replaces it.
var retiredWords = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)\b(take|takes|taken|taking|took)\b`), "accept, require, read or use: the verb meant"},
	{regexp.MustCompile(`(?i)\bfor good\b`), "permanently"},
	{regexp.MustCompile(`(?i)\bnone held\b`), "no revocation shown"},
	{regexp.MustCompile(`(?i)\bbreaks (its|the|their) form\b`), "is malformed"},
}

// Each page plainPages lists reads plainly by the rules a count can check: no
// sentence of its prose over maxSentenceWords, at most maxPronounsPerThousand
// "it", "its" or "itself" in each 1,000 words, and none of retiredWords. A
// fenced block, an indented code line, a table, a heading and a section headed
// References are not prose; retiredWords apply to tables too.
func TestThePagesReadPlainly(t *testing.T) {
	t.Parallel()
	word := regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9'’\-]*`)
	pronoun := regexp.MustCompile(`(?i)\b(it|its|itself)\b`)
	for _, page := range plainPages {
		data, err := os.ReadFile(filepath.Join("..", "..", page)) //nolint:gosec // a page of this repo
		if err != nil {
			t.Fatal(err)
		}
		sentences := proseSentences(string(data))
		if len(sentences) < 100 {
			t.Fatalf("%s: read %d sentences: the reading is broken", page, len(sentences))
		}
		words, pronouns := 0, 0
		for _, s := range sentences {
			n := len(word.FindAllString(s.text, -1))
			words += n
			pronouns += len(pronoun.FindAllString(s.text, -1))
			// RFC 8174 §2 gives this sentence's words, and a spec quotes it whole.
			if n > maxSentenceWords && !strings.HasPrefix(s.text, `The key words "MUST"`) {
				t.Errorf("%s:%d has a sentence of %d words, over %d: split it: %s", page, s.line, n, maxSentenceWords, s.text)
			}
		}
		if 1000*pronouns > maxPronounsPerThousand*words {
			t.Errorf("%s has %d \"it\", \"its\" or \"itself\" in %d words of prose, over %d per 1,000: name the noun instead", page, pronouns, words, maxPronounsPerThousand)
		}
		inFence := false
		for i, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			text := inlineCode.ReplaceAllString(l, "")
			for _, r := range retiredWords {
				if m := r.re.FindString(text); m != "" {
					t.Errorf("%s:%d uses %q: write %s", page, i+1, m, r.with)
				}
			}
		}
	}
}

var (
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	listMarker = regexp.MustCompile(`^([-*]|\d+\.)\s+`)
	// A sentence ends at ".", "!" or "?" before a space and a capital, an
	// opening bracket or a quote.
	sentenceEnd = regexp.MustCompile(`[.!?]\s+["(\[A-Z]`)
)

// sentence is one sentence of a page's prose, and the line it starts on.
type sentence struct {
	line int
	text string
}

// proseSentences returns the sentences of a page's prose: each list item and
// each paragraph split at its sentence ends, inline code read as one word and
// bold marks dropped. A fenced block, an indented code line, a table row, a
// heading, a blank line and a section headed References are not prose.
func proseSentences(page string) []sentence {
	var out []sentence
	var para []string
	start := 0
	flush := func() {
		text := strings.Join(para, " ")
		para = nil
		for text != "" {
			cut := len(text)
			if m := sentenceEnd.FindStringIndex(text); m != nil {
				cut = m[0] + 1
			}
			if s := strings.TrimSpace(text[:cut]); len(strings.Fields(s)) >= 3 {
				out = append(out, sentence{start, s})
			}
			text = strings.TrimSpace(text[cut:])
		}
	}
	inFence, references := false, false
	for i, l := range strings.Split(page, "\n") {
		trim := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(trim, "```"):
			inFence = !inFence
			flush()
			continue
		case inFence:
			continue
		case strings.HasPrefix(trim, "#"):
			flush()
			references = strings.Contains(trim, "References")
			continue
		case references, trim == "", strings.HasPrefix(trim, "|"),
			strings.HasPrefix(l, "    ") && !listMarker.MatchString(trim):
			flush()
			continue
		}
		if m := listMarker.FindStringIndex(trim); m != nil {
			flush()
			trim = trim[m[1]:]
		}
		if len(para) == 0 {
			start = i + 1
		}
		para = append(para, strings.ReplaceAll(inlineCode.ReplaceAllString(trim, "X"), "**", ""))
	}
	flush()
	return out
}
