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

// With --headers the callback gets the message_headers line, as recv prints it,
// with none of the text the sender chose.
func TestExecWithHeadersGetsTheHeadersLine(t *testing.T) {
	c := newCallback(t)
	c.send("the words of the sender")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s", "--headers"}, c.exec("exit", "0")...)...)
	if err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	if got := c.recorded(); got != stdout || !strings.HasPrefix(got, `{"type":"message_headers"`) || strings.Contains(got, "the words") {
		t.Fatalf("the callback read %q, recv printed %q", got, stdout)
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
	failed, gaveUp := ofType(t, stdout, "exec_failed"), ofType(t, stdout, "exec_gave_up")
	if len(messages) != 2 || len(failed) != 6 || len(gaveUp) != 2 {
		t.Fatalf("recv printed %s", stdout)
	}
	for i, l := range failed {
		id := messages[i/3]["id"]
		if l["id"] != id || l["try"] != jsonInt(i%3+1) || l["exit_status"] != jsonInt(7) || l["signal"] != nil {
			t.Errorf("failed try %d: %v", i, l)
		}
		if gaveUp[i/3]["id"] != id || gaveUp[i/3]["tries"] != jsonInt(3) {
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

// Exit status 100 says failed for good: no more tries.
func TestExecExit100IsPermanent(t *testing.T) {
	c := newCallback(t)
	c.send("fails for good")
	stdout, _, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("exit", "100")...)...)
	if status := report(err, io.Discard); status != 3 || len(ofType(t, stdout, "exec_failed")) != 1 || len(ofType(t, stdout, "exec_gave_up")) != 1 {
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
	if err != nil || len(ofType(t, stdout, "exec_failed")) != 1 || len(ofType(t, stdout, "exec_gave_up")) != 0 {
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
	failed := ofType(t, stdout, "exec_failed")
	if len(failed) != 6 || len(ofType(t, stdout, "exec_gave_up")) != 2 {
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
	for _, typ := range []string{"exec_failed", "exec_gave_up"} {
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
	if err != nil || len(ofType(t, stdout, "exec_failed")) != 0 || c.recorded() != "ran\n" {
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
	if first.err != nil || len(ofType(t, first.stdout, "exec_failed")) != 0 {
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
