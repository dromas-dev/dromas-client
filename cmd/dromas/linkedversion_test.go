package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// A build from a release's source archive records no version in its build
// information: it names the release it was given at link time
// (client.LinkedVersion), as a package manager builds it, in its version line
// and in every request's Dromas-Version. Built as such a build is, with no
// VCS information; the control is the same build without the release, which
// names Go's "(devel)" and DevelVersion.
func TestABuildFromSourceNamesTheVersionItWasLinkedWith(t *testing.T) {
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, c := range []struct{ linked, line, request string }{
		{"v9.8.7", "v9.8.7", "9.8.7"},
		{"", "(devel)", client.DevelVersion},
	} {
		ctx := context.Background()
		bin := filepath.Join(t.TempDir(), "dromas")
		build := []string{"build", "-buildvcs=false", "-o", bin}
		if c.linked != "" {
			build = append(build, "-ldflags", "-X github.com/dromas-dev/dromas-client/client.LinkedVersion="+c.linked)
		}
		if out, err := exec.CommandContext(ctx, "go", append(build, ".")...).CombinedOutput(); err != nil { //nolint:gosec // the test's own build
			t.Fatalf("go build: %v %s", err, out)
		}
		out, err := exec.CommandContext(ctx, bin, "version").Output() //nolint:gosec // the binary this test built
		var line struct{ Version string }
		if err != nil || json.Unmarshal(out, &line) != nil || line.Version != c.line {
			t.Errorf("linked %q: version printed %q, %v, want %q", c.linked, out, err, c.line)
		}
		srv := fakeserver.New(t, time.Now)
		b, err := client.Init(ctx, t.TempDir(), client.Options{Base: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		before := len(srv.Requests())
		renew := exec.CommandContext(ctx, bin, "prekey", "renew") //nolint:gosec // as above
		renew.Env = append(os.Environ(), "DROMAS_CONFIG="+configFor(t, b.Dir()), "DROMAS_SERVER_URL="+srv.URL)
		if out, err := renew.CombinedOutput(); err != nil {
			t.Fatalf("prekey renew: %v %s", err, out)
		}
		qs := srv.Requests()[before:]
		if len(qs) == 0 {
			t.Fatal("prekey renew made no request")
		}
		for _, q := range qs {
			if q.Version != c.request {
				t.Errorf("linked %q: %s %s names %q, want %q", c.linked, q.Method, q.Path, q.Version, c.request)
			}
		}
	}
}
