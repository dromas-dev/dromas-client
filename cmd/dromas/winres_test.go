package main

import (
	"bytes"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// manifest is the application manifest of the Windows build: Microsoft's console
// allocation policy, detached, so that Windows 11 24H2 and Windows Server 2025
// allocate no console for dromas when nothing started it inside one, as a task
// of Task Scheduler. dromas started from a shell writes to that shell's console
// as before.
const manifest = "../../packaging/windows/dromas.exe.manifest"

// winArchs are the Windows architectures .goreleaser.yaml builds, each with its
// resource file beside this package: rsrc_windows_<arch>.syso, which the Go
// linker links into a build for that system and architecture alone.
var winArchs = map[string]uint16{"amd64": pe.IMAGE_FILE_MACHINE_AMD64, "arm64": pe.IMAGE_FILE_MACHINE_ARM64}

// Each resource file is what rsrc, the go.mod tool, makes of the manifest, so the
// committed bytes are the manifest's and nothing else. go test ./cmd/dromas -run
// TestTheWindowsResourcesAreTheManifests -update writes them again.
func TestTheWindowsResourcesAreTheManifests(t *testing.T) {
	t.Parallel()
	for arch := range winArchs {
		file := "rsrc_windows_" + arch + ".syso"
		made := filepath.Join(t.TempDir(), file)
		if out, err := exec.Command("go", "tool", "rsrc", "-manifest", manifest, "-arch", arch, "-o", made).CombinedOutput(); err != nil { //nolint:gosec // the go.mod tool on the files above
			t.Fatalf("go tool rsrc -arch %s: %v: %s", arch, err, out)
		}
		want, err := os.ReadFile(made)
		if err != nil {
			t.Fatal(err)
		}
		if *updateDocs {
			if err := os.WriteFile(file, want, 0o644); err != nil { //nolint:gosec // a file of the repository
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not what go tool rsrc makes of %s: run go test ./cmd/dromas -run TestTheWindowsResourcesAreTheManifests -update", file, manifest)
		}
	}
}

// dromas built for Windows carries the manifest, on each architecture a release
// builds: the go command adds to the build for that system and architecture the
// one resource file of it (SysoFiles, go help list), and that file is an object
// of that machine whose resource section holds the manifest. Nothing is
// compiled.
func TestTheWindowsBuildCarriesTheManifest(t *testing.T) {
	t.Parallel()
	want, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for arch, machine := range winArchs {
		list := exec.Command("go", "list", "-f", "{{join .SysoFiles \" \"}}", ".")
		list.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch)
		out, err := list.Output()
		file := "rsrc_windows_" + arch + ".syso"
		if err != nil || strings.TrimSpace(string(out)) != file {
			t.Fatalf("go list for windows/%s: the build adds %q (%v), want %s alone", arch, out, err, file)
		}
		f, err := pe.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		rsrc := f.Section(".rsrc")
		var data []byte
		if rsrc != nil {
			data, err = rsrc.Data()
		}
		f.Close() //nolint:errcheck,gosec // read only
		switch {
		case err != nil:
			t.Fatal(err)
		case f.Machine != machine:
			t.Errorf("%s: its machine is %#x, want %#x", file, f.Machine, machine)
		case !bytes.Contains(data, want):
			t.Errorf("%s: its resource section does not hold %s", file, manifest)
		}
	}
}
