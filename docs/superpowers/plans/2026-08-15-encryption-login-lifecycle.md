# Encryption and Login Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add AES-128/CFB8 encryption below framing and a developer-controllable Java login sequence to `minecraft-protocol`, on protocol 47.

**Architecture:** A stream-owned `Conduit` sits between the transport and both pumps. It buffers ciphertext and transforms bytes as it hands them out, so a cipher installed while the read pump is blocked takes effect correctly. Encryption reaches the conduit through `Stream.Control` with a control implementing `TransportControl`, not through a session-proposed transition, because no packet carries the plaintext secret. An opt-in `login.Negotiator` drives the client sequence; the session and the code generator learn nothing about encryption beyond marking two packets sensitive.

**Tech Stack:** Go 1.26.5, Devbox, Task, standard library only (`crypto/aes`, `crypto/cipher`, `crypto/rand`, `crypto/rsa`, `crypto/sha1`, `crypto/x509`, `bufio`), pinned Node `minecraft-protocol` 1.66.2 for interoperability.

## Global Constraints

- Work in `/home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol`.
- Run every command as `devbox run -- task <name>`. Never call `go` directly.
- The module has **no external dependencies**. `go.mod` has no `require` block and must still have none when this plan is finished.
- Never print, log, wrap, or return a plaintext shared secret. `SharedSecret` redacts itself; do not add a method that formats it.
- Never commit an RSA private key or a fixture containing one. `task secrets` runs `gitleaks` over the tree and will fail. Generate keys inside tests with `rsa.GenerateKey`.
- Pass `context.Context` as the first argument to every blocking public operation. Do not store a caller context in an option or a struct.
- Split validation from application: every check that can fail belongs in a `Validate` path so the matching `Apply` cannot fail after bytes have left the process.
- Leave changes uncommitted only when told to. Each task ends with a commit.
- Never add the `Co-Authored-By` or `Claude-Session` trailer to a commit message.
- Run `devbox run -- task precommit` before every commit.
- Protocol 47 only. Do not add protocol 775 packets or a configuration state.

## File Structure

**New files:**

| File | Responsibility |
| --- | --- |
| `conduit.go` | The switchable transport stage: raw buffering, per-direction ciphers, one mutex |
| `conduit_test.go` | Passthrough, switch point, overrun, concurrency |
| `transport_control.go` | The `TransportControl` interface and its routing contract |
| `wire/java/secret.go` | `SharedSecret`, its redacting formatters, and `NewSharedSecret` |
| `wire/java/secret_test.go` | Redaction across every format verb and through wrapped errors |
| `wire/java/identity.go` | Strict `ParseUUID` and `ParseUsername`, plus `ServerHash` |
| `wire/java/identity_test.go` | Both UUID wire forms, username bounds, every malformed form |
| `wire/java/encryption.go` | `EncryptionControl`, key parsing, PKCS#1 v1.5, `ComputeServerHash` |
| `wire/java/encryption_test.go` | Canonical hash vectors, key parsing, cipher construction |
| `login/negotiator.go` | `Profile`, `Authenticator`, `Negotiator`, driven by descriptor login roles |
| `login/negotiator_test.go` | Success and every failure mode |
| `stream_encryption_test.go` | Encrypted loopback login, Go client against Go server |

**Modified files:**

| File | Change |
| --- | --- |
| `stream.go` | Build the conduit in `NewStream`; add `WithSecretDisclosure` |
| `stream_runtime.go` | Pumps read and write through the conduit; route `TransportControl`; merge the conduit into `Snapshot` |
| `stream_errors.go` | Four new sentinels |
| `observation.go` | `Observation.Redacted`, `ObservationSecret`, `SecretDisclosure`, redaction in `observe` |
| `session.go` | The optional `SensitivePackets` interface |
| `internal/codegen/generator/templates/protocol.go.tmpl` | A `Sensitive` method and a `LoginRole` lookup on the session |
| `generated/java/v1_8/protocol.go` | Regenerated output |
| `interop/node/runner.mjs` | Encrypted scenarios and the `yggdrasil` stub |
| `interop/node_test.go` | Two encrypted interoperability tests |
| `README.md`, `CHANGELOG.md`, `ROADMAP.md`, `../headless-minecraft/MASTER_PLAN.md` | Documentation |

---

### Task 1: The conduit

**Files:**
- Create: `conduit.go`
- Create: `conduit_test.go`
- Modify: `stream.go` (`NewStream`)
- Modify: `stream_runtime.go:126-131` (`readPump`), `stream_runtime.go:187-204` (`writePump`)

**Interfaces:**
- Produces: `type Conduit struct{...}`; `func newConduit(Transport) *Conduit`; `func (*Conduit) Read([]byte) (int, error)`; `func (*Conduit) Write([]byte) (int, error)`; `func (*Conduit) PreFrameReader() *bufio.Reader`; `func (*Conduit) pipeline() map[string]string`. `Stream` gains the field `conduit *Conduit`.

- [ ] **Step 1: Write the failing test**

Create `conduit_test.go`:

```go
package protocol

import (
	"bytes"
	"io"
	"testing"
)

func TestConduitPassesBytesThroughUnencrypted(t *testing.T) {
	source := bytes.NewReader([]byte("hello wire"))
	var sink bytes.Buffer
	conduit := newConduit(Transport{
		Reader:    source,
		Writer:    &sink,
		Interrupt: func() error { return nil },
	})

	read := make([]byte, 10)
	if _, err := io.ReadFull(conduit, read); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(read) != "hello wire" {
		t.Fatalf("read %q, want %q", read, "hello wire")
	}

	if _, err := conduit.Write([]byte("goodbye")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if sink.String() != "goodbye" {
		t.Fatalf("wrote %q, want %q", sink.String(), "goodbye")
	}
}

func TestConduitReportsDisabledEncryptionInPipeline(t *testing.T) {
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})

	if got := conduit.pipeline()["encryption.enabled"]; got != "false" {
		t.Fatalf("encryption.enabled = %q, want %q", got, "false")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./ -run TestConduit`
Expected: FAIL, `undefined: newConduit`.

- [ ] **Step 3: Write the conduit**

Create `conduit.go`. The cipher fields are nil in this task and installed in Task 5; the structure exists now so the pumps only change once.

```go
package protocol

import (
	"bufio"
	"crypto/cipher"
	"io"
	"strconv"
	"sync"
)

// Conduit is the byte-level stage between a transport and a stream's pumps.
//
// It buffers raw transport bytes and transforms them as it hands them out,
// not as it buffers them. That ordering is what makes a mid-stream cipher
// switch safe: the read pump is normally parked waiting for the next frame
// length when the switch happens, and the bytes it eventually receives are
// transformed with the cipher that is active at hand-out time.
//
// Every method is safe for concurrent use by one reader and one writer.
type Conduit struct {
	buffered *bufio.Reader
	writer   io.Writer

	mu      sync.Mutex
	decrypt cipher.Stream
	encrypt cipher.Stream
}

func newConduit(transport Transport) *Conduit {
	return &Conduit{
		buffered: bufio.NewReader(transport.Reader),
		writer:   transport.Writer,
	}
}

// PreFrameReader returns the buffered reader the pre-frame hook inspects.
//
// The hook runs before any frame and therefore before any cipher, so reading
// the buffer directly is identical to reading through the conduit.
func (c *Conduit) PreFrameReader() *bufio.Reader { return c.buffered }

// Read fills p with transport bytes, decrypting them if a cipher is active
// when they are handed out.
func (c *Conduit) Read(p []byte) (int, error) {
	read, err := c.buffered.Read(p)
	if read > 0 {
		c.mu.Lock()
		if c.decrypt != nil {
			c.decrypt.XORKeyStream(p[:read], p[:read])
		}
		c.mu.Unlock()
	}

	return read, err
}

// Write sends p to the transport, encrypting it first if a cipher is active.
// It never retains p and never mutates the caller's buffer.
func (c *Conduit) Write(p []byte) (int, error) {
	c.mu.Lock()
	active := c.encrypt
	if active != nil {
		// A fresh buffer, because the caller owns p and an observation may
		// already hold a view of it.
		encrypted := make([]byte, len(p))
		active.XORKeyStream(encrypted, p)
		p = encrypted
	}
	c.mu.Unlock()

	return c.writer.Write(p)
}

// pipeline reports the conduit's contribution to a stream snapshot.
func (c *Conduit) pipeline() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return map[string]string{
		"encryption.enabled": strconv.FormatBool(c.decrypt != nil),
	}
}
```

- [ ] **Step 4: Build the conduit in NewStream**

In `stream.go`, inside the `stream := &Stream{...}` literal in `NewStream`, add the field after `processing`:

```go
		conduit:    newConduit(transport),
```

And add the field to the `Stream` struct, directly after `transport Transport`:

```go
	// conduit is the byte-level stage both pumps use. It owns the read
	// buffer and the ciphers, so encryption is invisible to framing.
	conduit *Conduit
```

- [ ] **Step 5: Route both pumps through the conduit**

In `stream_runtime.go`, replace the first line of `readPump`:

```go
	reader := bufio.NewReader(s.transport.Reader)
```

with:

```go
	reader := s.conduit
```

and change the `runPreFrame` call directly below it to pass the buffered reader and the conduit writer:

```go
	claimed, err := runPreFrame(ctx, s.preFrame, s.conduit.PreFrameReader(), s.conduit)
```

In `writePump`, replace the transport writer:

```go
			err := s.framer.WriteFrame(s.conduit, job.frame)
```

Then remove the now-unused `"bufio"` import from `stream_runtime.go`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `devbox run -- task test -- ./ -run 'TestConduit|TestStream|TestPreFrame'`
Expected: PASS. The whole existing stream suite must still pass unchanged: the conduit is a transparent passthrough until Task 5.

- [ ] **Step 7: Run the full suite**

Run: `devbox run -- task test`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
devbox run -- task precommit
git add conduit.go conduit_test.go stream.go stream_runtime.go
git commit -m "feat(protocol): route stream pumps through a conduit"
```

---

### Task 2: Transport control routing

**Files:**
- Create: `transport_control.go`
- Modify: `stream_runtime.go:606-618` (`processControl`), and the snapshot handler in `coordinate`
- Test: `stream_transition_test.go` (append)

**Interfaces:**
- Consumes: `*Conduit` from Task 1.
- Produces: `type TransportControl interface { Control; ApplyTransport(*Conduit) error }`. `Stream.Snapshot` now includes `encryption.enabled`.

- [ ] **Step 1: Write the failing test**

Append to `stream_transition_test.go`:

```go
// recordingTransportControl proves the stream routes by interface, not by
// concrete type, and never inspects a control's contents.
type recordingTransportControl struct {
	applied *int
	fail    error
}

func (recordingTransportControl) ControlName() string { return "test.transport" }

func (c recordingTransportControl) ApplyTransport(conduit *Conduit) error {
	if conduit == nil {
		return errors.New("nil conduit")
	}
	if c.fail != nil {
		return c.fail
	}
	*c.applied++

	return nil
}

func TestStreamRoutesTransportControlToConduit(t *testing.T) {
	applied := 0
	stream, _ := startTestStream(t)

	if err := stream.Control(t.Context(), recordingTransportControl{applied: &applied}); err != nil {
		t.Fatalf("control: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied %d times, want 1", applied)
	}
}

func TestStreamReportsTransportControlFailureToCaller(t *testing.T) {
	stream, _ := startTestStream(t)
	sentinel := errors.New("refused")

	err := stream.Control(t.Context(), recordingTransportControl{applied: new(int), fail: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Control error = %v, want %v", err, sentinel)
	}
	if err := stream.Wait(); err != nil && !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("a rejected control must not terminate the stream: %v", err)
	}
}

func TestStreamSnapshotIncludesConduitPipeline(t *testing.T) {
	stream, _ := startTestStream(t)

	snapshot, err := stream.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := snapshot.Pipeline["encryption.enabled"]; got != "false" {
		t.Fatalf("encryption.enabled = %q, want %q", got, "false")
	}
}
```

If `startTestStream` does not exist with that name in `stream_test_helpers_test.go`, use whichever helper the existing tests in that file use to start a stream over an in-memory transport, and keep the assertions as written.

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./ -run 'TestStreamRoutesTransportControl|TestStreamSnapshotIncludesConduit'`
Expected: FAIL, `undefined: Conduit` in the method signature or a missing `encryption.enabled` key.

- [ ] **Step 3: Define the interface**

Create `transport_control.go`:

