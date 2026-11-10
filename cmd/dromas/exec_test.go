package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
)

// callbackWord, as a copy of this test binary's first argument, makes the copy
// a callback: no shell runs, and a test holds --exec on every system Go runs on.
const callbackWord = "dromas-test-callback"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == callbackWord {
		os.Exit(callbackMain(os.Args[2], os.Args[3], os.Args[4:]))
	}
	// Run by ssh-add as agent add's askpass program, as the binary is.
	if status, ok := countedAskpassMain(); ok {
		os.Exit(status)
	}
	// Run as an askpass program, as ssh-add and the client run one, with the
	// prompt as its one argument: it prints the passphrase the test set, and
	// notes the run in askpassRunsEnv's file where that is set.
	if pass, ok := os.LookupEnv(askpassEnv); ok && len(os.Args) == 2 && (strings.HasPrefix(os.Args[1], "Enter passphrase") || strings.HasPrefix(os.Args[1], "Bad passphrase")) {
		if runs := os.Getenv(askpassRunsEnv); runs != "" {
			f, err := os.OpenFile(runs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the test's own file
			if err != nil {
				os.Exit(1)
			}
			_, _ = f.WriteString(os.Args[1] + "\n")
			_ = f.Close()
		}
		fmt.Println(pass)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// callbackMain is the callback: it appends what it was given to the file record,
// in the form the mode asks for, and exits as the mode asks.
func callbackMain(record, mode string, args []string) int {
	note := func(s string) {
		f, err := os.OpenFile(record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the test's own file
		if err != nil {
			panic(err)
		}
		defer f.Close() //nolint:errcheck // a test's
		if _, err := f.WriteString(s); err != nil {
			panic(err)
		}
	}
	runs := func() int {
		raw, _ := os.ReadFile(record) //nolint:gosec // the test's own file
		return bytes.Count(raw, []byte{'\n'})
	}
	switch mode {
	case "noread": // never reads its stdin
		note("ran\n")
		return 0
	case "hang":
		note("ran\n")
		time.Sleep(time.Hour)
		return 0
	case "kill":
		note("ran\n")
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			panic(err)
		}
		_ = p.Kill()
		time.Sleep(time.Hour)
		return 0
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	switch mode {
	case "exit":
		note(string(stdin))
		n, err := strconv.Atoi(args[0])
		if err != nil {
			panic(err)
		}
		return n
	case "fail-first":
		first := runs() == 0
		note(string(stdin))
		if first {
			return 1
		}
		return 0
	case "args":
		note(fmt.Sprintf("%q\n", args))
		return 0
	case "in-turn":
		note("begin " + string(stdin))
		time.Sleep(100 * time.Millisecond)
		note("end " + string(stdin))
		return 0
	case "lock-free":
		// The message is in inbox/, and the mailbox's lock is free for a command.
		b, err := client.Open(args[0], client.Options{})
		if err != nil {
			panic(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ms, err := b.Inbox(ctx)
		if err != nil {
			note(fmt.Sprintf("inbox: %v\n", err))
			return 1
		}
		if err := b.SetAlias(ctx, "from-the-callback", b.Address()); err != nil {
			note(fmt.Sprintf("alias: %v\n", err))
			return 1
		}
		for _, m := range ms {
			if bytes.Contains(stdin, []byte(m.ID)) {
				note("kept, and the lock is free\n")
				return 0
			}
		}
		note("not in inbox/\n")
		return 1
	case "noisy":
		// All it read, and more, to each of its outputs.
		for _, out := range []*os.File{os.Stdout, os.Stderr} {
			_, _ = out.Write(stdin)
			_, _ = out.WriteString(strings.Repeat("NOISE ", 1<<16))
		}
		note("ran\n")
		return 1
	}
	panic("no mode " + mode)
}

// callbackEnv is a recvEnv whose recv runs a callback, and the file the callback
// records in.
type callbackEnv struct {
	*recvEnv
	record string
}

func newCallback(t *testing.T) *callbackEnv {
	t.Helper()
	// The tries of one message are a test's business, and their wait is not.
	was := execDelay
	execDelay = time.Millisecond
	t.Cleanup(func() { execDelay = was })
	return &callbackEnv{recvEnv: newRecvEnv(t), record: filepath.Join(t.TempDir(), "record")}
}

// exec is the flags and arguments that run the callback in mode.
func (c *callbackEnv) exec(mode string, args ...string) []string {
	return append([]string{"--exec", os.Args[0], "--", callbackWord, c.record, mode}, args...)
}

// recorded is what the callback recorded.
func (c *callbackEnv) recorded() string {
	c.t.Helper()
	raw, err := os.ReadFile(c.record)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		c.t.Fatal(err)
	}
	return string(raw)
}

// The callback gets recv's line for the message on stdin, the line recv printed,
// and its arguments one for one: no shell splits or expands them.
func TestExecGetsTheLineAndItsArgumentsAsGiven(t *testing.T) {
	c := newCallback(t)
	c.send(`a "quoted" body; $(touch pwned) and 'more'`)
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "0")...)...)
	if err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if got := c.recorded(); got != stdout || !strings.HasPrefix(got, `{"type":"message"`) {
		t.Fatalf("the callback read %q, recv printed %q", got, stdout)
	}
	weird := []string{"two words", `$(touch pwned)`, `"quoted"`, `back\slash`, `*`, ``, `;`, `--help`}
	c = newCallback(t)
	c.send("the arguments")
	if _, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("args", weird...)...)...); err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if got, want := c.recorded(), fmt.Sprintf("%q\n", weird); got != want {
		t.Fatalf("the callback got %s, want %s", got, want)
	}
}

