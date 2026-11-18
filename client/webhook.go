package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
	"golang.org/x/net/http/httpguts"
)

// WebhookTimeout bounds one post to a webhook: the top of the 15 to 30 seconds
// Standard Webhooks recommends (read 2026-11-17).
const WebhookTimeout = 30 * time.Second

// webhookSecretPrefix and the secret's bounds are Standard Webhooks' for a
// symmetric secret: whsec_ and the base64 of 24 to 64 random bytes.
const (
	webhookSecretPrefix = "whsec_"
	webhookSecretMin    = 24
	webhookSecretMax    = 64
)

// Webhook posts a delivered message's line to an endpoint its owner names, with
// a header its owner sets and a Standard Webhooks signature, each where given.
// It reads nothing of the mailbox.
type Webhook struct {
	url         string
	name, value string
	secret      []byte
	client      *http.Client
	agent       string
}

// WebhookError is a post that failed: the endpoint's answer, by its status, or
// no answer, by a kind from a fixed set of this client's own words. Neither
// quotes the endpoint, net/http or the URL.
type WebhookError struct {
	// Status is the answer's status, a 3xx among them, which is never
	// followed; 0 for no answer.
	Status int
	// Failure is why no answer came, where Status is 0.
	Failure string
	// RetryAfter is the wait the answer's Retry-After asks for, in either of
	// its forms, or 0 for none.
	RetryAfter time.Duration
}

func (e *WebhookError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("the webhook answered %d", e.Status)
	}
	return "the webhook: " + e.Failure
}

// CheckWebhookURL refuses a webhook's URL that is not http or https with a
// host, and one that carries a user or a password: the URL is an argument,
// and a secret is never one (#15), so a token goes in the header file. Its
// words never quote the URL.
func CheckWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return errors.New("the webhook's URL does not parse as a URL")
	case u.Scheme != "http" && u.Scheme != "https":
		return errors.New("the webhook's URL is http or https")
	case u.Host == "" || u.Opaque != "":
		return errors.New("the webhook's URL names no host")
	case u.User != nil:
		return errors.New("the webhook's URL carries a user or a password, which an argument never carries: put a token in the header file")
	}
	return nil
}

// webhookReserved are the headers a post writes itself, which the header file
// may not set: the body's, the host's, and Standard Webhooks' (any webhook-*).
var webhookReserved = []string{"content-type", "content-length", "host", "transfer-encoding"}

// WebhookHeader is the one header each post to a webhook carries beside its
// own, as its owner set it.
type WebhookHeader struct{ Name, Value string }

// ReadWebhookHeader reads the header file name: one line, "Name: Value". The
// file is read once, refused where others can reach it, as ssh refuses a
// private key (checkKeyFile), since it may hold a token its owner put in place.
// A header that a post writes itself is refused. Its words name the file,
// never what it holds.
func ReadWebhookHeader(name string) (WebhookHeader, error) {
	line, err := readWebhookFile("the webhook's header file", name)
	if err != nil {
		return WebhookHeader{}, err
	}
	var h WebhookHeader
	if h.Name, h.Value, err = webhookHeader(line); err != nil {
		return WebhookHeader{}, fmt.Errorf("the webhook's header file %s: %w", name, err)
	}
	return h, nil
}

// ReadWebhookSecret reads the secret file name, Standard Webhooks' symmetric
// secret: whsec_ and the base64 of 24 to 64 bytes, which it returns decoded. It
// judges and reads the file as ReadWebhookHeader does, and refuses a secret of
// another form.
func ReadWebhookSecret(name string) ([]byte, error) {
	line, err := readWebhookFile("the webhook's secret file", name)
	if err != nil {
		return nil, err
	}
	key, err := webhookSecret(line)
	if err != nil {
		return nil, fmt.Errorf("the webhook's secret file %s: %w", name, err)
	}
	return key, nil
}

