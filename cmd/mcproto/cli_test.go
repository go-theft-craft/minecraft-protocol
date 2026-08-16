package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI runs the CLI with args, returning its exit code, stdout, and stderr.
// It drives the same entry point main does, so exit codes are covered rather
// than inferred.
//
// It is not called "capture": that is the name of a package this command
// imports, and a package-level identifier may not shadow one.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	return runCLIWithInput(t, "", args...)
}

// runCLIWithInput is runCLI with something on stdin, for the commands that
// read a packet body or a JSON object from a pipe.
func runCLIWithInput(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()

	dir := t.TempDir()
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}

	code := run(context.Background(), args, strings.NewReader(stdin), outFile, errFile)

	if err := outFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := errFile.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, err := os.ReadFile(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}

	return code, string(stdout), string(stderr)
}

func TestHelpSucceedsOnStdout(t *testing.T) {
	code, stdout, _ := runCLI(t, "--help")
	if code != exitSuccess {
		t.Errorf("exit = %d, want %d", code, exitSuccess)
	}
	if !strings.Contains(stdout, "mcproto") {
		t.Errorf("stdout = %q, want the root usage", stdout)
	}

	code, stdout, _ = runCLI(t, "data", "--help")
	if code != exitSuccess {
		t.Errorf("data --help exit = %d, want %d", code, exitSuccess)
	}
	for _, want := range []string{"fetch", "validate"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("data usage does not mention %q", want)
		}
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	cases := [][]string{
		{},
		{"nosuchcommand"},
		{"data"},
		{"data", "nosuchsubcommand"},
		{"data", "validate"},
		{"data", "validate", "--source", "x", "--format", "yaml"},
		{"data", "validate", "--nosuchflag"},
		{"data", "validate", "--source", "x", "extra"},
	}

	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, _, stderr := runCLI(t, args...)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d (stderr: %s)", code, exitUsage, stderr)
			}
		})
	}
}

func TestValidateFailureExitsOne(t *testing.T) {
	code, _, stderr := runCLI(t, "data", "validate", "--source", filepath.Join(t.TempDir(), "absent"))
	if code != exitFailure {
		t.Errorf("exit = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "mcproto:") {
		t.Errorf("stderr = %q, want a prefixed message", stderr)
	}
}

func TestValidateReportsTheCheckedInTree(t *testing.T) {
	code, stdout, stderr := runCLI(t, "data", "validate", "--source", "../../source/java/1.8", "--format", "json")
	if code != exitSuccess {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitSuccess, stderr)
	}

	var report treeReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}

	if report.Protocol != 47 {
		t.Errorf("protocol = %d, want 47", report.Protocol)
	}
	if report.DatasetCount != len(report.Datasets) || report.DatasetCount != 17 {
		t.Errorf("datasetCount = %d with %d datasets, want 17", report.DatasetCount, len(report.Datasets))
	}

	// Java 1.8 data all comes from one upstream directory, and the manifest
	// records that directory as 1.8 while the target version is 1.8.9. The
	// alias flag reports where the bytes came from, so every dataset here is
	// flagged; what matters is that the count and the flags agree.
	aliased := 0
	for _, dataset := range report.Datasets {
		if dataset.Aliased {
			aliased++
		}
		if dataset.SourceVersion != "1.8" {
			t.Errorf("%s came from %q, want 1.8", dataset.Name, dataset.SourceVersion)
		}
	}
	if aliased != report.AliasedCount {
		t.Errorf("aliasedCount = %d, want %d", report.AliasedCount, aliased)
	}
}

func TestValidateRejectsATamperedTree(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "1.8")
	copyTree(t, "../../source/java/1.8", source)

	if err := os.WriteFile(filepath.Join(source, "blocks.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLI(t, "data", "validate", "--source", source)
	if code != exitFailure {
		t.Errorf("exit = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "blocks") {
		t.Errorf("stderr = %q, want it to name the dataset", stderr)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()

	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			copyTree(t, filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()))

			continue
		}
		body, err := os.ReadFile(filepath.Join(from, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, entry.Name()), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
