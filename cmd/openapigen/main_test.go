package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// src is the repository root, three levels up from cmd/openapigen.
const src = "../.."

func TestRunWritesTheDocument(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")

	if err := run(dir, src, false); err != nil {
		t.Fatalf("writing: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "openapi.json"))
	if err != nil {
		t.Fatalf("nothing was written: %v", err)
	}
	if !strings.Contains(string(raw), `"openapi": "3.1.0"`) {
		t.Error("what was written is not an OpenAPI 3.1 document")
	}
}

func TestCheckPassesOnFreshOutput(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, src, false); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := run(dir, src, true); err != nil {
		t.Errorf("a document just written is reported stale: %v", err)
	}
}

// This is the failure an integrator sees in CI when someone changes a proto and
// forgets to regenerate, so the message has to say how to fix it.
func TestCheckReportsAMissingDocument(t *testing.T) {
	err := run(t.TempDir(), src, true)
	if err == nil {
		t.Fatal("a missing document passed the check")
	}
	if !strings.Contains(err.Error(), "make openapi") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}

func TestCheckReportsAStaleDocument(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openapi.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, src, true); err == nil {
		t.Error("a stale document passed the check")
	}
}

// The examples are read from the committed fixtures, so a run with nowhere to
// read them from has to fail rather than emit a document with none.
func TestRunReportsMissingFixtures(t *testing.T) {
	if err := run(t.TempDir(), t.TempDir(), false); err == nil {
		t.Error("generating without the fixtures succeeded")
	}
}

func TestRunReportsAnUnwritableDirectory(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(filepath.Join(blocker, "nested"), src, false); err == nil {
		t.Error("writing into a path that is a file succeeded")
	}
}

func TestWritingIntoAnUnwritableFileIsReported(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file should go: the write fails, the mkdir does not.
	if err := os.Mkdir(filepath.Join(dir, "openapi.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, src, false); err == nil {
		t.Error("writing over a directory succeeded")
	}
}