```go
package protocol

// TransportControl is a control that reconfigures the byte stream below
// framing, such as enabling encryption.
//
// A stream applies it to its conduit instead of its session, which keeps
// transport-level changes out of a type documented as performing no I/O. The
// stream matches on this interface and never on a concrete type, so it still
// does not interpret control contents.
//
// ApplyTransport runs on the coordinator, at a frame boundary, after any
// preceding write has fully reached the transport. Returning an error rejects
// the control and fails the caller without terminating the stream.
type TransportControl interface {
	Control
	ApplyTransport(*Conduit) error
}
```

- [ ] **Step 4: Route the control**

Replace `processControl` in `stream_runtime.go`:

```go
// processControl validates and applies one runtime control. A control that
// reconfigures the transport goes to the conduit; every other control goes to
// the session. An unsupported or rejected control fails the caller without
// stopping the stream.
func (s *Stream) processControl(request *controlRequest) {
	if transport, ok := request.control.(TransportControl); ok {
		request.result <- transport.ApplyTransport(s.conduit)

		return
	}

	if err := s.session.ValidateControl(request.control); err != nil {
		request.result <- err

		return
	}

	s.session.ApplyControl(request.control)
	request.result <- nil
}
```

- [ ] **Step 5: Merge the conduit into the snapshot**

Find the `snapshotRequests` case in `coordinate` and send a merged snapshot rather than `s.session.Snapshot()`. Add this method to `stream_runtime.go`:

```go
// snapshot merges the session's view with the conduit's, so one snapshot
// describes everything a caller can configure at runtime.
func (s *Stream) snapshot() Snapshot {
	merged := s.session.Snapshot()
	pipeline := merged.Pipeline
	if pipeline == nil {
		pipeline = map[string]string{}
	}
	for key, value := range s.conduit.pipeline() {
		pipeline[key] = value
	}

	return NewSnapshot(merged.State, pipeline)
}
```

`Snapshot.Pipeline` is already a clone made by `NewSnapshot`, so mutating it here cannot reach the session. Replace the `s.session.Snapshot()` call in the `snapshotRequests` case with `s.snapshot()`.

Leave the `before` and `after` snapshots in `decodeInbound` and `processWrite` as `s.session.Snapshot()`. Observation records describe the session at a frame boundary, and Task 6 adds a dedicated record for the encryption switch.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `devbox run -- task test -- ./ -run TestStream`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
devbox run -- task precommit
git add transport_control.go stream_runtime.go stream_transition_test.go
git commit -m "feat(protocol): route transport controls to the conduit"
```

---

### Task 3: The shared secret type

**Files:**
- Create: `wire/java/secret.go`
- Create: `wire/java/secret_test.go`

**Interfaces:**
- Produces: `type SharedSecret struct{...}`; `func NewSharedSecret() (SharedSecret, error)`; `func SharedSecretFrom([]byte) (SharedSecret, error)`; `func (SharedSecret) Reveal() []byte`; `func (SharedSecret) Len() int`; `func (SharedSecret) IsZero() bool`.

- [ ] **Step 1: Write the failing test**

Create `wire/java/secret_test.go`:

```go
package java

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewSharedSecretIsSixteenBytes(t *testing.T) {
	secret, err := NewSharedSecret()
	if err != nil {
		t.Fatalf("NewSharedSecret: %v", err)
	}
	if secret.Len() != 16 {
		t.Fatalf("length %d, want 16", secret.Len())
	}
	if secret.IsZero() {
		t.Fatal("a generated secret must not be zero")
	}
}

func TestSharedSecretRevealsAnIndependentCopy(t *testing.T) {
	secret, err := SharedSecretFrom(make([]byte, 16))
	if err != nil {
		t.Fatalf("SharedSecretFrom: %v", err)
	}

	revealed := secret.Reveal()
	revealed[0] = 0xff
	if secret.Reveal()[0] != 0x00 {
		t.Fatal("Reveal must return a copy, not the stored bytes")
	}
}

func TestSharedSecretRejectsWrongLength(t *testing.T) {
	if _, err := SharedSecretFrom(make([]byte, 8)); !errors.Is(err, ErrInvalidSharedSecret) {
		t.Fatalf("error = %v, want ErrInvalidSharedSecret", err)
	}
}

