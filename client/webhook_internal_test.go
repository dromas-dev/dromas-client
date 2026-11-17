package client

import (
	"encoding/base64"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The signature is Standard Webhooks' v1: its reference library's own vector
// (libraries/go/webhook_test.go, TestWebhookSign, read 2026-11-17), whose secret
// is 24 bytes, the least the spec allows.
func TestTheWebhookSignatureIsStandardWebhooks(t *testing.T) {
	t.Parallel()
	key, err := webhookSecret("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw")
	if err != nil {
		t.Fatal(err)
	}
	got := webhookSignature(key, "msg_p5jXN8AQM9LWM0D4loKWxJek", time.Unix(1614265330, 0), []byte(`{"test": 2432232314}`))
	if want := "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="; got != want {
		t.Fatalf("signature %s, want %s", got, want)
	}
}

// A secret is whsec_ and the standard base64 of 24 to 64 bytes, and nothing
// else.
func TestAWebhookSecretHasTheSpecsForm(t *testing.T) {
	t.Parallel()
	of := func(n int) string { return "whsec_" + b64(strings.Repeat("k", n)) }
	for _, ok := range []string{of(24), of(64), of(32)} {
		if _, err := webhookSecret(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{of(23), of(65), b64(strings.Repeat("k", 32)), "whsec_", "whsec_not base64!", "WHSEC_" + b64(strings.Repeat("k", 32)), of(32) + " "} {
		if _, err := webhookSecret(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// The header file's line is one header, never one a post writes itself, in
// any case.
func TestAWebhookHeaderIsOneHeaderTheOwnersOwn(t *testing.T) {
	t.Parallel()
	for line, want := range map[string][2]string{
		"Authorization: Bearer abc":   {"Authorization", "Bearer abc"},
		"X-Token:abc":                 {"X-Token", "abc"},
		"X-Token: \t two words \t":    {"X-Token", "two words"},
		"user-agent: theirs":          {"user-agent", "theirs"},
		"Webhookish: not the reserve": {"Webhookish", "not the reserve"},
	} {
		name, value, err := webhookHeader(line)
		if err != nil || name != want[0] || value != want[1] {
			t.Errorf("%q: %q %q, %v", line, name, value, err)
		}
	}
	for _, bad := range []string{
		"no colon", ": no name", "Two Words: x", "X-Token:", "X-Token:   ", "X-Token: a\x00b",
		"Content-Type: text/plain", "CONTENT-LENGTH: 1", "host: elsewhere", "Transfer-Encoding: chunked",
		"webhook-signature: v1,forged", "Webhook-Id: x", "WEBHOOK-TIMESTAMP: 1",
	} {
		if _, _, err := webhookHeader(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

// A header or secret file is one line, less one ending, and is refused where
// others can read it, with no override; neither refusal quotes what it holds.
func TestAWebhookFileIsOneLineItsOwnersAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(_, s string, mode os.FileMode) string {
		p := privateFile(t, dir, s)
		if mode != 0o600 {
			if err := os.Chmod(p, mode); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	for _, ending := range []string{"", "\n", "\r\n"} {
		h, err := ReadWebhookHeader(write("h", "Authorization: Bearer sekrit"+ending, 0o600))
		if err != nil || h.Value != "Bearer sekrit" {
			t.Errorf("ending %q: %+v, %v", ending, h, err)
		}
	}
	if _, err := ReadWebhookHeader(write("two", "A: 1\nB: 2\n", 0o600)); err == nil || strings.Contains(err.Error(), "B: 2") {
		t.Errorf("two lines: %v", err)
	}
	if _, err := ReadWebhookHeader(write("bad", "Content-Type: sekrit", 0o600)); err == nil || strings.Contains(err.Error(), "sekrit") {
		t.Errorf("a reserved header: %v", err)
	}
	if _, err := ReadWebhookSecret(write("s", "whsec_sekrit", 0o600)); err == nil || strings.Contains(err.Error(), "sekrit") {
		t.Errorf("a bad secret: %v", err)
	}
	if runtime.GOOS == "windows" {
		return // modes are not access lists: perm_windows_test.go holds checkKeyFile there
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o660} {
		if _, err := ReadWebhookHeader(write("open", "Authorization: Bearer sekrit", mode)); err == nil || strings.Contains(err.Error(), "sekrit") {
			t.Errorf("mode %04o: %v", mode, err)
		}
		if _, err := ReadWebhookSecret(write("open", "whsec_"+b64(strings.Repeat("k", 32)), mode)); err == nil {
			t.Errorf("a secret at mode %04o taken", mode)
		}
	}
}

// A webhook's URL is http or https with a host, and carries no user or
// password; a refusal never quotes it.
func TestAWebhookURLCarriesNoSecret(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"http://127.0.0.1:5678/webhook/x", "https://n8n.example/webhook/abc?token=q", "http://[::1]/"} {
		if err := CheckWebhookURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"https://user:sekrit@example.com/", "https://sekrit@example.com/", "ftp://example.com/", "example.com/x", "https:///x", "mailto:sekrit@example.com", "http://sekrit\x7f/"} {
		err := CheckWebhookURL(bad)
		if err == nil || strings.Contains(err.Error(), "sekrit") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// A Retry-After is read in both of RFC 9110's forms; a date past, or anything
// else, asks for no wait.
func TestAWebhooksRetryAfterIsReadInBothForms(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{
		"120": 2 * time.Minute,
		now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
		now.Add(-time.Minute).Format(http.TimeFormat):     0,
		"":            0,
		"soon":        0,
		"-5":          0,
		"Sunday noon": 0,
	} {
		if got := webhookRetryAfter(v, now); got != want {
			t.Errorf("%q: %s, want %s", v, got, want)
		}
	}
}

// privateFile writes body to a new file in dir, its owner's alone from its
// making on every system (client.NewPrivateTemp), and returns its name.
func privateFile(t *testing.T, dir, body string) string {
	t.Helper()
	f, err := NewPrivateTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}
