package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestReleaseBuildIsReproducible asserts what -trimpath buys rather than assuming it: the
// same source built by scripts/build-release.sh from two different directories yields
// byte-identical binaries, and so identical SHA256SUMS.
//
// Two checkouts on one machine differ only in path, so this is the local half of the claim.
// The other half — two machines, different host OS — is the release workflow's
// `reproducible` job, which diffs the SHA256SUMS of an ubuntu and a macOS build before
// anything is published.
func TestReleaseBuildIsReproducible(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary twice; skipped under -short")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("scripts/build-release.sh needs bash")
	}

	root := moduleRoot(t)
	target := runtime.GOOS + "/" + runtime.GOARCH

	var sums [2][]byte
	for i := range sums {
		// A copy of the module in its own directory, so the only thing that differs
		// between the two builds is where the source lives.
		dir := filepath.Join(t.TempDir(), "checkout")
		copyModule(t, root, dir)

		out := filepath.Join(dir, "dist")
		cmd := exec.Command("bash", filepath.Join(dir, "scripts", "build-release.sh"), out)
		cmd.Env = append(os.Environ(),
			"NUTZ_VERIFY_TARGETS="+target,
			// The property under test is path-independence under one toolchain. The
			// script pins the release toolchain itself, which would download one here;
			// that pin is exercised by the release workflow, not by this test.
			"GOTOOLCHAIN=local",
		)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %d: %v\n%s", i, err, b)
		}

		var err error
		sums[i], err = os.ReadFile(filepath.Join(out, "SHA256SUMS"))
		if err != nil {
			t.Fatal(err)
		}
	}

	if !bytes.Equal(sums[0], sums[1]) {
		t.Fatalf("two builds of the same source at different paths differ:\n%s---\n%s", sums[0], sums[1])
	}

	name := "nutz-verify_" + runtime.GOOS + "_" + runtime.GOARCH
	if !strings.Contains(string(sums[0]), "  "+name+"\n") {
		t.Fatalf("SHA256SUMS does not list %s:\n%s", name, sums[0])
	}
}

// moduleRoot is the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}

	return strings.TrimSpace(string(out))
}

// copyModule copies what a build needs — the module files, the source and the build
// script — and nothing that would make the copy differ from a fresh checkout.
func copyModule(t *testing.T, root, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, sub := range []string{"cmd", "internal", "chain", "cache", "epoch", "merkle", "scripts"} {
		if err := os.CopyFS(filepath.Join(dir, sub), os.DirFS(filepath.Join(root, sub))); err != nil {
			t.Fatalf("copying %s: %v", sub, err)
		}
	}
}