// TestSharedSecretRedactsEveryFormatting is the test that matters. A secret
// that reaches a log through any verb is the failure this type exists to
// prevent.
func TestSharedSecretRedactsEveryFormatting(t *testing.T) {
	raw := []byte("0123456789abcdef")
	secret, err := SharedSecretFrom(raw)
	if err != nil {
		t.Fatalf("SharedSecretFrom: %v", err)
	}

	rendered := []string{
		secret.String(),
		secret.GoString(),
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%s", secret),
		fmt.Sprintf("%q", secret),
		fmt.Sprintf("%d", secret),
		fmt.Sprintf("%x", secret),
		fmt.Sprintf("%X", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%+v", secret),
		fmt.Sprintf("%v", struct{ Secret SharedSecret }{secret}),
		fmt.Sprintf("%v", &secret),
		fmt.Errorf("wrapped: %w", fmt.Errorf("secret %v", secret)).Error(),
	}

	for index, text := range rendered {
		if strings.Contains(text, "0123456789abcdef") {
			t.Fatalf("rendering %d leaked the secret: %s", index, text)
		}
		if !strings.Contains(text, "redacted") {
			t.Fatalf("rendering %d is not marked redacted: %s", index, text)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./wire/java -run TestSharedSecret`
Expected: FAIL, `undefined: NewSharedSecret`.

- [ ] **Step 3: Write the type**

Create `wire/java/secret.go`:

```go
package java

import (
	"crypto/rand"
	"fmt"
)

// SharedSecretBytes is the length of a Java Edition session key.
const SharedSecretBytes = 16

// SharedSecret is a Java Edition session key.
//
// Every formatting method redacts, so the key cannot reach a log line or an
// error by accident. Reveal is the only way to read the bytes, and calling it
// is a deliberate act that shows up in review. The field is an array rather
// than a slice so a copy of the value is a copy of the key, with no aliasing
// back to the caller's buffer.
type SharedSecret struct {
	key [SharedSecretBytes]byte
}

// redacted is what every formatting method renders.
const redacted = "java.SharedSecret(redacted)"

// NewSharedSecret draws a fresh session key from the system source.
func NewSharedSecret() (SharedSecret, error) {
	var secret SharedSecret
	if _, err := rand.Read(secret.key[:]); err != nil {
		return SharedSecret{}, fmt.Errorf("generate shared secret: %w", err)
	}

	return secret, nil
}

// SharedSecretFrom adopts key, which must be exactly SharedSecretBytes long.
// It copies key, so the caller may reuse the buffer.
func SharedSecretFrom(key []byte) (SharedSecret, error) {
	if len(key) != SharedSecretBytes {
		return SharedSecret{}, fmt.Errorf(
			"%w: length %d, want %d",
			ErrInvalidSharedSecret,
			len(key),
			SharedSecretBytes,
		)
	}

	var secret SharedSecret
	copy(secret.key[:], key)

	return secret, nil
}

// Reveal returns an independent copy of the key.
func (s SharedSecret) Reveal() []byte {
	key := make([]byte, SharedSecretBytes)
	copy(key, s.key[:])

	return key
}

// Len returns the key length in bytes.
func (SharedSecret) Len() int { return SharedSecretBytes }

// IsZero reports whether the secret was never populated.
func (s SharedSecret) IsZero() bool {
	return s.key == [SharedSecretBytes]byte{}
}

// String implements fmt.Stringer and redacts.
func (SharedSecret) String() string { return redacted }

// GoString implements fmt.GoStringer and redacts.
func (SharedSecret) GoString() string { return redacted }

// Format implements fmt.Formatter and redacts under every verb, including the
// numeric and hexadecimal verbs that would otherwise render the array.
func (SharedSecret) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(redacted))
}

var (
	_ fmt.Stringer   = SharedSecret{}
	_ fmt.GoStringer = SharedSecret{}
	_ fmt.Formatter  = SharedSecret{}
)
```

Do not add a comparison helper. Nothing in this milestone compares two session keys, and `golangci-lint` fails on unused code. The `crypto/rand` import is the only one this file needs beyond `fmt`.

- [ ] **Step 4: Add the sentinel error**

In `wire/java/errors.go`, add to the `var` block:

```go
	// ErrInvalidSharedSecret reports a session key of the wrong length.
	ErrInvalidSharedSecret = errors.New("invalid shared secret")
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `devbox run -- task test -- ./wire/java -run TestSharedSecret`
Expected: PASS.

Note on `%q`, `%d`, and `%x`: `Format` handles all verbs, so the pointer case and the struct-field case both route through it. If `%q` renders quotes around the placeholder, that is fine; the test only requires the key to be absent and `redacted` to be present.

- [ ] **Step 6: Commit**

```bash
devbox run -- task precommit
git add wire/java/secret.go wire/java/secret_test.go wire/java/errors.go
git commit -m "feat(java): add a self-redacting shared secret type"
```

---

### Task 4: The identity types and the key-exchange primitives

**Files:**
- Create: `wire/java/identity.go`
- Create: `wire/java/identity_test.go`
- Create: `wire/java/encryption.go`
- Create: `wire/java/encryption_test.go`
- Modify: `wire/java/errors.go`

**Interfaces:**
- Consumes: `SharedSecret`, `SharedSecretFrom` from Task 3.
- Produces: `func ParseUUID(string) (UUID, error)`; `func (UUID) String() string`; `func (UUID) IsZero() bool`; `type Username`; `func ParseUsername(string) (Username, error)`; `type ServerHash`; `func ParseServerPublicKey([]byte) (*rsa.PublicKey, error)`; `func EncodeServerPublicKey(*rsa.PublicKey) ([]byte, error)`; `func EncryptToServerKey(*rsa.PublicKey, []byte) ([]byte, error)`; `func DecryptFromServerKey(*rsa.PrivateKey, []byte) ([]byte, error)`; `func VerifyToken([]byte, []byte) error`; `func ComputeServerHash(string, SharedSecret, *rsa.PublicKey) (ServerHash, error)`.

- [ ] **Step 0a: Write the failing identity test**

`java.UUID` already exists as a sixteen-byte array, but nothing parses or
renders one. Protocol 47 carries the login success UUID as a dashed string and
the Mojang session server returns it undashed, so both forms must parse.

Create `wire/java/identity_test.go`:

```go
package java

import (
	"errors"
	"strings"
	"testing"
)

func TestParseUUIDAcceptsBothWireForms(t *testing.T) {
	const dashed = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	const undashed = "069a79f444e94726a5befca90e38aaf5"

	fromDashed, err := ParseUUID(dashed)
	if err != nil {
		t.Fatalf("ParseUUID(dashed): %v", err)
	}
	fromUndashed, err := ParseUUID(undashed)
	if err != nil {
		t.Fatalf("ParseUUID(undashed): %v", err)
	}
	if fromDashed != fromUndashed {
		t.Fatal("the two wire forms must parse to the same value")
	}
	if fromDashed.String() != dashed {
		t.Fatalf("String() = %q, want %q", fromDashed.String(), dashed)
	}
	if fromDashed.IsZero() {
		t.Fatal("a populated UUID must not report zero")
	}
}

func TestParseUUIDIsCaseInsensitive(t *testing.T) {
	upper, err := ParseUUID("069A79F4-44E9-4726-A5BE-FCA90E38AAF5")
	if err != nil {
		t.Fatalf("ParseUUID: %v", err)
	}
	lower, err := ParseUUID("069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err != nil {
		t.Fatalf("ParseUUID: %v", err)
	}
	if upper != lower {
		t.Fatal("parsing must be case insensitive")
	}
	if upper.String() != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Fatalf("String() must render lowercase, got %q", upper.String())
	}
}

func TestParseUUIDRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "too short", text: "069a79f4"},
		{name: "too long", text: "069a79f4-44e9-4726-a5be-fca90e38aaf5a"},
		{name: "non-hex", text: "069a79f4-44e9-4726-a5be-fca90e38aazz"},
		{name: "dashes in the wrong places", text: "069a79f4-44e94-726-a5be-fca90e38aaf5"},
		{name: "leading space", text: " 069a79f4-44e9-4726-a5be-fca90e38aaf5"},
		{name: "braced", text: "{069a79f4-44e9-4726-a5be-fca90e38aaf5}"},
		{name: "urn form", text: "urn:uuid:069a79f4-44e9-4726-a5be-fca90e38aaf5"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParseUUID(testCase.text); !errors.Is(err, ErrInvalidUUID) {
				t.Fatalf("ParseUUID(%q) error = %v, want ErrInvalidUUID", testCase.text, err)
			}
		})
	}
}

func TestParseUsernameAcceptsRealNames(t *testing.T) {
	cases := []string{
		"Notch",
		"jeb_",
		"a",
		"sixteencharacter",
		"Ünïcödé",  // offline and modded servers issue these
		"player.one",
	}

	for _, text := range cases {
		name, err := ParseUsername(text)
		if err != nil {
			t.Fatalf("ParseUsername(%q): %v", text, err)
		}
		if name.String() != text {
			t.Fatalf("String() = %q, want %q", name.String(), text)
		}
	}
}

func TestParseUsernameRejectsInvalidNames(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "too long", text: "seventeencharact"[:16] + "x"},
		{name: "newline", text: "player\nname"},
		{name: "null byte", text: "player\x00"},
		{name: "invalid UTF-8", text: "player\xff"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParseUsername(testCase.text); !errors.Is(err, ErrInvalidUsername) {
				t.Fatalf("ParseUsername(%q) error = %v, want ErrInvalidUsername", testCase.text, err)
			}
		})
	}
}

func TestParseUsernameBoundsBytesNotRunes(t *testing.T) {
	// Nine two-byte runes are nine characters but eighteen bytes, and the
	// wire format bounds bytes.
	if _, err := ParseUsername(strings.Repeat("ü", 9)); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("error = %v, want ErrInvalidUsername", err)
	}
}

func TestZeroUUIDReportsZero(t *testing.T) {
	var zero UUID
	if !zero.IsZero() {
		t.Fatal("the zero value must report zero")
	}
	if zero.String() != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("String() = %q", zero.String())
	}
}
```

- [ ] **Step 0b: Run the test to verify it fails**

Run: `devbox run -- task test -- ./wire/java -run 'TestParseUUID|TestZeroUUID|TestParseUsername'`
Expected: FAIL, `undefined: ParseUUID`.

- [ ] **Step 0c: Write the identity types**

Create `wire/java/identity.go`:

```go
package java

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// uuidDashPositions are the indices a dashed UUID puts its separators at.
var uuidDashPositions = [4]int{8, 13, 18, 23}

// ParseUUID reads the two forms a Java Edition login can carry: the dashed
// thirty-six character form that the login success packet sends, and the
// undashed thirty-two character form that the session server returns.
//
// It is deliberately strict. Braced and URN forms, surrounding whitespace, and
// partial dashing are rejected, because a login is the one exchange where the
// peer is entirely unauthenticated and a permissive parser there turns a
// malformed identity into a valid-looking one.
func ParseUUID(text string) (UUID, error) {
	var compact string

	switch len(text) {
	case 32:
		compact = text
	case 36:
		for _, position := range uuidDashPositions {
			if text[position] != '-' {
				return UUID{}, fmt.Errorf(
					"%w: expected a separator at index %d",
					ErrInvalidUUID,
					position,
				)
			}
		}
		compact = strings.ReplaceAll(text, "-", "")
		if len(compact) != 32 {
			return UUID{}, fmt.Errorf("%w: misplaced separators", ErrInvalidUUID)
		}
	default:
		return UUID{}, fmt.Errorf("%w: length %d, want 32 or 36", ErrInvalidUUID, len(text))
	}

	var value UUID
	if _, err := hex.Decode(value[:], []byte(strings.ToLower(compact))); err != nil {
		return UUID{}, fmt.Errorf("%w: %w", ErrInvalidUUID, err)
	}

	return value, nil
}

// String renders the dashed, lowercase form.
func (u UUID) String() string {
	encoded := hex.EncodeToString(u[:])

	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] +
		"-" + encoded[16:20] + "-" + encoded[20:32]
}

// IsZero reports whether the UUID is the nil UUID.
func (u UUID) IsZero() bool { return u == UUID{} }

// MaxUsernameBytes is the longest username the protocol allows.
const MaxUsernameBytes = 16

// Username is a validated Java Edition account name.
//
// The field is unexported so ParseUsername is the only way to build a
// non-zero one. A defined string type would be convertible, and
// Username("bad\nname") compiling anywhere would make the validation a
// convention rather than a guarantee. The struct is still comparable and
// still usable as a map key.
type Username struct {
	name string
}

// ParseUsername validates a peer-supplied account name.
//
// It enforces the rules that hold everywhere: non-empty, at most
// MaxUsernameBytes, valid UTF-8, and no control characters. It deliberately
// does not enforce the [a-zA-Z0-9_] charset that Mojang applies to new
// accounts, because offline-mode and modded servers legitimately issue names
// outside it and rejecting those breaks real connections while preventing
// nothing.
func ParseUsername(text string) (Username, error) {
	if text == "" {
		return Username{}, fmt.Errorf("%w: empty", ErrInvalidUsername)
	}
	if len(text) > MaxUsernameBytes {
		return Username{}, fmt.Errorf(
			"%w: %d bytes, limit %d",
			ErrInvalidUsername,
			len(text),
			MaxUsernameBytes,
		)
	}
	if !utf8.ValidString(text) {
		return Username{}, fmt.Errorf("%w: not valid UTF-8", ErrInvalidUsername)
	}
	for _, character := range text {
		if unicode.IsControl(character) {
			return Username{}, fmt.Errorf("%w: contains a control character", ErrInvalidUsername)
		}
	}

	return Username{name: text}, nil
}

// String returns the account name.
func (u Username) String() string { return u.name }

// IsZero reports whether the username was never parsed.
func (u Username) IsZero() bool { return u.name == "" }

// ServerHash is the login hash a client presents to the session server and a
// server verifies. ComputeServerHash is the only way to obtain one.
//
// It is a type rather than a string because of the signature it appears in:
// Verify takes a username and a hash, and as two adjacent strings, swapping
// them compiles, survives review, and fails at runtime as an authentication
// error that looks like a rejected account.
type ServerHash struct {
	hash string
}

// String returns the hash in the form the session server expects.
func (h ServerHash) String() string { return h.hash }

// IsZero reports whether the hash was never computed.
func (h ServerHash) IsZero() bool { return h.hash == "" }
```

Add `"unicode"` and `"unicode/utf8"` to the `wire/java/identity.go` imports alongside `"encoding/hex"`, `"fmt"`, and `"strings"`.

Note the length rule: `MaxUsernameBytes` bounds bytes, not runes, because the wire format bounds bytes. A sixteen-rune name in a multi-byte script is correctly rejected.

Note on `hex.Decode`: it rejects non-hex bytes, which covers the `non-hex` case, and `strings.ToLower` handles the uppercase form. Do not add a manual hex loop.

- [ ] **Step 0d: Add the sentinel error**

In `wire/java/errors.go`, add to the `var` block:

```go
	// ErrInvalidUUID reports a UUID that is not one of the two accepted wire
	// forms.
	ErrInvalidUUID = errors.New("invalid UUID")
	// ErrInvalidUsername reports an account name that breaks the protocol's
	// rules.
	ErrInvalidUsername = errors.New("invalid username")
```

- [ ] **Step 0e: Run the UUID tests**

Run: `devbox run -- task test -- ./wire/java -run 'TestParseUUID|TestZeroUUID|TestParseUsername'`
Expected: PASS.

- [ ] **Step 0f: Commit the identity types separately**

```bash
devbox run -- task precommit
git add wire/java/identity.go wire/java/identity_test.go wire/java/errors.go
git commit -m "feat(java): add strict identity types for login"
```

- [ ] **Step 1: Write the failing test**

Create `wire/java/encryption_test.go`:

```go
package java

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"errors"
	"testing"
)

// The three canonical server-hash vectors. Java renders a negative SHA-1
// digest as the negation of its twos complement, with no zero padding, which
// is the only unusual part of this function.
func TestComputeServerHashMatchesCanonicalVectors(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	cases := []struct {
		name   string
		digest string
	}{
		{name: "Notch", digest: "4ed1f46bbe04bc756bcb17c0c7ce3e4632f06a48"},
		{name: "jeb_", digest: "-7c9d5b0044c130109a5d7b5fb5c317c02b4e28c1"},
		{name: "simon", digest: "88e16a1019277b15d58faf0541e11910eb756f6"},
	}

	// Each vector is the Java rendering of SHA-1 over the name's bytes, so
	// they pin the rendering rather than the concatenation. "jeb_" is the one
	// that matters: its digest has the high bit set, so a naive
	// implementation renders it as a large positive number instead of a
	// negative one.
	for _, testCase := range cases {
		sum := sha1.Sum([]byte(testCase.name))
		got := javaDigest(sum[:])
		if got != testCase.digest {
			t.Fatalf("javaDigest(sha1(%q)) = %q, want %q", testCase.name, got, testCase.digest)
		}
	}

	// ComputeServerHash must be deterministic for the same inputs and differ when
	// any input differs.
	secret, err := SharedSecretFrom([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("SharedSecretFrom: %v", err)
	}
	first, err := ComputeServerHash("serverid", secret, &key.PublicKey)
	if err != nil {
		t.Fatalf("ComputeServerHash: %v", err)
	}
	again, err := ComputeServerHash("serverid", secret, &key.PublicKey)
	if err != nil {
		t.Fatalf("ComputeServerHash: %v", err)
	}
	if first != again {
		t.Fatalf("ComputeServerHash is not deterministic: %q then %q", first, again)
	}
	other, err := ComputeServerHash("different", secret, &key.PublicKey)
	if err != nil {
		t.Fatalf("ComputeServerHash: %v", err)
	}
	if first == other {
		t.Fatal("ComputeServerHash ignored the server ID")
	}
}

func TestServerPublicKeyRoundTrips(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	encoded, err := EncodeServerPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("EncodeServerPublicKey: %v", err)
	}
	parsed, err := ParseServerPublicKey(encoded)
	if err != nil {
		t.Fatalf("ParseServerPublicKey: %v", err)
	}
	if !parsed.Equal(&key.PublicKey) {
		t.Fatal("round trip changed the key")
	}
}

func TestParseServerPublicKeyRejectsGarbage(t *testing.T) {
	if _, err := ParseServerPublicKey([]byte("not a key")); !errors.Is(err, ErrInvalidServerKey) {
		t.Fatalf("error = %v, want ErrInvalidServerKey", err)
	}
	if _, err := ParseServerPublicKey(nil); !errors.Is(err, ErrInvalidServerKey) {
		t.Fatalf("error = %v, want ErrInvalidServerKey", err)
	}
}

func TestServerKeyEncryptionRoundTrips(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	plaintext := []byte("0123456789abcdef")
	ciphertext, err := EncryptToServerKey(&key.PublicKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}
	if string(ciphertext) == string(plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	recovered, err := DecryptFromServerKey(key, ciphertext)
	if err != nil {
		t.Fatalf("DecryptFromServerKey: %v", err)
	}
	if string(recovered) != string(plaintext) {
		t.Fatalf("recovered %q, want %q", recovered, plaintext)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./wire/java -run 'TestComputeServerHash|TestServerPublicKey|TestParseServerPublicKey|TestServerKeyEncryption'`
Expected: FAIL, `undefined: javaDigest`.

- [ ] **Step 3: Write the primitives**

Create `wire/java/encryption.go`:

```go
package java

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"fmt"
	"math/big"
)

// ParseServerPublicKey reads the DER SubjectPublicKeyInfo encoding that a
// Java server sends in its encryption request.
func ParseServerPublicKey(der []byte) (*rsa.PublicKey, error) {
	if len(der) == 0 {
		return nil, fmt.Errorf("%w: empty key", ErrInvalidServerKey)
	}

	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidServerKey, err)
	}

	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: key is %T, want RSA", ErrInvalidServerKey, parsed)
	}

	return key, nil
}

// EncodeServerPublicKey produces the encoding ParseServerPublicKey reads. A
// server uses it to build its encryption request.
func EncodeServerPublicKey(key *rsa.PublicKey) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrInvalidServerKey)
	}

	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidServerKey, err)
	}

	return der, nil
}

// EncryptToServerKey encrypts plaintext with PKCS#1 v1.5, which is the scheme
// the Java client uses for the shared secret and the verify token.
func EncryptToServerKey(key *rsa.PublicKey, plaintext []byte) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrInvalidServerKey)
	}

	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, key, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt to server key: %w", err)
	}

	return ciphertext, nil
}

// DecryptFromServerKey is the server side of EncryptToServerKey.
func DecryptFromServerKey(key *rsa.PrivateKey, ciphertext []byte) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrInvalidServerKey)
	}

	plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt from server key: %w", err)
	}

	return plaintext, nil
}

// ComputeServerHash builds the login hash a client presents to the session
// server and a server verifies. It is SHA-1 over the server ID, the session
// key, and the encoded public key, rendered the way Java renders a BigInteger.
func ComputeServerHash(serverID string, secret SharedSecret, key *rsa.PublicKey) (ServerHash, error) {
	encoded, err := EncodeServerPublicKey(key)
	if err != nil {
		return ServerHash{}, err
	}

	digest := sha1.New()
	digest.Write([]byte(serverID))
	digest.Write(secret.Reveal())
	digest.Write(encoded)

	return ServerHash{hash: javaDigest(digest.Sum(nil))}, nil
}

// VerifyToken compares the token a server sent with the one a client returned
// after decryption. It compares in constant time, because a server that leaks
// the comparison position leaks the token.
//
// It is the server half of the exchange. This package supplies it so a server
// implementation does not reimplement the comparison and get it wrong.
func VerifyToken(expected, returned []byte) error {
	if len(expected) == 0 {
		return fmt.Errorf("%w: no expected token", ErrVerifyTokenMismatch)
	}
	if subtle.ConstantTimeCompare(expected, returned) != 1 {
		return ErrVerifyTokenMismatch
	}

	return nil
}

// javaDigest renders bytes as Java's BigInteger(1 signed byte array) does:
// as a signed, base-16, unpadded integer. A digest whose leading bit is set is
// negative, and Java prints it as a minus sign followed by the magnitude of
// its twos complement. Every other implementation of this function in the
// wild gets that case wrong, which is why the canonical vectors pin it.
func javaDigest(sum []byte) string {
	value := new(big.Int).SetBytes(sum)
	if len(sum) > 0 && sum[0]&0x80 != 0 {
		// Reinterpret the bytes as a twos-complement negative number.
		value.Sub(value, new(big.Int).Lsh(big.NewInt(1), uint(len(sum)*8)))
	}

	return value.Text(16)
}
```

- [ ] **Step 4: Add the sentinel error**

In `wire/java/errors.go`, add to the `var` block:

```go
	// ErrInvalidServerKey reports a server public key that is unparseable or
	// is not RSA.
	ErrInvalidServerKey = errors.New("invalid server public key")
	// ErrVerifyTokenMismatch reports a client that did not return the verify
	// token the server sent.
	ErrVerifyTokenMismatch = errors.New("verify token mismatch")
```

Add `"crypto/subtle"` to the `wire/java/encryption.go` imports.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `devbox run -- task test -- ./wire/java -run 'TestComputeServerHash|TestServerPublicKey|TestParseServerPublicKey|TestServerKeyEncryption'`
Expected: PASS.

If a canonical vector fails, the bug is in `javaDigest`, not in the hashing. `big.Int.Text(16)` already omits leading zeros and prefixes a minus sign, which is exactly Java's rendering. Do not add padding.

- [ ] **Step 6: Commit**

```bash
devbox run -- task precommit
git add wire/java/encryption.go wire/java/encryption_test.go wire/java/errors.go
git commit -m "feat(java): add key-exchange primitives and the server hash"
```

---

### Task 5: The encryption control and the switch point

**Files:**
- Modify: `wire/java/encryption.go`
- Modify: `conduit.go`
- Modify: `stream_errors.go`
- Test: `wire/java/encryption_test.go` (append), `conduit_test.go` (append)

**Interfaces:**
- Consumes: `Conduit` (Task 1), `TransportControl` (Task 2), `SharedSecret` (Task 3).
- Produces: `type EncryptionControl struct{ Secret SharedSecret }` implementing `protocol.TransportControl`; `func (*Conduit) EnableEncryption(decrypt, encrypt cipher.Stream) error`; `protocol.ErrEncryptionOverrun`; `protocol.ErrEncryptionEnabled`.

- [ ] **Step 1: Write the failing conduit test**

Append to `conduit_test.go`:

```go
import (
	"crypto/aes"
	"crypto/cipher"
	// keep the existing imports
)

// testCiphers builds the same CFB8 pair on both sides of a loopback pipe.
func testCiphers(t *testing.T, key []byte) (cipher.Stream, cipher.Stream) {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}

	return cipher.NewCFBDecrypter(block, key), cipher.NewCFBEncrypter(block, key)
}

func TestConduitEncryptsAfterTheSwitch(t *testing.T) {
	key := []byte("0123456789abcdef")
	var sink bytes.Buffer
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    &sink,
		Interrupt: func() error { return nil },
	})

	if _, err := conduit.Write([]byte("clear")); err != nil {
		t.Fatalf("write before switch: %v", err)
	}

	decrypt, encrypt := testCiphers(t, key)
	if err := conduit.EnableEncryption(decrypt, encrypt); err != nil {
		t.Fatalf("EnableEncryption: %v", err)
	}
	if _, err := conduit.Write([]byte("secret")); err != nil {
		t.Fatalf("write after switch: %v", err)
	}

	written := sink.Bytes()
	if string(written[:5]) != "clear" {
		t.Fatalf("bytes before the switch were transformed: %q", written[:5])
	}
	if string(written[5:]) == "secret" {
		t.Fatal("bytes after the switch were not encrypted")
	}

	// Decrypt with the matching direction to prove the ciphertext is real.
	peerDecrypt, _ := testCiphers(t, key)
	recovered := make([]byte, len(written)-5)
	peerDecrypt.XORKeyStream(recovered, written[5:])
	if string(recovered) != "secret" {
		t.Fatalf("recovered %q, want %q", recovered, "secret")
	}

	if got := conduit.pipeline()["encryption.enabled"]; got != "true" {
		t.Fatalf("encryption.enabled = %q, want %q", got, "true")
	}
}

func TestConduitRejectsSwitchWithBufferedCiphertext(t *testing.T) {
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader([]byte("bytes the peer sent too early")),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})

	// Force the buffer to fill, which is what a peer writing past the switch
	// point causes in practice.
	if _, err := conduit.buffered.Peek(1); err != nil {
		t.Fatalf("peek: %v", err)
	}

	decrypt, encrypt := testCiphers(t, []byte("0123456789abcdef"))
	err := conduit.EnableEncryption(decrypt, encrypt)
	if !errors.Is(err, ErrEncryptionOverrun) {
		t.Fatalf("error = %v, want ErrEncryptionOverrun", err)
	}
	if got := conduit.pipeline()["encryption.enabled"]; got != "false" {
		t.Fatal("a rejected switch must leave encryption disabled")
	}
}

func TestConduitRejectsASecondSwitch(t *testing.T) {
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})

	decrypt, encrypt := testCiphers(t, []byte("0123456789abcdef"))
	if err := conduit.EnableEncryption(decrypt, encrypt); err != nil {
		t.Fatalf("first EnableEncryption: %v", err)
	}

	again, alsoAgain := testCiphers(t, []byte("fedcba9876543210"))
	if err := conduit.EnableEncryption(again, alsoAgain); !errors.Is(err, ErrEncryptionEnabled) {
		t.Fatalf("error = %v, want ErrEncryptionEnabled", err)
	}
}
```

Add `"errors"` to the test file imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./ -run TestConduit`
Expected: FAIL, `conduit.EnableEncryption undefined`.

- [ ] **Step 3: Add the sentinels**

In `stream_errors.go`, add to the `var` block:

```go
	// ErrEncryptionOverrun reports that a peer sent bytes past the point where
	// encryption began. The buffered bytes were read as plaintext and cannot
	// be recovered, so the stream cannot continue.
	ErrEncryptionOverrun = errors.New("peer sent bytes past the encryption switch point")
	// ErrEncryptionEnabled reports a second attempt to enable encryption.
	// Java Edition has no rekeying, so this is a caller bug.
	ErrEncryptionEnabled = errors.New("encryption is already enabled")
	// ErrEncryptionUnavailable reports an encryption control applied to a
	// stream whose conduit has already terminated.
	ErrEncryptionUnavailable = errors.New("encryption is unavailable")
```

- [ ] **Step 4: Implement the switch**

Add to `conduit.go`:

```go
// EnableEncryption installs the per-direction ciphers.
//
// It refuses when the read buffer already holds unread bytes. Those bytes
// arrived before the switch and would have been handed out as plaintext, so
// accepting the switch would corrupt the very next frame with no way to tell
// why. Failing here names the cause at the cause.
func (c *Conduit) EnableEncryption(decrypt, encrypt cipher.Stream) error {
	if decrypt == nil || encrypt == nil {
		return fmt.Errorf("%w: nil cipher", ErrEncryptionUnavailable)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.decrypt != nil || c.encrypt != nil {
		return ErrEncryptionEnabled
	}
	if buffered := c.buffered.Buffered(); buffered > 0 {
		return fmt.Errorf("%w: %d unread bytes", ErrEncryptionOverrun, buffered)
	}

	c.decrypt = decrypt
	c.encrypt = encrypt

	return nil
}
```

Add `"fmt"` to the `conduit.go` imports.

- [ ] **Step 5: Run the conduit tests**

Run: `devbox run -- task test -- ./ -run TestConduit`
Expected: PASS.

- [ ] **Step 6: Write the failing control test**

Append to `wire/java/encryption_test.go`:

```go
func TestEncryptionControlIsATransportControl(t *testing.T) {
	secret, err := SharedSecretFrom([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("SharedSecretFrom: %v", err)
	}

	control := EncryptionControl{Secret: secret}
	if control.ControlName() != "java.encryption" {
		t.Fatalf("ControlName = %q", control.ControlName())
	}

	var _ protocol.TransportControl = control
	var _ protocol.SecretDisclosure = control

	if control.SecretLabel() != "java.session-key" {
		t.Fatalf("SecretLabel = %q, want %q", control.SecretLabel(), "java.session-key")
	}

	material := control.DisclosedSecret()
	if string(material) != "0123456789abcdef" {
		t.Fatal("DisclosedSecret must return the key for a disclosing capture")
	}

	// The material must be a copy: a sink that mutates it must not reach the
	// live cipher configuration.
	material[0] = 0xff
	if control.DisclosedSecret()[0] == 0xff {
		t.Fatal("DisclosedSecret must return an independent copy")
	}
}

func TestEncryptionControlRejectsAnEmptySecret(t *testing.T) {
	control := EncryptionControl{}
	if err := control.ApplyTransport(nil); !errors.Is(err, ErrInvalidSharedSecret) {
		t.Fatalf("error = %v, want ErrInvalidSharedSecret", err)
	}
}
```

Add the `protocol` import to the test file. `SecretDisclosure` is defined in Task 6; if this task runs first, add the interface to `observation.go` now with the two-line definition shown there and leave the redaction work to Task 6.

- [ ] **Step 7: Implement the control**

Append to `wire/java/encryption.go`:

```go
// EncryptionControl enables AES-128/CFB8 on a stream's conduit.
//
// It is a protocol.TransportControl rather than a session control, because the
// cipher covers the frame length prefix that the session never sees. It is not
// proposed by a session either: no packet carries the plaintext key, so the
// caller applies it through Stream.Control once the key exchange is complete.
// Stream.Write returns only after the frame has reached the transport, and
// Control queues behind it on the same coordinator, so applying it directly
// after the write cannot encrypt the response packet itself.
type EncryptionControl struct {
	Secret SharedSecret
}

// ControlName implements protocol.Control.
func (EncryptionControl) ControlName() string { return "java.encryption" }

// SecretLabel implements protocol.SecretDisclosure. A stream calls it on every
// switch, disclosing or not, so a redacted capture still says what kind of
// material was installed.
//
// The label names the kind of material rather than describing one connection,
// because it is written into durable captures that later tooling has to
// interpret without knowing which milestone produced them.
func (EncryptionControl) SecretLabel() string { return "java.session-key" }

// DisclosedSecret implements protocol.SecretDisclosure. A stream calls it only
// when the developer opted into disclosure, so the key is never materialized
// on the default path. The returned slice is a copy the caller may retain.
func (c EncryptionControl) DisclosedSecret() []byte { return c.Secret.Reveal() }

// ApplyTransport implements protocol.TransportControl. Java uses the key as
// its own initialization vector in both directions.
func (c EncryptionControl) ApplyTransport(conduit *protocol.Conduit) error {
	if c.Secret.IsZero() {
		return fmt.Errorf("%w: empty session key", ErrInvalidSharedSecret)
	}

	key := c.Secret.Reveal()
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("build session cipher: %w", err)
	}

	return conduit.EnableEncryption(
		cipher.NewCFBDecrypter(block, key),
		cipher.NewCFBEncrypter(block, key),
	)
}

var (
	_ protocol.TransportControl = EncryptionControl{}
	_ protocol.SecretDisclosure = EncryptionControl{}
)
```

Add `"crypto/aes"`, `"crypto/cipher"`, and the `protocol` import to `wire/java/encryption.go`.

Note the nil-conduit path: `ApplyTransport(nil)` reaches the empty-secret check first and returns before dereferencing, which is what the test asserts. Do not add a nil check that would mask it.

- [ ] **Step 8: Run the tests**

Run: `devbox run -- task test -- ./ ./wire/java -run 'TestConduit|TestEncryptionControl'`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
devbox run -- task precommit
git add conduit.go conduit_test.go stream_errors.go wire/java/encryption.go wire/java/encryption_test.go
git commit -m "feat(java): enable AES-CFB8 through a transport control"
```

---

### Task 6: Redaction and disclosure

**Files:**
- Modify: `observation.go`
- Modify: `session.go`
- Modify: `stream.go` (`WithSecretDisclosure`)
- Modify: `stream_runtime.go` (`decodeInbound`, `observeOutbound`, `processControl`)
- Modify: `internal/codegen/generator/templates/protocol.go.tmpl`
- Modify: `generated/java/v1_8/protocol.go` (regenerated)
- Test: `observation_test.go` (append)

**Interfaces:**
- Consumes: `EncryptionControl` (Task 5).
- Produces: `Observation.Redacted bool`; `Observation.Secret *SecretMetadata`; `ObservationSecret ObservationStage`; `type SecretDisclosure interface{ SecretLabel() string; DisclosedSecret() []byte }`; `type SensitivePackets interface{ Sensitive(Packet) bool }`; `func WithSecretDisclosure(reason string) StreamOption`.

- [ ] **Step 1: Write the failing test**

Append to `observation_test.go`:

```go
func TestObservationsRedactSensitivePacketsByDefault(t *testing.T) {
	records := collectObservations(t, nil, sensitiveTestPacket())

	packetRecord := findRecord(t, records, ObservationPacket)
	if !packetRecord.Redacted {
		t.Fatal("a sensitive packet record must be marked redacted")
	}
	if len(packetRecord.Bytes) != 0 {
		t.Fatalf("a redacted record must carry no body, got %d bytes", len(packetRecord.Bytes))
	}

	rawRecord := findRecord(t, records, ObservationRawFrame)
	if rawRecord.Redacted {
		t.Fatal("raw frame records are never redacted")
	}
	if len(rawRecord.Bytes) == 0 {
		t.Fatal("a raw frame record must carry the exact wire bytes")
	}
}

func TestObservationsDiscloseSensitivePacketsWhenAsked(t *testing.T) {
	records := collectObservations(t,
		[]StreamOption{WithSecretDisclosure("interoperability debugging")},
		sensitiveTestPacket(),
	)

	packetRecord := findRecord(t, records, ObservationPacket)
	if packetRecord.Redacted {
		t.Fatal("disclosure must clear the redacted flag")
	}
	if len(packetRecord.Bytes) == 0 {
		t.Fatal("disclosure must carry the real body")
	}
}

func TestSecretRecordKeepsItsLabelWhenRedacted(t *testing.T) {
	records := collectObservations(t, nil, encryptionTestControl(t))

	record := findRecord(t, records, ObservationSecret)
	if !record.Redacted {
		t.Fatal("a secret record must be redacted by default")
	}
	if len(record.Bytes) != 0 {
		t.Fatal("a redacted secret record must carry no material")
	}
	if record.Secret == nil || record.Secret.Label == "" {
		t.Fatal("a redacted secret record must still name the kind of material")
	}
}

func TestSecretRecordCarriesMaterialUnderDisclosure(t *testing.T) {
	records := collectObservations(t,
		[]StreamOption{WithSecretDisclosure("interoperability debugging")},
		encryptionTestControl(t),
	)

	record := findRecord(t, records, ObservationSecret)
	if record.Redacted {
		t.Fatal("disclosure must clear the redacted flag")
	}
	if len(record.Bytes) == 0 {
		t.Fatal("disclosure must carry the material")
	}
	if record.Secret == nil {
		t.Fatal("a secret record must always name its material")
	}
}

func TestSecretDisclosureRequiresAReason(t *testing.T) {
	_, err := NewStream(newTestSession(t), newTestTransport(t), WithSecretDisclosure(""))
	if !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("error = %v, want ErrInvalidStream", err)
	}
}
```

Write `collectObservations`, `findRecord`, `sensitiveTestPacket`, `encryptionTestControl`, `newTestSession`, and `newTestTransport` as small helpers in the same file. `collectObservations` takes stream options and then either a packet to write or a control to apply, so both shapes above work; give it a small variadic or two thin wrappers, whichever reads better. `encryptionTestControl` returns a `TransportControl` that also implements `SecretDisclosure`, defined locally in the test rather than importing `wire/java`, because `observation_test.go` is in package `protocol` and importing `wire/java` would be an import cycle, reusing whatever fake session and in-memory transport `stream_test_helpers_test.go` already provides. `sensitiveTestPacket` must be a packet the fake session reports as sensitive; give the fake session a `sensitive map[int32]bool` field and a `Sensitive(Packet) bool` method.

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./ -run 'TestObservations|TestSecretDisclosure'`
Expected: FAIL, `packetRecord.Redacted undefined`.

- [ ] **Step 3: Extend the observation types**

In `observation.go`, add the stage constant to the existing `const` block:

```go
	// ObservationSecret is recorded when secret material is installed on the
	// conduit. It carries the key only under WithSecretDisclosure; otherwise
	// it marks the switch point and nothing more, so a capture always shows
	// when encryption began.
	ObservationSecret ObservationStage = "secret"
```

Add the field to `Observation`, after `Bytes`:

```go
	// Redacted reports that Bytes was withheld. It is set per record rather
	// than inferred from stream configuration, so a sink never has to guess
	// whether it holds a real body or a placeholder.
	Redacted bool
	// Secret is present on ObservationSecret records and names the kind of
	// material the record describes.
	Secret *SecretMetadata
```

Add the disclosure interface:

```go
// SecretMetadata names the kind of secret material a record carries. A capture
// with more than one kind of secret in it is ambiguous without this, and a
// discriminator cannot be added retroactively to captures already written.
type SecretMetadata struct {
	Label string
}

// SecretDisclosure is implemented by a TransportControl that carries secret
// material a disclosing capture needs in order to be decryptable later.
//
// The two methods are separate because a stream needs them at different times.
// SecretLabel is called on every switch, so a redacted capture still records
// what kind of material was installed and when. DisclosedSecret is called only
// when the developer passed WithSecretDisclosure, so the default path never
// materializes a key it would immediately discard. It must return a copy the
// caller may retain.
//
// The stream interprets neither value. It copies both into the record and
// hands it to the sink.
type SecretDisclosure interface {
	SecretLabel() string
	DisclosedSecret() []byte
}
```

- [ ] **Step 4: Add the sensitivity interface**

In `session.go`, after the `Session` interface:

```go
// SensitivePackets reports packets whose bodies must be withheld from
// observations. A session that does not implement it has no sensitive packets.
//
// It is an optional interface rather than a Session method so that adding a
// sensitive packet in one protocol version does not change the contract every
// session must satisfy.
type SensitivePackets interface {
	Sensitive(Packet) bool
}
```

- [ ] **Step 5: Add the stream option**

In `stream.go`, add the fields to `Stream` next to `sink`:

```go
	// disclosureReason is empty unless the developer opted into disclosure.
	disclosureReason string
```

And the option:

```go
// WithSecretDisclosure turns off redaction of secret material in observations.
//
// A capture then contains the session key and the full key-exchange bodies,
// which makes it as sensitive as the account it belongs to. Use it to debug
// an encrypted connection, and treat the resulting capture as a credential.
//
// reason must be non-empty. It is recorded on the stream so a capture can
// state why it is unredacted.
func WithSecretDisclosure(reason string) StreamOption {
	return func(stream *Stream) error {
		if reason == "" {
			return fmt.Errorf("%w: secret disclosure needs a reason", ErrInvalidStream)
		}
		stream.disclosureReason = reason

		return nil
	}
}
```

- [ ] **Step 6: Redact at the observation points**

`observe` is already at seven parameters and this task needs two more, so convert it to take an input struct. It is unexported, so there is no API impact and the call sites become readable.

In `observation.go`, add above `observe`:

```go
// observationInput is what one call to observe describes. It is a struct
// rather than a parameter list because a nine-argument call whose arguments
// are three snapshots, two pointers, and two booleans is unreadable at the
// call site and easy to transpose.
type observationInput struct {
	direction Direction
	stage     ObservationStage
	frame     uint64
	before    Snapshot
	after     Snapshot
	packet    *PacketMetadata
	secret    *SecretMetadata
	payload   []byte
	redacted  bool
}
```

Change the signature to `func (s *Stream) observe(input observationInput) error`, and build the record from it:

```go
	body := input.payload
	if input.redacted {
		body = nil
	}
	charge := len(body)
```

The record then sets `Bytes: bytes.Clone(body)`, `Redacted: input.redacted`, `Packet: input.packet`, and `Secret: input.secret`, with the remaining fields taken from `input` as before.

Add the helper next to `packetMetadata`:

```go
// sensitive reports whether the session withholds this packet's body.
func (s *Stream) sensitive(packet Packet) bool {
	if s.disclosureReason != "" {
		return false
	}

	marker, ok := s.session.(SensitivePackets)

	return ok && marker.Sensitive(packet)
}
```

In `stream_runtime.go`, rewrite the four existing `observe` calls as struct literals. The two `ObservationRawFrame` calls set `redacted: false`; the two `ObservationPacket` calls set `redacted: s.sensitive(packet)`. For example, the raw record in `decodeInbound` becomes:

```go
	if err := s.observe(observationInput{
		direction: s.session.Inbound(),
		stage:     ObservationRawFrame,
		frame:     frameID,
		before:    before,
		after:     before,
		payload:   frame.WireBytes(),
	}); err != nil {
		return nil, err
	}
```

and the packet record in `decodeInbound` becomes:

```go
	if err := s.observe(observationInput{
		direction: s.session.Inbound(),
		stage:     ObservationPacket,
		frame:     frameID,
		before:    before,
		after:     s.session.Snapshot(),
		packet:    packetMetadata(packet),
		payload:   packet.Payload,
		redacted:  s.sensitive(packet),
	}); err != nil {
		return nil, err
	}
```

Convert the two calls in `observeOutbound` the same way, using `s.session.Outbound()` and `request.packet`.

- [ ] **Step 7: Record the switch point**

In `stream_runtime.go`, extend the transport branch of `processControl`:

```go
	if transport, ok := request.control.(TransportControl); ok {
		if err := transport.ApplyTransport(s.conduit); err != nil {
			request.result <- err

			return
		}
		if err := s.observeSecret(request.control); err != nil {
			request.result <- err
			s.fail(err)
			s.stop()

			return
		}
		request.result <- nil

		return
	}
```

And add:

```go
// observeSecret records that secret material was installed. The key itself is
// present only under disclosure; otherwise the record marks the switch point
// so a capture is never silently missing it.
func (s *Stream) observeSecret(control Control) error {
	disclosing, ok := control.(SecretDisclosure)
	if !ok {
		return nil
	}

	// The label is always recorded; the material is only read when the
	// developer asked for it, so the default path never materializes a key.
	redacted := s.disclosureReason == ""

	var material []byte
	if !redacted {
		material = disclosing.DisclosedSecret()
	}

	snapshot := s.snapshot()

	return s.observe(observationInput{
		direction: s.session.Outbound(),
		stage:     ObservationSecret,
		frame:     s.frameCounter,
		before:    snapshot,
		after:     snapshot,
		secret:    &SecretMetadata{Label: disclosing.SecretLabel()},
		payload:   material,
		redacted:  redacted,
	})
}
```

- [ ] **Step 8: Mark the two login packets in the generator**

In `internal/codegen/generator/templates/protocol.go.tmpl`, add after `ApplyControl`:

```go
// Sensitive implements protocol.SensitivePackets. The key-exchange packets
// carry material that must not reach a capture by default.
func (session *protocolSession) Sensitive(packet protocol.Packet) bool {
	switch packet.Value.(type) {
	case *LoginClientboundEncryptionBegin, *LoginServerboundEncryptionBegin:
		return true
	default:
		return false
	}
}

var _ protocol.SensitivePackets = (*protocolSession)(nil)
```

- [ ] **Step 9: Regenerate and verify**

Run: `devbox run -- task generate`
Run: `devbox run -- task generate:check`
Expected: `generate:check` passes and `git diff --stat` shows only `generated/java/v1_8/protocol.go`.

- [ ] **Step 10: Run the tests**

Run: `devbox run -- task test`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
devbox run -- task precommit
git add observation.go observation_test.go session.go stream.go stream_runtime.go \
  internal/codegen/generator/templates/protocol.go.tmpl generated/java/v1_8/protocol.go
git commit -m "feat(protocol): redact secret material in observations"
```

---

### Task 7: Login roles on the generated descriptor

**Files:**
- Modify: `internal/codegen/generator/templates/protocol.go.tmpl`
- Modify: `internal/codegen/generator/generator.go`
- Modify: `internal/codegen/generator/generator_test.go`
- Modify: `generated/java/v1_8/protocol.go` (regenerated output)

**Interfaces:**
- Produces: `type LoginRole string` with `RoleEncryptionRequest`, `RoleEncryptionResponse`, `RoleLoginSuccess`, `RoleSetCompression`, `RoleLoginStart`; and the optional session interface `LoginRoles interface{ LoginRole(State, Direction, int32) (LoginRole, bool) }`.

- [ ] **Step 1: Why this exists**

The negotiator in Task 8 must not name a concrete packet type. Protocol 775 has
the same five roles plus two the 47 sequence has no analogue for, and a
negotiator written against `*v1_8.LoginClientboundEncryptionBegin` would have to
be rewritten rather than extended. Tagging costs one template change now and
saves a duplicate negotiator with a duplicate failure-mode suite later.

This follows the precedent already set in this plan: the generated session
learns that two packets are sensitive without learning what encryption is. It
now also learns which packet plays which part in a login, without learning what
a login is.

- [ ] **Step 2: Write the failing test**

Assert that the generated protocol 47 session reports `RoleEncryptionRequest`
for clientbound login `encryption_begin`, `RoleEncryptionResponse` for
serverbound `encryption_begin`, `RoleLoginSuccess` for `success`,
`RoleSetCompression` for `compress`, and `RoleLoginStart` for serverbound
`login_start`. Assert that a play-state packet has no role. Assert that a
protocol declaring no roles satisfies the interface and returns false for
everything, so the mechanism is optional.

- [ ] **Step 3: Run and verify failure**

```bash
devbox run -- task test -- ./internal/codegen/generator
```

- [ ] **Step 4: Implement**

Roles come from the descriptor, keyed by state, direction, and packet name, and
are emitted as a generated lookup with no map allocation per call. A packet with
no role is absent rather than an error.

- [ ] **Step 5: Regenerate and verify**

```bash
devbox run -- task generate
devbox run -- task generate:check
devbox run -- task test
```

Expected: `generated/java/v1_8/protocol.go` gains the lookup and nothing else
changes.

- [ ] **Step 6: Commit**

```bash
devbox run -- task precommit
git add internal/codegen/generator generated/java/v1_8
git commit -m "feat(codegen): tag login packets with their role"
```

---

### Task 8: The login negotiator

**Files:**
- Create: `login/negotiator.go`
- Create: `login/doc.go`
- Create: `login/negotiator_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1 through 7. The negotiator reads packets through the login roles from Task 7 and must not reference a generated packet type by name, so protocol 775 extends it by tagging rather than by rewriting.
- Produces: `type Profile struct{ Name java.Username; UUID java.UUID }`; `type Authenticator interface{...}`; `type Verifier interface{...}`; `func NewOffline(string) (Offline, error)`; `func NewNegotiator(Authenticator) (*Negotiator, error)`; `func (*Negotiator) Negotiate(context.Context, *protocol.Stream) (Profile, error)`; `login.ErrInvalidLoginField`; `login.MaxServerIDBytes`.

- [ ] **Step 1: Write the failing test**

Create `login/negotiator_test.go` with a table of failure modes plus the success path. Drive it against a scripted in-memory server built from a second `protocol.Stream` in `RoleServer` over an `net.Pipe`, so the test exercises real framing and real encryption rather than a mock.

```go
package login_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/login"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// rejectingAuthenticator fails Join, which is what an unowned account or an
// unreachable session server looks like to the negotiator.
type rejectingAuthenticator struct{ err error }

func (rejectingAuthenticator) Profile() login.Profile {
	name, _ := java.ParseUsername("tester")

	return login.Profile{Name: name}
}

func (a rejectingAuthenticator) Join(context.Context, java.ServerHash) error { return a.err }

func TestNegotiateCompletesAnEncryptedLogin(t *testing.T) {
	client, server := loginPair(t)

	go serveLogin(t, server, serverScript{encrypt: true, compress: true})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	profile, err := negotiator.Negotiate(t.Context(), client)
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if profile.Name.String() != "tester" {
		t.Fatalf("profile name = %q, want %q", profile.Name, "tester")
	}
	if profile.UUID.IsZero() {
		t.Fatal("a successful login must carry a UUID")
	}

	snapshot, err := client.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.State != v1_8.StatePlay {
		t.Fatalf("state = %q, want %q", snapshot.State, v1_8.StatePlay)
	}
	if snapshot.Pipeline["encryption.enabled"] != "true" {
		t.Fatal("encryption must be enabled after a successful login")
	}
	if snapshot.Pipeline["compression.enabled"] != "true" {
		t.Fatal("the compression transition must still be applied by the session")
	}
}

func TestNegotiateReportsAuthenticatorRejection(t *testing.T) {
	client, server := loginPair(t)
	go serveLogin(t, server, serverScript{encrypt: true})

	refused := errors.New("account does not own the game")
	negotiator, err := login.NewNegotiator(rejectingAuthenticator{err: refused})
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	_, err = negotiator.Negotiate(t.Context(), client)
	if !errors.Is(err, login.ErrAuthenticationRejected) {
		t.Fatalf("error = %v, want ErrAuthenticationRejected", err)
	}
	if !errors.Is(err, refused) {
		t.Fatalf("error must wrap the authenticator cause, got %v", err)
	}
}

func TestNegotiateReportsALoginDisconnect(t *testing.T) {
	client, server := loginPair(t)
	go serveLogin(t, server, serverScript{disconnect: "banned"})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	_, err = negotiator.Negotiate(t.Context(), client)
	if !errors.Is(err, login.ErrLoginDisconnected) {
		t.Fatalf("error = %v, want ErrLoginDisconnected", err)
	}
}

func TestNegotiateHonoursCancellation(t *testing.T) {
	client, server := loginPair(t)
	go serveLogin(t, server, serverScript{stall: true})

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	if _, err := negotiator.Negotiate(ctx, client); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestNewOfflineRejectsAnInvalidName(t *testing.T) {
	if _, err := login.NewOffline(""); !errors.Is(err, java.ErrInvalidUsername) {
		t.Fatalf("error = %v, want ErrInvalidUsername", err)
	}
	if _, err := login.NewOffline(strings.Repeat("n", 17)); !errors.Is(err, java.ErrInvalidUsername) {
		t.Fatalf("error = %v, want ErrInvalidUsername", err)
	}
}

// offlineTester is the authenticator every healthy case in this file uses.
func offlineTester(t *testing.T) login.Offline {
	t.Helper()

	authenticator, err := login.NewOffline("tester")
	if err != nil {
		t.Fatalf("NewOffline: %v", err)
	}

	return authenticator
}

// A hostile or broken server is the reason every inbound login field is
// validated. Each case must fail before the negotiator acts on the field.
func TestNegotiateRejectsMalformedServerFields(t *testing.T) {
	cases := []struct {
		name   string
		script serverScript
		want   error
	}{
		{
			name:   "oversized server ID",
			script: serverScript{encrypt: true, serverID: strings.Repeat("x", 21)},
			want:   login.ErrInvalidLoginField,
		},
		{
			name:   "empty public key",
			script: serverScript{encrypt: true, emptyPublicKey: true},
			want:   login.ErrInvalidLoginField,
		},
		{
			name:   "empty verify token",
			script: serverScript{encrypt: true, emptyVerifyToken: true},
			want:   login.ErrInvalidLoginField,
		},
		{
			name:   "unparseable public key",
			script: serverScript{encrypt: true, garbagePublicKey: true},
			want:   java.ErrInvalidServerKey,
		},
		{
			name:   "malformed success UUID",
			script: serverScript{successUUID: "not-a-uuid"},
			want:   java.ErrInvalidUUID,
		},
		{
			name:   "oversized success username",
			script: serverScript{successUsername: strings.Repeat("n", 17)},
			want:   java.ErrInvalidUsername,
		},
		{
			name:   "empty success username",
			script: serverScript{emptyUsername: true},
			want:   java.ErrInvalidUsername,
		},
		{
			name:   "success username with a control character",
			script: serverScript{successUsername: "bad\nname"},
			want:   java.ErrInvalidUsername,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, server := loginPair(t)
			go serveLogin(t, server, testCase.script)

			negotiator, err := login.NewNegotiator(offlineTester(t))
			if err != nil {
				t.Fatalf("NewNegotiator: %v", err)
			}

			if _, err := negotiator.Negotiate(t.Context(), client); !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
		})
	}
}
```

Add `"strings"` to the test imports. Extend `serverScript` with the fields this
table uses: `serverID string`, `emptyPublicKey`, `emptyVerifyToken`,
`garbagePublicKey`, `successUUID string`, `successUsername string`, and
`emptyUsername bool`. Where a field is empty, `serveLogin` uses its normal
value, so the success case in the earlier tests keeps working unchanged: a
valid dashed UUID and the username `tester`.

- [ ] **Step 1b: Write the scripted server helper**

This helper is the fixture for Tasks 7, 8, and 9. Write it once, correctly, in `login/negotiator_test.go`:

```go
// serverScript describes what the scripted server does. A zero script writes
// a plain, unencrypted, uncompressed success.
type serverScript struct {
	encrypt    bool
	compress   bool
	disconnect string
	stall      bool

	// Malformed-field overrides. Each is empty or false in a healthy script.
	serverID         string
	emptyPublicKey   bool
	emptyVerifyToken bool
	garbagePublicKey bool
	successUUID      string
	successUsername  string
	emptyUsername    bool
}

// loginPair builds a started client stream and a started server stream over an
// in-memory connection, both already in the login state.
func loginPair(t *testing.T) (*protocol.Stream, *protocol.Stream) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	client := startLoginStream(t, clientConn, protocol.RoleClient)
	server := startLoginStream(t, serverConn, protocol.RoleServer)

	return client, server
}

func startLoginStream(t *testing.T, conn net.Conn, role protocol.Role) *protocol.Stream {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("limits: %v", err)
	}
	session, err := v1_8.Protocol().NewSession(role, limits)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	stream, err := protocol.NewStream(session, protocol.Transport{
		Reader:    conn,
		Writer:    conn,
		Interrupt: conn.Close,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := stream.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The handshake is not part of this test. Put both sides straight into
	// the login state, which is where the negotiator expects to begin.
	if err := stream.SetState(t.Context(), v1_8.StateLogin); err != nil {
		t.Fatalf("set state: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	return stream
}

// serveLogin plays the server half. It reports failures through t rather than
// returning them, because it runs in its own goroutine.
func serveLogin(t *testing.T, stream *protocol.Stream, script serverScript) {
	t.Helper()

	ctx := t.Context()

	if _, err := stream.Read(ctx); err != nil {
		return // The client gave up first, which several cases expect.
	}
	if script.stall {
		<-ctx.Done()

		return
	}
	if script.disconnect != "" {
		writeLoginPacket(t, stream, &v1_8.LoginClientboundDisconnect{Reason: script.disconnect})

		return
	}

	if script.encrypt {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Errorf("generate key: %v", err)

			return
		}
		encoded, err := java.EncodeServerPublicKey(&key.PublicKey)
		if err != nil {
			t.Errorf("encode key: %v", err)

			return
		}

		switch {
		case script.emptyPublicKey:
			encoded = nil
		case script.garbagePublicKey:
			encoded = []byte("not a key")
		}

		token := []byte{0x01, 0x02, 0x03, 0x04}
		if script.emptyVerifyToken {
			token = nil
		}

		writeLoginPacket(t, stream, &v1_8.LoginClientboundEncryptionBegin{
			ServerID:    script.serverID,
			PublicKey:   encoded,
			VerifyToken: token,
		})

		packet, err := stream.Read(ctx)
		if err != nil {
			return // The client rejected the request, which is the point.
		}
		response, ok := packet.Value.(*v1_8.LoginServerboundEncryptionBegin)
		if !ok {
			t.Errorf("received %T, want *v1_8.LoginServerboundEncryptionBegin", packet.Value)

			return
		}

		returnedToken, err := java.DecryptFromServerKey(key, response.VerifyToken)
		if err != nil {
			t.Errorf("decrypt verify token: %v", err)

			return
		}
		if err := java.VerifyToken(token, returnedToken); err != nil {
			t.Errorf("verify token: %v", err)

			return
		}

		secretBytes, err := java.DecryptFromServerKey(key, response.SharedSecret)
		if err != nil {
			t.Errorf("decrypt session key: %v", err)

			return
		}
		secret, err := java.SharedSecretFrom(secretBytes)
		if err != nil {
			t.Errorf("adopt session key: %v", err)

			return
		}
		if err := stream.Control(ctx, java.EncryptionControl{Secret: secret}); err != nil {
			t.Errorf("enable server encryption: %v", err)

			return
		}
	}

	if script.compress {
		writeLoginPacket(t, stream, &v1_8.LoginClientboundCompress{Threshold: 256})
	}

	identity := script.successUUID
	if identity == "" {
		identity = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	}
	username := script.successUsername
	if username == "" && !script.emptyUsername {
		username = "tester"
	}

	writeLoginPacket(t, stream, &v1_8.LoginClientboundSuccess{
		UUID:     identity,
		Username: username,
	})
}

// writeLoginPacket sends one clientbound login packet.
func writeLoginPacket(t *testing.T, stream *protocol.Stream, value any) {
	t.Helper()

	identified, ok := value.(interface{ PacketID() int32 })
	if !ok {
		t.Fatalf("%T has no PacketID", value)
	}

	err := stream.Write(t.Context(), protocol.Packet{
		State:     v1_8.StateLogin,
		Direction: protocol.DirectionClientbound,
		ID:        identified.PacketID(),
		Value:     value,
	})
	if err != nil {
		t.Errorf("write %T: %v", value, err)
	}
}
```

Two things to check against the real API before running: `protocol.NewLimits()` takes options and returns `(Limits, error)`, and `Stream.SetState` may be named differently. Read `limits.go` and `stream_runtime.go:660` and adjust the two calls; everything else is settled.

Do not write an RSA key to `testdata`. `task secrets` runs `gitleaks` over the tree and will fail the commit.

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./login`
Expected: FAIL, no package `login`.

- [ ] **Step 3: Write the package documentation**

Create `login/doc.go`:

```go
// Package login runs the Java Edition login sequence over a managed stream.
//
// The negotiator is a helper, not a stream mode. Nothing in protocol.Stream
// knows it exists: it writes packets, reads packets, and applies one transport
// control, all through the public API. A developer who wants control over any
// step uses the primitives in wire/java and the stream's TransitionPolicy
// instead, and never constructs a negotiator.
//
// This package is protocol 47 only. Protocol 775 changes the login packets
// themselves, so generalizing it belongs with the milestone that generates
// them.
package login
```

- [ ] **Step 4: Write the negotiator**

Create `login/negotiator.go`:

```go
package login

import (
	"context"
	"errors"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

var (
	// ErrInvalidAuthenticator reports an authenticator that cannot be used.
	ErrInvalidAuthenticator = errors.New("invalid authenticator")
	// ErrAuthenticationRejected reports that the authenticator refused. It
	// always wraps the authenticator's own error.
	ErrAuthenticationRejected = errors.New("authentication rejected")
	// ErrLoginDisconnected reports a disconnect packet during login.
	ErrLoginDisconnected = errors.New("server disconnected during login")
	// ErrUnexpectedLoginPacket reports a packet the login state does not
	// allow at that point.
	ErrUnexpectedLoginPacket = errors.New("unexpected packet during login")
	// ErrInvalidLoginField reports a peer-supplied login field that failed
	// validation. Login is the one exchange where the peer is entirely
	// unauthenticated, so every field it sends is checked before use.
	ErrInvalidLoginField = errors.New("invalid login field")
)

// MaxServerIDBytes is the longest server ID an encryption request may carry.
// It is the protocol's own bound, not a guess.
const MaxServerIDBytes = 20

// Profile identifies the account a login presents.
//
// Both fields are types that cannot hold an invalid value, so a Profile is
// itself proof that validation ran. There is no validate method to forget to
// call.
type Profile struct {
	Name java.Username
	UUID java.UUID
}

// Authenticator proves account ownership during login.
//
// Join receives the server hash and must prove to the session server that this
// account is joining. It performs whatever network work that requires; this
// package makes no request of its own. An offline authenticator does nothing.
type Authenticator interface {
	Profile() Profile
	Join(ctx context.Context, hash java.ServerHash) error
}

// Offline is an authenticator for a server that does not verify accounts.
// Build it with NewOffline, which validates the name.
type Offline struct {
	name java.Username
}

// NewOffline validates an account name and returns an offline authenticator.
func NewOffline(name string) (Offline, error) {
	parsed, err := java.ParseUsername(name)
	if err != nil {
		return Offline{}, fmt.Errorf("offline authenticator: %w", err)
	}

	return Offline{name: parsed}, nil
}

// Profile implements Authenticator.
func (o Offline) Profile() Profile { return Profile{Name: o.name} }

// Join implements Authenticator and does nothing, because there is nobody to
// tell.
func (Offline) Join(context.Context, java.ServerHash) error { return nil }

// Verifier is the server half of authentication: it confirms that the account
// claiming this username really joined with this server hash.
//
// This package defines the interface and never implements it, because
// implementing it means calling a session server, which is a consumer's job.
type Verifier interface {
	Verify(ctx context.Context, username java.Username, hash java.ServerHash) (Profile, error)
}

// Negotiator runs the client half of the login sequence.
type Negotiator struct {
	authenticator Authenticator
}

// NewNegotiator validates the authenticator and returns a negotiator.
func NewNegotiator(authenticator Authenticator) (*Negotiator, error) {
	if authenticator == nil {
		return nil, fmt.Errorf("%w: nil authenticator", ErrInvalidAuthenticator)
	}
	if authenticator.Profile().Name.IsZero() {
		return nil, fmt.Errorf("%w: profile has no name", ErrInvalidAuthenticator)
	}

	return &Negotiator{authenticator: authenticator}, nil
}

// Negotiate runs the login sequence and returns the profile the server
// confirmed.
//
// It calls stream.Read, so it owns inbound delivery until it returns. A caller
// that reads concurrently would steal packets the sequence needs. The stream
// must already be started and already be in the login state, which is what the
// handshake packet puts it in.
//
// On return the stream is in the play state, encryption is active if the
// server asked for it, and the caller resumes reading.
func (n *Negotiator) Negotiate(ctx context.Context, stream *protocol.Stream) (Profile, error) {
	if stream == nil {
		return Profile{}, fmt.Errorf("%w: nil stream", protocol.ErrInvalidStream)
	}

	profile := n.authenticator.Profile()
	start := protocol.Packet{
		State:     v1_8.StateLogin,
		Direction: protocol.DirectionServerbound,
		ID:        v1_8.LoginServerboundLoginStart{}.PacketID(),
		Value:     &v1_8.LoginServerboundLoginStart{Username: profile.Name.String()},
	}
	if err := stream.Write(ctx, start); err != nil {
		return Profile{}, fmt.Errorf("write login start: %w", err)
	}

	for {
		packet, err := stream.Read(ctx)
		if err != nil {
			return Profile{}, fmt.Errorf("read login packet: %w", err)
		}

		switch value := packet.Value.(type) {
		case *v1_8.LoginClientboundEncryptionBegin:
			if err := n.exchangeKeys(ctx, stream, value); err != nil {
				return Profile{}, err
			}

		case *v1_8.LoginClientboundCompress:
			// The session proposes this transition and the stream commits it
			// before the packet is delivered here. Nothing to do.

		case *v1_8.LoginClientboundSuccess:
			// Both fields come from an unauthenticated peer, so neither is
			// trusted into a Profile without checking.
			identity, err := java.ParseUUID(value.UUID)
			if err != nil {
				return Profile{}, fmt.Errorf("login success UUID: %w", err)
			}
			name, err := java.ParseUsername(value.Username)
			if err != nil {
				return Profile{}, fmt.Errorf("login success username: %w", err)
			}

			return Profile{Name: name, UUID: identity}, nil

		case *v1_8.LoginClientboundDisconnect:
			return Profile{}, fmt.Errorf("%w: %s", ErrLoginDisconnected, value.Reason)

		default:
			return Profile{}, fmt.Errorf(
				"%w: ID %#x in state %q",
				ErrUnexpectedLoginPacket,
				packet.ID,
				packet.State,
			)
		}
	}
}

// exchangeKeys answers one encryption request and enables the cipher.
//
// The ordering is the whole point. Stream.Write returns only after the frame
// has reached the transport, and Stream.Control queues behind it on the same
// coordinator, so the response itself is never encrypted and every later byte
// is.
func (n *Negotiator) exchangeKeys(
	ctx context.Context,
	stream *protocol.Stream,
	request *v1_8.LoginClientboundEncryptionBegin,
) error {
	if err := validateEncryptionRequest(request); err != nil {
		return err
	}

	key, err := java.ParseServerPublicKey(request.PublicKey)
	if err != nil {
		return fmt.Errorf("parse server key: %w", err)
	}

	secret, err := java.NewSharedSecret()
	if err != nil {
		return fmt.Errorf("generate session key: %w", err)
	}

	hash, err := java.ComputeServerHash(request.ServerID, secret, key)
	if err != nil {
		return fmt.Errorf("compute server hash: %w", err)
	}
	if err := n.authenticator.Join(ctx, hash); err != nil {
		return fmt.Errorf("%w: %w", ErrAuthenticationRejected, err)
	}

	encryptedSecret, err := java.EncryptToServerKey(key, secret.Reveal())
	if err != nil {
		return fmt.Errorf("encrypt session key: %w", err)
	}
	encryptedToken, err := java.EncryptToServerKey(key, request.VerifyToken)
	if err != nil {
		return fmt.Errorf("encrypt verify token: %w", err)
	}

	response := protocol.Packet{
		State:     v1_8.StateLogin,
		Direction: protocol.DirectionServerbound,
		ID:        v1_8.LoginServerboundEncryptionBegin{}.PacketID(),
		Value: &v1_8.LoginServerboundEncryptionBegin{
			SharedSecret: encryptedSecret,
			VerifyToken:  encryptedToken,
		},
	}
	if err := stream.Write(ctx, response); err != nil {
		return fmt.Errorf("write encryption response: %w", err)
	}

	if err := stream.Control(ctx, java.EncryptionControl{Secret: secret}); err != nil {
		return fmt.Errorf("enable encryption: %w", err)
	}

	return nil
}

// validateEncryptionRequest checks every field of an encryption request before
// any of it is used. The public key is checked by ParseServerPublicKey; these
// are the bounds that parser has no opinion about.
func validateEncryptionRequest(request *v1_8.LoginClientboundEncryptionBegin) error {
	if len(request.ServerID) > MaxServerIDBytes {
		return fmt.Errorf(
			"%w: server ID is %d bytes, limit %d",
			ErrInvalidLoginField,
			len(request.ServerID),
			MaxServerIDBytes,
		)
	}
	if len(request.PublicKey) == 0 {
		return fmt.Errorf("%w: empty public key", ErrInvalidLoginField)
	}
	if len(request.VerifyToken) == 0 {
		return fmt.Errorf("%w: empty verify token", ErrInvalidLoginField)
	}

	return nil
}
```

The frame limit already bounds the length of every field above, because the
session refused the frame otherwise. These checks add the protocol's own
bounds, which are much tighter, and they run before the RSA work so a hostile
server cannot make the client do public-key operations over a field it was
going to reject anyway.

- [ ] **Step 5: Run the tests**

Run: `devbox run -- task test -- ./login`
Expected: PASS.

- [ ] **Step 6: Run the whole suite with the race detector**

Run: `devbox run -- task test`
Expected: PASS with no race reports. The switch point is where a race would appear.

- [ ] **Step 7: Commit**

```bash
devbox run -- task precommit
git add login
git commit -m "feat(login): add an opt-in Java login negotiator"
```

---

### Task 9: Encrypted loopback over TCP

**Files:**
- Create: `stream_encryption_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1 through 7.
- Produces: no new API. This task proves the composition that the unit tests cannot: compression inside encryption, over a real socket, with the race detector on.

- [ ] **Step 1: Write the failing test**

Create `stream_encryption_test.go` in package `protocol_test`, following the pattern in `stream_tcp_test.go`:

```go
func TestEncryptedLoginOverLoopbackTCP(t *testing.T) {
	client, server := tcpLoginPair(t)

	go serveLogin(t, server, serverScript{encrypt: true, compress: true})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	if _, err := negotiator.Negotiate(t.Context(), client); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	// After login, exchange a play packet and assert the body survives. This
	// is the assertion that fails if the compression envelope and the cipher
	// are composed in the wrong order.
	keepAlive := protocol.Packet{
		State:     v1_8.StatePlay,
		Direction: protocol.DirectionServerbound,
		ID:        v1_8.PlayServerboundKeepAlive{}.PacketID(),
		Value:     &v1_8.PlayServerboundKeepAlive{KeepAliveID: 4242},
	}
	if err := client.Write(t.Context(), keepAlive); err != nil {
		t.Fatalf("write keep alive: %v", err)
	}

	received, err := server.Read(t.Context())
	if err != nil {
		t.Fatalf("read keep alive: %v", err)
	}
	value, ok := received.Value.(*v1_8.PlayServerboundKeepAlive)
	if !ok {
		t.Fatalf("received %T, want *v1_8.PlayServerboundKeepAlive", received.Value)
	}
	if value.KeepAliveID != 4242 {
		t.Fatalf("keep alive ID = %d, want 4242", value.KeepAliveID)
	}
}
```

Also add a test that sends a body larger than the 256-byte compression threshold, so the frame is genuinely compressed and encrypted rather than only encrypted. `PlayServerboundChat` with a long message is the simplest such packet; check the generated struct for its field name before writing it.

- [ ] **Step 2: Run the test to verify it fails**

Run: `devbox run -- task test -- ./ -run TestEncryptedLogin`
Expected: FAIL until the test body is complete.

- [ ] **Step 3: Complete the test**

Copy `serverScript`, `serveLogin`, and `writeLoginPacket` from `login/negotiator_test.go` into this file. They are test helpers, not API, so copying is correct; do not export them. Replace `loginPair` with a TCP version:

```go
// tcpLoginPair is loginPair over a real socket. net.Pipe is synchronous and
// unbuffered, so it hides the read-ahead behaviour at the switch point that
// this test exists to check.
func tcpLoginPair(t *testing.T) (*protocol.Stream, *protocol.Stream) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(accepted)

			return
		}
		accepted <- conn
	}()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })

	serverConn, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = serverConn.Close() })

	return startLoginStream(t, clientConn, protocol.RoleClient),
		startLoginStream(t, serverConn, protocol.RoleServer)
}
```

Then the test body is: `client, server := tcpLoginPair(t)`, `go serveLogin(t, server, serverScript{encrypt: true, compress: true})`, run the negotiator, and assert the packet round-trips shown in Step 1.

- [ ] **Step 4: Run the test**

Run: `devbox run -- task test -- ./ -run TestEncryptedLogin`
Expected: PASS.

- [ ] **Step 5: Run the suite repeatedly to catch flakes**

Run: `devbox run -- task test -- ./ -run TestEncryptedLogin -count 20`
Expected: PASS every iteration. A failure here is a real ordering bug at the switch point, not a flaky test; do not add a retry.

- [ ] **Step 6: Commit**

```bash
devbox run -- task precommit
git add stream_encryption_test.go
git commit -m "test(protocol): cover encrypted login over loopback TCP"
```

---

### Task 10: Node interoperability

**Files:**
- Modify: `interop/node/runner.mjs`
- Modify: `interop/node_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: scenarios `encrypted-login` in both `--mode server` and `--mode client`.