// With --headers recv prints the message_headers line, with none of the text
// the sender chose, and the callback still gets the message line: so a service
// whose stdout is a log logs who wrote and when, and its callback reads the
// message.
func TestExecWithHeadersGetsTheMessageLineAndPrintsTheHeaders(t *testing.T) {
	c := newCallback(t)
	c.send("the words of the sender")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s", "--headers"}, c.exec("exit", "0")...)...)
	if err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if !strings.HasPrefix(stdout, `{"type":"message_headers"`) || strings.Contains(stdout, "the words") {
		t.Fatalf("recv --headers printed %q", stdout)
	}
	if got := c.recorded(); !strings.HasPrefix(got, `{"type":"message"`) || !strings.Contains(got, "the words of the sender") {
		t.Fatalf("the callback read %q, want the message line", got)
	}
}

// A message held from a sender the mailbox has not allowed runs no callback: recv
// prints none of its text either.
func TestExecRunsNothingForAHeldMessage(t *testing.T) {
	c := newCallback(t)
	stranger, err := client.Init(context.Background(), t.TempDir(), client.Options{Base: c.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stranger.Send(context.Background(), c.b.Address(), "from a stranger"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "0")...)...)
	if err != nil || len(ofType(t, stdout, "held")) != 1 || c.recorded() != "" {
		t.Fatalf("recv: %v, %s %s; the callback recorded %q", err, stdout, stderr, c.recorded())
	}
}

// Callbacks run one at a time, in the order recv delivers.
func TestExecRunsOneAtATimeInDeliveryOrder(t *testing.T) {
	c := newCallback(t)
	c.send("one", "two", "three")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("in-turn")...)...)
	if err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	var want strings.Builder
	for l := range strings.Lines(stdout) {
		fmt.Fprintf(&want, "begin %send %s", l, l)
	}
	if got := c.recorded(); got != want.String() {
		t.Fatalf("the callback recorded %s, want %s", got, want.String())
	}
}

// The message is in inbox/ before its callback runs, and the mailbox's lock is
// free while it does: a callback may run a command of the mailbox.
func TestExecRunsWithTheMessageKeptAndTheLockFree(t *testing.T) {
	c := newCallback(t)
	c.send("kept first")
	if _, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("lock-free", c.b.Dir())...)...); err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if got := c.recorded(); got != "kept, and the lock is free\n" {
		t.Fatalf("the callback says %q", got)
	}
}

