package protocol

import (
	"bufio"
	"crypto/cipher"
	"fmt"
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
	// pending is how many bytes the read buffer still holds, recorded by the
	// reader under the mutex.
	//
	// EnableEncryption cannot ask the bufio.Reader directly: the read pump is
	// normally parked inside a transport read at switch time, and querying
	// bufio state concurrently with that read is a data race. The reader
	// publishes the count instead, which is exact for the same reason the
	// switch is safe at all -- a parked read has buffered nothing yet, so the
	// last recorded count is still current.
	pending int
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

	// The lock is taken after the read, never around it, so a transport read
	// that blocks forever cannot stop the coordinator from switching ciphers.
	c.mu.Lock()
	if read > 0 && c.decrypt != nil {
		c.decrypt.XORKeyStream(p[:read], p[:read])
	}
	c.pending = c.buffered.Buffered()
	c.mu.Unlock()

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
	if c.pending > 0 {
		return fmt.Errorf("%w: %d unread bytes", ErrEncryptionOverrun, c.pending)
	}

	c.decrypt = decrypt
	c.encrypt = encrypt

	return nil
}

// pipeline reports the conduit's contribution to a stream snapshot.
func (c *Conduit) pipeline() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return map[string]string{
		"encryption.enabled": strconv.FormatBool(c.decrypt != nil),
	}
}
