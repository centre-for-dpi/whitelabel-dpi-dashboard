package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/configschema"
)

func TestRunWritesEverySchema(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "schema") // not yet created, so MkdirAll runs

	if err := run(dir, false); err != nil {
		t.Fatalf("run: %v", err)
	}

	files, err := configschema.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, f.Name))
		if err != nil {
			t.Errorf("%s was not written: %v", f.Name, err)
			continue
		}
		if string(got) != string(f.JSON) {
			t.Errorf("%s does not match what configschema produced", f.Name)
		}
	}
}

func TestCheckPassesOnFreshOutput(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, false); err != nil {
		t.Fatalf("run: %v", err)
	}

	if err := run(dir, true); err != nil {
		t.Errorf("check failed immediately after writing: %v", err)
	}
}

func TestCheckReportsMissingFiles(t *testing.T) {
	// This is the failure an integrator sees in CI when someone changes a
	// config struct and forgets to regenerate.
	err := run(t.TempDir(), true)
	if err == nil {
		t.Fatal("check passed against an empty directory")
	}
	if !strings.Contains(err.Error(), "make schema") {
		t.Errorf("error does not say how to fix it: %v", err)
	}
}

func TestCheckReportsStaleFiles(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.schema.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(dir, true)
	if err == nil {
		t.Fatal("check passed against an edited file")
	}
	if !strings.Contains(err.Error(), "app.schema.json") {
		t.Errorf("error does not name the stale file: %v", err)
	}
}

func TestRunReportsAnUnusableOutputDirectory(t *testing.T) {
	// A path whose parent is a regular file cannot be created.
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run(filepath.Join(blocker, "schema"), false); err == nil {
		t.Error("writing beneath a regular file was accepted")
	}
}

func TestRunReportsAnUnwritableOutputDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can write regardless of mode")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make a directory read-only here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := run(dir, false); err == nil {
		t.Error("writing into a read-only directory was accepted")
	}
}