- [ ] **Step 1: Add the yggdrasil stub to the runner**

In `interop/node/runner.mjs`, before the server is created, add:

```js
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)

// The pinned minecraft-protocol 1.66.2 gates the encryption request and the
// hasJoined call on the same flag, so there is no configuration that encrypts
// without contacting Mojang. server/login.js resolves yggdrasil.server through
// the module object at connection time, so replacing the export here is
// enough. This is confined to the loopback interoperability runner and never
// ships; no scenario contacts a host outside 127.0.0.1.
function stubSessionServer () {
  const yggdrasil = require('yggdrasil')
  const real = yggdrasil.server
  yggdrasil.server = (options) => ({
    ...real(options),
    hasJoined (username, serverId, sharedSecret, publicKey, callback) {
      callback(null, { id: '00000000000040008000000000000000', name: username })
    }
  })
}
```

- [ ] **Step 2: Add the server scenario**

In the server branch of `runner.mjs`, next to the existing `login` scenario, add:

```js
  if (args.scenario === 'encrypted-login') {
    stubSessionServer()
    const server = mc.createServer({
      'online-mode': true,
      host: args.host,
      port: args.port,
      version: '1.8.9',
      keepAlive: false
    })
    server.on('login', (client) => {
      emit({ event: 'login', username: client.username, uuid: client.uuid })
    })
    server.on('listening', () => {
      emit({ event: 'listening', port: server.socketServer.address().port })
    })
    return
  }
```