// A callback that fails is tried three times at most, each failed try one line
// naming the message, the try and the exit status. Running out of tries is one
// more line, recv goes on to the next message, its exit status is 3, status
// lists it, and nothing tries it again.
func TestExecIsTriedThreeTimesAndGivesUp(t *testing.T) {
	c := newCallback(t)
	c.send("fails", "fails too")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "7")...)...)
	if status := report(err, io.Discard); status != 3 {
		t.Fatalf("recv: exit %d, %v; %s", status, err, stderr)
	}
	messages := ofType(t, stdout, "message")
	failed, gaveUp := ofType(t, stdout, "exec_attempt_failed"), ofType(t, stdout, "exec_failed")
	if len(messages) != 2 || len(failed) != 6 || len(gaveUp) != 2 {
		t.Fatalf("recv printed %s", stdout)
	}
	for i, l := range failed {
		id := messages[i/3]["id"]
		if l["id"] != id || l["attempt"] != jsonInt(i%3+1) || l["exit_status"] != jsonInt(7) || l["signal"] != nil {
			t.Errorf("failed try %d: %v", i, l)
		}
		if gaveUp[i/3]["id"] != id || gaveUp[i/3]["attempts"] != jsonInt(3) {
			t.Errorf("gave up: %v", gaveUp[i/3])
		}
	}
	if got := strings.Count(c.recorded(), "\n"); got != 6 {
		t.Fatalf("%d runs of the callback, want 3 for each message", got)
	}
	st, err := c.b.Status(context.Background())
	if err != nil || len(st.GaveUp) != 2 || len(st.Attention) == 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
	if _, _, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "7")...)...); err != nil {
		t.Fatalf("the next recv: %v", err)
	}
	if got := strings.Count(c.recorded(), "\n"); got != 6 {
		t.Fatalf("%d runs of the callback after the next recv: nothing tries a message again", got)
	}
}

// A message whose callback gave up keeps dromas status at exit status 3 while
// inbox/ keeps its file, through a later recv --exec. dromas inbox clear-failed
// takes no id, clears every one in one "failed_cleared" line with their count,
// and status exits 0; a callback that gives up after it raises status again.
func TestInboxClearFailedClearsEveryCallbackThatGaveUpUntilTheNext(t *testing.T) {
	c := newCallback(t)
	ctx := context.Background()
	run := func(command string, args ...string) (string, int) {
		t.Helper()
		var out, errs bytes.Buffer
		err := do(ctx, command, args, strings.NewReader(""), &out, &errs)
		return out.String(), report(err, io.Discard)
	}
	status := func(want int, gaveUp ...string) {
		t.Helper()
		stdout, code := run("status")
		ls := ofType(t, stdout, "status")
		if code != want || len(ls) != 1 {
			t.Fatalf("status: exit %d, want %d: %s", code, want, stdout)
		}
		var got []string
		if ids, ok := ls[0]["failed"].([]any); ok {
			for _, id := range ids {
				got = append(got, fmt.Sprint(id))
			}
		}
		slices.Sort(got)
		slices.Sort(gaveUp)
		if !slices.Equal(got, gaveUp) {
			t.Fatalf("status lists %q as gave up, want %q: %s", got, gaveUp, stdout)
		}
	}
	cleared := func(want int) {
		t.Helper()
		stdout, code := run("inbox clear-failed")
		ls := printed(t, stdout)
		if code != 0 || len(ls) != 1 || ls[0]["type"] != "failed_cleared" || ls[0]["count"] != jsonInt(want) || len(ls[0]) != 2 {
			t.Fatalf("inbox clear-failed: exit %d, %s; want one failed_cleared line of count %d", code, stdout, want)
		}
	}
	c.send("fails", "fails too")
	stdout, _, err := c.recv(ctx, append([]string{"--wait", "0s"}, c.exec("exit", "100")...)...)
	if code := report(err, io.Discard); code != 3 {
		t.Fatalf("recv: exit %d, %s", code, stdout)
	}
	var ids []string
	for _, l := range ofType(t, stdout, "exec_failed") {
		ids = append(ids, fmt.Sprint(l["id"]))
	}
	if len(ids) != 2 {
		t.Fatalf("recv printed %s", stdout)
	}
	status(3, ids...)
	if _, _, err := c.recv(ctx, append([]string{"--wait", "0s"}, c.exec("exit", "0")...)...); err != nil {
		t.Fatalf("the next recv: %v", err)
	}
	status(3, ids...)
	if _, code := run("inbox clear-failed", ids[0]); code != 2 {
		t.Fatalf("inbox clear-failed <id>: exit %d, want 2: it takes no id", code)
	}
	status(3, ids...)
	cleared(2)
	status(0)
	if stdout, code := run("inbox list"); code != 0 || len(ofType(t, stdout, "message")) != 2 {
		t.Fatalf("inbox list after clear-failed: exit %d, %s", code, stdout)
	}
	cleared(0)
	c.send("fails again")
	stdout, _, err = c.recv(ctx, append([]string{"--wait", "0s"}, c.exec("exit", "100")...)...)
	again := ofType(t, stdout, "exec_failed")
	if code := report(err, io.Discard); code != 3 || len(again) != 1 {
		t.Fatalf("recv: exit %d, %s", code, stdout)
	}
	status(3, fmt.Sprint(again[0]["id"]))
	cleared(1)
	status(0)
}

