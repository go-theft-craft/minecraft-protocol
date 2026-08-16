package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	capturepkg "github.com/go-theft-craft/minecraft-protocol/capture"
)

// everyCommand is the list the help and exit-code tests walk. A command added
// without a line here is a command with no help test.
var everyCommand = []string{
	"version", "data", "packet", "status", "login", "capture", "inspect", "replay",
}

func TestNoArgumentsPrintsTheCommandListAndExitsUsage(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCLI(t)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want diagnostics on stderr", stdout)
	}
	for _, command := range everyCommand {
		if !strings.Contains(stderr, command) {
			t.Errorf("the command list does not mention %q", command)
		}
	}
}

func TestHelpExitsSuccessAtEveryLevel(t *testing.T) {
	t.Parallel()

	code, stdout, _ := runCLI(t, "--help")
	if code != exitSuccess {
		t.Fatalf("root --help exit code = %d, want %d", code, exitSuccess)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("root help does not show usage: %q", stdout)
	}

	for _, command := range everyCommand {
		code, stdout, stderr := runCLI(t, command, "-h")
		if code != exitSuccess {
			t.Errorf("%s -h exit code = %d, want %d (stderr: %s)", command, code, exitSuccess, stderr)
		}
		if !strings.Contains(stdout, "Usage:") {
			t.Errorf("%s -h printed no usage: %q", command, stdout)
		}
	}
}

func TestAnUnknownCommandNamesItAndExitsUsage(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCLI(t, "frobnicate")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Fatalf("stderr does not name the unknown command: %q", stderr)
	}
}

// TestAMissingRequiredFlagNamesItAndShowsAnExample is what makes the tool
// usable without the documentation open: the error says which flag, and the
// help it prints alongside contains a line that would have worked.
func TestAMissingRequiredFlagNamesItAndShowsAnExample(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		flag string
	}{
		{args: []string{"status"}, flag: "--address"},
		{args: []string{"login", "--address", "127.0.0.1:1"}, flag: "--username"},
		{args: []string{"inspect"}, flag: "--input"},
		{args: []string{"replay"}, flag: "--input"},
		{args: []string{"capture", "--address", "127.0.0.1:1"}, flag: "--output"},
	}

	for _, test := range cases {
		code, _, stderr := runCLI(t, test.args...)
		if code != exitUsage {
			t.Errorf("%v exit code = %d, want %d", test.args, code, exitUsage)
		}
		if !strings.Contains(stderr, test.flag) {
			t.Errorf("%v did not name %s: %q", test.args, test.flag, stderr)
		}
		if !strings.Contains(stderr, "Examples:") {
			t.Errorf("%v printed no worked example", test.args)
		}
	}
}

func TestAnUnknownFlagExitsUsage(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCLI(t, "inspect", "--nonsense")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "nonsense") {
		t.Fatalf("stderr does not name the unknown flag: %q", stderr)
	}
}

func TestVersionJSONIsAStableObject(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCLI(t, "version", "--format", "json")
	if code != exitSuccess {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitSuccess, stderr)
	}

	var report versionReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("version JSON does not parse: %v\n%s", err, stdout)
	}
	if report.CaptureFormat != capturepkg.FormatVersion {
		t.Errorf("captureFormat = %d, want %d", report.CaptureFormat, capturepkg.FormatVersion)
	}
	if len(report.Protocols) < 2 {
		t.Errorf("version lists %d protocols, want both", len(report.Protocols))
	}
	for _, entry := range report.Protocols {
		if entry.ID == "" || entry.Protocol == 0 {
			t.Errorf("protocol entry %+v is incomplete", entry)
		}
	}
}

// TestEverySuccessfulInvocationWritesOnlyDataToStdout is the property that
// makes the tool safe in a pipeline.
func TestEverySuccessfulInvocationWritesOnlyDataToStdout(t *testing.T) {
	t.Parallel()

	code, stdout, _ := runCLI(t, "version", "--format", "json")
	if code != exitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not the data alone: %q", stdout)
	}
}

func TestStatusAgainstARefusedConnectionExitsPeer(t *testing.T) {
	t.Parallel()

	// Port 1 on loopback refuses rather than hanging, which is the failure
	// this exit code exists to distinguish from a bad invocation.
	code, _, stderr := runCLI(t, "status", "--address", "127.0.0.1:1", "--timeout", "2s")
	if code != exitPeer {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitPeer, stderr)
	}
}

func TestLoginRefusesOnlineModeAndPointsSomewhere(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCLI(t, "login", "--address", "127.0.0.1:1", "--username", "tester")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "headless-minecraft") {
		t.Fatalf("the refusal does not say where an authenticated login lives: %q", stderr)
	}
}