Match the surrounding code's emit helpers and error handling rather than inventing new ones; read the existing `login` scenario first and mirror it.

- [ ] **Step 3: Add the client scenario**

In the client branch, add an `encrypted-login` scenario that creates a client with `auth: 'offline'` and no credentials. Version 1.66.2 answers `encryption_begin` regardless of credentials and skips the join call, which is exactly what a Go server without account verification needs.

```js
  if (args.scenario === 'encrypted-login') {
    const client = mc.createClient({
      host: args.host,
      port: args.port,
      username: 'interop',
      version: '1.8.9',
      auth: 'offline',
      keepAlive: false
    })
    client.on('success', () => emit({ event: 'login', username: client.username }))
    client.on('error', (err) => fail(String(err)))
    return
  }
```

- [ ] **Step 4: Write the Go interoperability tests**

Append to `interop/node_test.go`, mirroring `TestGoClientAgainstNodeServerCompressedLogin` and `TestNodeClientAgainstGoServerCompressedLogin`:

```go
func TestGoClientAgainstNodeServerEncryptedLogin(t *testing.T) {
	runner := startRunner(t, "--mode", "server", "--scenario", "encrypted-login", "--port", "0")
	// ... wait for the listening event, dial, handshake into login state,
	// run login.NewNegotiator with an offline "interop" account and Negotiate.

	// The assertions that matter:
	//   1. Negotiate returns without error.
	//   2. The stream snapshot reports encryption.enabled = "true".
	//   3. The Node runner emitted a login event for the username.
	//   4. A play packet round-trips after login.
}

func TestNodeClientAgainstGoServerEncryptedLogin(t *testing.T) {
	// ... listen, start the Node client scenario against the port, and run a
	// Go RoleServer stream that generates an RSA key, sends
	// LoginClientboundEncryptionBegin, verifies the returned token, applies
	// java.EncryptionControl, and writes LoginClientboundSuccess.

	// Assert the Node client reaches the success event and that a play packet
	// round-trips afterwards.
}
```