// Exit status 100 says failed for good: no more tries.
func TestExecExit100IsPermanent(t *testing.T) {
	c := newCallback(t)
	c.send("fails for good")
	stdout, _, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "100")...)...)
	if status := report(err, io.Discard); status != 3 || len(ofType(t, stdout, "exec_attempt_failed")) != 1 || len(ofType(t, stdout, "exec_failed")) != 1 {
		t.Fatalf("recv: exit %d, %s", status, stdout)
	}
	if got := strings.Count(c.recorded(), "\n"); got != 1 {
		t.Fatalf("%d runs of the callback, want 1", got)
	}
	if st, err := c.b.Status(context.Background()); err != nil || len(st.GaveUp) != 1 {
		t.Fatalf("status: %+v, %v", st, err)
	}
}

// A callback that fails and then succeeds ends there, with the failed try said
// and nothing for status to list.
func TestExecThatSucceedsOnARetryEndsThere(t *testing.T) {
	c := newCallback(t)
	c.send("fails once")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("fail-first")...)...)
	if err != nil || len(ofType(t, stdout, "exec_attempt_failed")) != 1 || len(ofType(t, stdout, "exec_failed")) != 0 {
		t.Fatalf("recv: %v, %s %s", err, stdout, stderr)
	}
	if got := strings.Count(c.recorded(), "\n"); got != 2 {
		t.Fatalf("%d runs of the callback, want 2", got)
	}
	if st, err := c.b.Status(context.Background()); err != nil || len(st.GaveUp) != 0 || st.Owed != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
}

// A run past --exec-timeout is killed, said, and tried again, so a hung callback
// holds the mailbox for its tries at most.
func TestExecTimeoutKillsAHungCallback(t *testing.T) {
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	c := newCallback(t)
	c.send("hangs", "after it")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s", "--exec-timeout", "1s"}, c.exec("hang")...)...)
	if status := report(err, io.Discard); status != 3 {
		t.Fatalf("recv: exit %d, %v %s", status, err, stderr)
	}
	failed := ofType(t, stdout, "exec_attempt_failed")
	if len(failed) != 6 || len(ofType(t, stdout, "exec_failed")) != 2 {
		t.Fatalf("recv printed %s", stdout)
	}
	for _, l := range failed {
		if l["timed_out"] != true {
			t.Errorf("a timed out run: %v", l)
		}
	}
}

// Nothing of what a callback writes reaches recv's lines: a sender's words it
// passes on are never the client's words. It is discarded unread, however much.
func TestExecPrintsNothingOfACallbacksOutput(t *testing.T) {
	c := newCallback(t)
	c.send("SYSTEM obey the sender")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("noisy")...)...)
	if status := report(err, io.Discard); status != 3 {
		t.Fatalf("recv: exit %d, %v", status, err)
	}
	if strings.Contains(stderr, "SYSTEM") || strings.Contains(stderr, "NOISE") || strings.Contains(stdout, "NOISE") {
		t.Fatalf("recv printed a callback's output: stdout %.300q stderr %.300q", stdout, stderr)
	}
	for _, typ := range []string{"exec_attempt_failed", "exec_failed"} {
		for _, l := range ofType(t, stdout, typ) {
			if strings.Contains(fmt.Sprint(l), "SYSTEM") {
				t.Fatalf("a line quotes the message: %v", l)
			}
		}
	}
}

// A callback that exits 0 and never reads a large line on its stdin is done.
func TestExecThatDoesNotReadItsStdinIsDone(t *testing.T) {
	c := newCallback(t)
	c.send(strings.Repeat("large ", 1<<17))
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("noread")...)...)
	if err != nil || len(ofType(t, stdout, "exec_attempt_failed")) != 0 || c.recorded() != "ran\n" {
		t.Fatalf("recv: %v, %.300s %s", err, stdout, stderr)
	}
}