// NewWebhook is the webhook at the URL raw, where CheckWebhookURL takes it. Where
// h is not nil, each post carries it; where secret is not nil, each post is
// signed by Standard Webhooks' v1 scheme under it (ReadWebhookHeader,
// ReadWebhookSecret, whose rules it holds both to). hc makes the posts where it
// is not nil, and otherwise a client with the system's CAs and the environment's
// proxy, as net/http's default: never the mailbox's, whose CAs are its server's.
// Neither follows a redirect.
func NewWebhook(raw string, h *WebhookHeader, secret []byte, hc *http.Client) (*Webhook, error) {
	if err := CheckWebhookURL(raw); err != nil {
		return nil, err
	}
	w := &Webhook{url: raw, agent: "dromas/" + Version()}
	if h != nil {
		var err error
		if w.name, w.value, err = webhookHeader(h.Name + ": " + h.Value); err != nil {
			return nil, fmt.Errorf("the webhook's header: %w", err)
		}
	}
	if secret != nil {
		if len(secret) < webhookSecretMin || len(secret) > webhookSecretMax {
			return nil, fmt.Errorf("the webhook's secret is %d to %d bytes", webhookSecretMin, webhookSecretMax)
		}
		w.secret = secret
	}
	c := http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	if hc != nil {
		c = *hc
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	w.client = &c
	return w, nil
}

// readWebhookFile reads what, a webhook's header or secret file at name,
// refused where others can reach it (checkKeyFile), and returns its one line:
// what it holds, less one line ending, LF or CRLF.
func readWebhookFile(what, name string) (string, error) {
	if err := checkKeyFile(what, name); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(name) //nolint:gosec // a file the mailbox's owner named
	if err != nil {
		return "", err
	}
	s := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("%s %s holds more than one line", what, name)
	}
	return s, nil
}

// webhookHeader reads line as one header field, "Name: Value" (RFC 9110 §5),
// whose value is trimmed of the spaces and tabs around it.
func webhookHeader(line string) (name, value string, err error) {
	name, value, ok := strings.Cut(line, ":")
	value = strings.Trim(value, " \t")
	switch {
	case !ok || !httpguts.ValidHeaderFieldName(name):
		return "", "", errors.New(`its line is not one header, "Name: Value"`)
	case value == "" || !httpguts.ValidHeaderFieldValue(value):
		return "", "", errors.New("its header's value is empty, or holds a character a header may not")
	}
	lower := protocol.LowerASCII(name)
	for _, r := range webhookReserved {
		if lower == r {
			return "", "", fmt.Errorf("its header is %s, which each post writes itself", name)
		}
	}
	if strings.HasPrefix(lower, "webhook-") {
		return "", "", fmt.Errorf("its header is %s, and each post writes the webhook-* headers itself", name)
	}
	return name, value, nil
}

// webhookSecret reads line as Standard Webhooks' symmetric secret.
func webhookSecret(line string) ([]byte, error) {
	rest, ok := strings.CutPrefix(line, webhookSecretPrefix)
	key, err := base64.StdEncoding.Strict().DecodeString(rest)
	if !ok || err != nil || len(key) < webhookSecretMin || len(key) > webhookSecretMax {
		return nil, fmt.Errorf("it holds no Standard Webhooks secret: %s and the base64 of %d to %d random bytes", webhookSecretPrefix, webhookSecretMin, webhookSecretMax)
	}
	return key, nil
}

// webhookSignature is Standard Webhooks' v1 signature of body, posted as the
// message id at the time at: the HMAC-SHA256, under secret, of id, the time
// in Unix seconds and body, joined by full stops.
func webhookSignature(secret []byte, id string, at time.Time, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%s.%d.", id, at.Unix())
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Post makes one post of body, the message id's line, as JSON, with the
// owner's header and, where the webhook has a secret, the webhook-id,
// webhook-timestamp and webhook-signature headers: the id the same at each
// try, the time and signature new. Any 2xx answer is success; any other
// answer, or none within WebhookTimeout, is a *WebhookError.
func (w *Webhook) Post(ctx context.Context, id string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, WebhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return &WebhookError{Failure: "the request could not be made"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", w.agent)
	if w.name != "" {
		req.Header.Set(w.name, w.value)
	}
	if w.secret != nil {
		at := time.Now()
		req.Header.Set("webhook-id", id)
		req.Header.Set("webhook-timestamp", strconv.FormatInt(at.Unix(), 10))
		req.Header.Set("webhook-signature", webhookSignature(w.secret, id, at, body))
	}
	resp, err := w.client.Do(req)
	if err != nil {
		var te *transportError
		if errors.As(noAnswer("POST webhook", "no answer", err), &te) {
			return &WebhookError{Failure: te.kind}
		}
		return &WebhookError{Failure: "no answer"}
	}
	// Read to its end, bounded, so the connection is used again; nothing of
	// it is kept.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &WebhookError{Status: resp.StatusCode, RetryAfter: webhookRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
}

// webhookRetryAfter is the wait a Retry-After asks for at now, in either form
// RFC 9110 §10.2.3 gives it: delay-seconds, or an HTTP-date. A webhook's
// endpoint is anyone's server, where retryAfter reads only the delay-seconds
// this protocol's servers write. Anything else, or a date past, asks for none.
func webhookRetryAfter(v string, now time.Time) time.Duration {
	if d := retryAfter(v); d > 0 {
		return d
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