Fill both bodies completely using the existing helpers in that file: `startRunner`, the event-waiting helper, and the transcript sink. Do not leave the comment placeholders in the committed test.

- [ ] **Step 5: Run the interoperability suite**

Run: `devbox run -- task test:interop`
Expected: PASS.

If the Node server scenario fails with a session-server error, the stub is not taking effect: confirm that `stubSessionServer()` runs before `mc.createServer` and that `require('yggdrasil')` resolves to the same module instance `minecraft-protocol` uses. Do not work around it by disabling encryption; the lane exists to exercise the cipher.

- [ ] **Step 6: Commit**

```bash
devbox run -- task precommit
git add interop/node/runner.mjs interop/node_test.go
git commit -m "test(interop): cover encrypted login against pinned Node"
```

---

### Task 11: Documentation and milestone records

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`
- Modify: `ROADMAP.md`
- Modify: `../headless-minecraft/MASTER_PLAN.md`

**Interfaces:**
- Consumes: the finished implementation.
- Produces: no code.

- [ ] **Step 1: Document encryption in the README**

Add a section after whatever section covers compression, showing the negotiator in ten lines and stating the four rules a developer needs: encryption is applied through `Stream.Control`, not proposed by the session; the negotiator owns `Read` while it runs; captures are redacted unless `WithSecretDisclosure` is passed, and a disclosed capture is a credential; and every peer-supplied identity crosses the boundary as a parsed type, so a `Profile` is proof that validation ran.

- [ ] **Step 2: Add the changelog entry**

Follow the existing format in `CHANGELOG.md`. Cover: the conduit and `TransportControl`; AES-128/CFB8; the key-exchange primitives and `ComputeServerHash`; the `java.UUID`, `java.Username`, and `java.ServerHash` identity types with strict login-field validation; `SharedSecret` redaction; `Observation.Redacted`, `ObservationSecret`, and `WithSecretDisclosure`; the `login` package; and the new sentinel errors. Note that `Stream.Snapshot` now reports `encryption.enabled`.

- [ ] **Step 3: Update the roadmap**

In `ROADMAP.md`, mark the P1 encryption and login items complete. Leave the configuration-state item, which belongs to the current-protocol work, unticked.

- [ ] **Step 4: Update the master plan**

In `../headless-minecraft/MASTER_PLAN.md`:

- Change M2's status from **Next** to **Complete** in the tracker table and the mermaid chart, and point its detailed-documents cell at this spec and this plan.
- Correct the "Current position" section, which still says M1 is uncommitted pending review. It landed as `8625ea7`.
- Move the "Implement configuration and play transitions for modern Java login" bullet from the M2 checklist to the M4 checklist, and add one line stating why: protocol 775 codecs do not exist until M4, and hand-written login packets would be discarded when M4 generates them.
- Add the two decisions this milestone made that affect later work, in the same style as the M1 notes:
  - Encryption is applied, not proposed. No packet carries the plaintext key, so M3 and M6 consumers must call `Stream.Control` after the key exchange rather than expecting a session transition.
  - The `login` package is protocol 47 only. M4 either parameterizes it or adds a second constructor, and M6 depends on whichever it chooses.
- Mark M3 as the next milestone.

- [ ] **Step 5: Run the full gate**

Run: `devbox run -- task verify`
Expected: PASS. This runs `generate:check`, `lint`, `secrets`, `test`, `test:interop`, `vuln`, and `build`.

- [ ] **Step 6: Commit**

```bash
devbox run -- task precommit
git add README.md CHANGELOG.md ROADMAP.md
git commit -m "docs: record encryption and the login lifecycle"
```

Commit the master plan separately, in its own repository:

```bash
cd ../headless-minecraft
git add MASTER_PLAN.md
git commit -m "docs: mark M2 complete and M3 next"
```

---

## Notes for the implementer

**The one subtle thing in this plan.** Everything else is ordinary Go. The subtle part is that `Conduit.Read` transforms bytes when it hands them out, not when it buffers them, and `EnableEncryption` refuses if the buffer is not empty. Those two facts together are what make a mid-stream cipher switch safe. If you find yourself moving the transform into the buffering path, or deleting the buffered-bytes check because it seems paranoid, stop: you have reintroduced the bug the design exists to prevent, and it will show up as a corrupted frame one packet after login on a real server, not in the test suite.

**Why `Stream.Write` then `Stream.Control` is safe.** `Write` returns only after `writePump` reports the frame written, and both `Write` and `Control` queue to the same coordinator goroutine. The response packet is therefore fully on the wire in plaintext before the cipher exists. Do not "optimize" this by applying the control before the write.

**SHA-1 and PKCS#1 v1.5 are required, not chosen.** `golangci-lint` may flag `crypto/sha1` and `rsa.EncryptPKCS1v15` as weak. Both are what the Java Edition protocol specifies, and changing either makes the client unable to log in to any server. If the linter objects, add a narrowly scoped `//nolint` with that reason on the line, not a blanket exclusion in `.golangci.yml`.

**If a task's test helper does not exist.** `stream_test_helpers_test.go` already provides a fake session and an in-memory transport. Read it before writing a new helper, and extend it rather than duplicating it.