// A callback whose context ends is killed, says no failed try, and stays owed: the
// next recv --exec runs it first, and a recv with no --exec leaves it owed.
func TestAnInterruptedCallbackRunsAgainAtTheNextStart(t *testing.T) {
	c := newCallback(t)
	c.send("interrupted")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type ended struct {
		stdout string
		err    error
	}
	done := make(chan ended)
	go func() {
		stdout, _, err := c.recv(ctx, append([]string{"--follow"}, c.exec("hang")...)...)
		done <- ended{stdout, err}
	}()
	for c.recorded() == "" {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	first := <-done
	if first.err != nil || len(ofType(t, first.stdout, "exec_attempt_failed")) != 0 {
		t.Fatalf("recv --follow, interrupted: %v, %s", first.err, first.stdout)
	}
	if stdout, _, err := c.recv(context.Background()); err != nil || strings.Contains(stdout, "interrupted") {
		t.Fatalf("recv with no --exec: %v, %s", err, stdout)
	}
	if st, err := c.b.Status(context.Background()); err != nil || st.Owed != 1 {
		t.Fatalf("status: %+v, %v", st, err)
	}
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "0")...)...)
	if err != nil || !strings.Contains(stdout, "interrupted") {
		t.Fatalf("recv: %v, %s %s", err, stdout, stderr)
	}
	if !strings.Contains(c.recorded(), `"body":"interrupted"`) {
		t.Fatalf("the callback recorded %q", c.recorded())
	}
	if st, err := c.b.Status(context.Background()); err != nil || st.Owed != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
}

// A message owed its callback that inbox/ no longer keeps is said, and its
// callback does not run.
func TestExecSkipsAMessageNoLongerKept(t *testing.T) {
	c := newCallback(t)
	c.send("deleted by its owner")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error)
	go func() {
		_, _, err := c.recv(ctx, append([]string{"--follow"}, c.exec("hang")...)...)
		done <- err
	}()
	for c.recorded() == "" {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob(filepath.Join(c.b.Dir(), "mail", "inbox", "*.json"))
	if err != nil || len(names) != 1 {
		t.Fatalf("inbox/ holds %v, %v", names, err)
	}
	if err := os.Remove(names[0]); err != nil {
		t.Fatal(err)
	}
	before := c.recorded()
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "0")...)...)
	if skipped := ofType(t, stdout, "exec_skipped"); err != nil || len(skipped) != 1 || c.recorded() != before {
		t.Fatalf("recv: %v, %s %s; the callback recorded %q", err, stdout, stderr, c.recorded())
	}
}