// writeTestCapture writes a small capture and returns its path.
func writeTestCapture(t *testing.T, closed bool) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.mcpcap")
	sink, err := capturepkg.NewFileSink(path, capturepkg.Header{
		Protocol:          "java/1.8.9",
		Role:              "client",
		FrameBytes:        1 << 20,
		DecompressedBytes: 1 << 20,
	}, capturepkg.WithFlushBytes(1))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	snapshot := protocol.NewSnapshot(protocol.State("play"), nil)
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if err := sink.Observe(t.Context(), protocol.Observation{
			Sequence:  sequence,
			Frame:     sequence,
			Direction: protocol.DirectionClientbound,
			Stage:     protocol.ObservationPacket,
			Before:    snapshot,
			After:     snapshot,
			Packet: &protocol.PacketMetadata{
				State: "play", Direction: protocol.DirectionClientbound, ID: 0x00, Name: "keep_alive",
			},
			Bytes: []byte{byte(sequence)},
		}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if closed {
		if err := sink.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	return path
}

func TestInspectPrintsOneLinePerRecord(t *testing.T) {
	t.Parallel()

	path := writeTestCapture(t, true)

	code, stdout, stderr := runCLI(t, "inspect", "--input", path)
	if code != exitSuccess {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitSuccess, stderr)
	}
	if lines := strings.Count(strings.TrimSpace(stdout), "\n") + 1; lines != 3 {
		t.Fatalf("printed %d lines, want one per record:\n%s", lines, stdout)
	}
}

func TestInspectJSONEmitsOneObjectPerLine(t *testing.T) {
	t.Parallel()

	path := writeTestCapture(t, true)

	code, stdout, _ := runCLI(t, "inspect", "--input", path, "--format", "json")
	if code != exitSuccess {
		t.Fatalf("exit code = %d", code)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var record inspectRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line is not one object: %v\n%s", err, line)
		}
		if record.Name != "keep_alive" {
			t.Errorf("record names %q, want keep_alive", record.Name)
		}
	}
}

func TestInspectFilterNarrowsTheOutput(t *testing.T) {
	t.Parallel()

	path := writeTestCapture(t, true)

	code, stdout, stderr := runCLI(t, "inspect", "--input", path, "--filter", "sequence=2")
	if code != exitSuccess {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr)
	}
	if lines := strings.Count(strings.TrimSpace(stdout), "\n") + 1; lines != 1 {
		t.Fatalf("filter matched %d lines, want 1:\n%s", lines, stdout)
	}
}

func TestInspectRejectsABadFilter(t *testing.T) {
	t.Parallel()

	path := writeTestCapture(t, true)

	code, _, stderr := runCLI(t, "inspect", "--input", path, "--filter", "nonsense=1")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "nonsense") {
		t.Fatalf("stderr does not name the unknown field: %q", stderr)
	}
}

func TestReplayVerifyOnAnUntouchedCaptureSucceeds(t *testing.T) {
	t.Parallel()

	path := writeTestCapture(t, true)

	code, stdout, stderr := runCLI(t, "replay", "--input", path, "--verify")
	if code != exitSuccess {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitSuccess, stderr)
	}
	if !strings.Contains(stdout, "digest") {
		t.Fatalf("stdout does not report a digest:\n%s", stdout)
	}
}

// TestReplayVerifyOnACaptureWithNoTrailerExitsVerify covers the exit code that
// says a check ran and did not match, as opposed to one that could not run.
func TestReplayVerifyOnACaptureWithNoTrailerExitsVerify(t *testing.T) {
	t.Parallel()

	path := writeTestCapture(t, false)

	code, _, stderr := runCLI(t, "replay", "--input", path, "--verify")
	if code != exitVerify {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitVerify, stderr)
	}
	if !strings.Contains(stderr, "trailer") {
		t.Fatalf("the failure does not say why it could not compare: %q", stderr)
	}
}

func TestReplayConnectWithoutDirectionIsAUsageError(t *testing.T) {
	t.Parallel()

	path := writeTestCapture(t, true)

	code, _, stderr := runCLI(t, "replay", "--input", path, "--connect", "127.0.0.1:1")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "--direction") {
		t.Fatalf("stderr does not name the missing flag: %q", stderr)
	}
}

func TestReplayOnAMissingFileExitsFailure(t *testing.T) {
	t.Parallel()

	code, _, _ := runCLI(t, "replay", "--input", filepath.Join(t.TempDir(), "absent.mcpcap"))
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
}

func TestCaptureRefusesToOverwriteWithoutBeingTold(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.mcpcap")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	code, _, stderr := runCLI(t,
		"capture", "--address", "127.0.0.1:1", "--output", path,
		"--username", "tester", "--offline", "--timeout", "2s",
	)
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitFailure, stderr)
	}
	if !strings.Contains(stderr, "exists") {
		t.Fatalf("stderr does not say the file exists: %q", stderr)
	}
}