// A failed write to stdout ends recv, once the drain's callbacks have run:
// each message is kept and handled whether or not its line was printed.
func TestAFailedWriteEndsARunOnceItsCallbacksRan(t *testing.T) {
	c := newCallback(t)
	c.send("one", "two")
	broke := errors.New("stdout is gone")
	var errs bytes.Buffer
	err := do(context.Background(), "recv", append([]string{"--follow"}, c.exec("exit", "0")...), strings.NewReader(""), failingWriter{broke}, &errs)
	if !errors.Is(err, broke) || strings.Count(c.recorded(), "\n") != 2 {
		t.Fatalf("recv: %v; the callback recorded %q", err, c.recorded())
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// A wrong --exec line runs nothing and is a usage error.
func TestExecUsageErrors(t *testing.T) {
	self := os.Args[0]
	c := newCallback(t)
	c.send("waits")
	for name, args := range map[string][]string{
		"--exec with neither --wait nor --follow": {"--exec", self},
		"--exec with no program":                  {"--wait", "0s", "--exec", ""},
		"a program that is not there":             {"--wait", "0s", "--exec", filepath.Join(t.TempDir(), "no-such-program")},
		"--exec-timeout of zero":                  {"--wait", "0s", "--exec", self, "--exec-timeout", "0s"},
		"--exec-timeout with no --exec":           {"--wait", "0s", "--exec-timeout", "1m"},
		"arguments with no --exec":                {"--wait", "0s", "--", "x"},
		"arguments before --":                     {"--wait", "0s", "--exec", self, "x", "--", "y"},
		"an argument with no --":                  {"x"},
	} {
		_, _, err := c.recv(context.Background(), args...)
		var wrong *usageError
		if !errors.As(err, &wrong) {
			t.Errorf("%s: %v, want a usage error", name, err)
		}
	}
	if n := c.srv.Count(c.b.Address()); n != 1 {
		t.Errorf("%d messages left on the server, want the one none of them took", n)
	}
}

// jsonInt is n as a printed line carries it, read back (printed).
func jsonInt(n int) any { return json.Number(strconv.Itoa(n)) }

// execConfig points the commands at a config file for c's mailbox whose [recv]
// holds exec, the callback in mode, and the lines more.
func (c *callbackEnv) execConfig(mode string, more ...string) {
	c.t.Helper()
	dir := c.b.Dir()
	list := []string{tomlString(os.Args[0]), tomlString(callbackWord), tomlString(c.record), tomlString(mode)}
	body := "base-dir = " + tomlString(dir) + "\nmail-dir = " + tomlString(filepath.Join(dir, "mail")) +
		"\n[recv]\nexec = [" + strings.Join(list, ", ") + "]\n" + strings.Join(more, "\n") + "\n"
	c.t.Setenv("DROMAS_CONFIG", writeConfig(c.t, body))
}

// The config file's [recv] exec is the callback of a recv that waits and names
// no --exec, so a service manager's unit names only the profile: its program,
// then its arguments, with its exec-timeout. A recv that does not wait runs
// nothing, and --exec on the command line takes its place.
func TestTheConfigsExecIsTheCallbackOfARecvThatWaits(t *testing.T) {
	c := newCallback(t)
	c.execConfig("args")
	c.send("one")
	if _, stderr, err := c.recv(context.Background(), "--wait", "0s"); err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if got, want := c.recorded(), fmt.Sprintf("%q\n", []string{}); got != want {
		t.Fatalf("the config's callback recorded %q, want %q", got, want)
	}

	c = newCallback(t)
	c.execConfig("args")
	c.send("one")
	if _, stderr, err := c.recv(context.Background()); err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if got := c.recorded(); got != "" {
		t.Fatalf("a recv that does not wait ran the callback: %q", got)
	}

	c = newCallback(t)
	c.execConfig("args")
	c.send("one")
	other := filepath.Join(t.TempDir(), "other")
	if _, stderr, err := c.recv(context.Background(), "--wait", "0s", "--exec", os.Args[0], "--", callbackWord, other, "args", "flag"); err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if got := c.recorded(); got != "" {
		t.Fatalf("the config's callback ran beside --exec: %q", got)
	}
	if raw, err := os.ReadFile(other); err != nil || string(raw) != fmt.Sprintf("%q\n", []string{"flag"}) { //nolint:gosec // the test's own file
		t.Fatalf("--exec's callback recorded %q, %v", raw, err)
	}

	if testing.Short() {
		return // the timeout below runs three tries of a second each
	}
	c = newCallback(t)
	c.execConfig("hang", `exec-timeout = "1s"`)
	c.send("hangs")
	stdout, stderr, err := c.recv(context.Background(), "--wait", "0s")
	if status := report(err, io.Discard); status != 3 || len(ofType(t, stdout, "exec_failed")) != 1 {
		t.Fatalf("recv: exit %d, %v %s %s", status, err, stdout, stderr)
	}
}

// A [recv] exec that names no program, or one that is not there, and an
// exec-timeout with no exec or of zero, are the config's error, and take
// nothing from the server.
func TestAWrongExecInTheConfigIsTheConfigsError(t *testing.T) {
	c := newCallback(t)
	c.send("waits")
	dir := c.b.Dir()
	good := "base-dir = " + tomlString(dir) + "\nmail-dir = " + tomlString(filepath.Join(dir, "mail")) + "\n[recv]\n"
	for name, body := range map[string]string{
		"an empty list":                "exec = []",
		"an empty program":             `exec = [""]`,
		"a program that is not there":  "exec = [" + tomlString(filepath.Join(t.TempDir(), "no-such-program")) + "]",
		"an exec-timeout with no exec": `exec-timeout = "1m"`,
		"an exec-timeout of zero":      "exec = [" + tomlString(os.Args[0]) + "]\n" + `exec-timeout = "0s"`,
	} {
		t.Setenv("DROMAS_CONFIG", writeConfig(t, good+body+"\n"))
		_, _, err := c.recv(context.Background(), "--wait", "0s")
		var bad *configError
		if !errors.As(err, &bad) || !strings.HasPrefix(bad.key, "recv.exec") {
			t.Errorf("%s: %#v, want the config's error at recv.exec", name, err)
		}
	}
	if n := c.srv.Count(c.b.Address()); n != 1 {
		t.Errorf("%d messages left on the server, want the one none of them took", n)
	}
}
