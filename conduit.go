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

// EnableEncryption installs both ciphers at once.
//
// A peer that can switch both halves together should: it is one call and one
// refusal. A login cannot, because the two halves become due at different
// moments, which is what EnableReadEncryption and EnableWriteEncryption are for.
func (c *Conduit) EnableEncryption(decrypt, encrypt cipher.Stream) error {
	if decrypt == nil || encrypt == nil {
		return fmt.Errorf("%w: nil cipher", ErrEncryptionUnavailable)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.decrypt != nil || c.encrypt != nil {
		return ErrEncryptionEnabled
	}
	if err := c.readSwitchable(); err != nil {
		return err
	}

	c.decrypt = decrypt
	c.encrypt = encrypt

	return nil
}

// EnableReadEncryption installs the inbound cipher on its own.
//
// The inbound half is due before the outbound one. A peer starts encrypting the
// moment it has what it needs to -- for Java, the moment it reads the encryption
// response -- and it owes this side no pause while this side catches up. Between
// writing that response and installing a cipher, every byte the read pump takes
// is handed out as plaintext, so a peer that replies immediately, as one
// refusing a login does, is decoded as garbage: a nonsense frame length, and a
// stream that fails without ever saying what it was told.
//
// Installing this half first closes that window. It is safe to install early
// because the protocol says what is inbound: after the encryption request there
// is nothing left for the peer to send in the clear, so there is no plaintext
// for an early cipher to corrupt.
func (c *Conduit) EnableReadEncryption(decrypt cipher.Stream) error {
	if decrypt == nil {
		return fmt.Errorf("%w: nil cipher", ErrEncryptionUnavailable)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.decrypt != nil {
		return ErrEncryptionEnabled
	}
	if err := c.readSwitchable(); err != nil {
		return err
	}

	c.decrypt = decrypt

	return nil
}

// EnableWriteEncryption installs the outbound cipher on its own.
//
// It has no read buffer to check. The outbound half is due only once the last
// plaintext packet is on the wire, and what this side has already written is not
// something a cipher installed now can reach.
func (c *Conduit) EnableWriteEncryption(encrypt cipher.Stream) error {
	if encrypt == nil {
		return fmt.Errorf("%w: nil cipher", ErrEncryptionUnavailable)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.encrypt != nil {
		return ErrEncryptionEnabled
	}

	c.encrypt = encrypt

	return nil
}

// readSwitchable reports whether the inbound cipher can be installed now. The
// caller holds the mutex.
//
// Unread bytes mean the read pump is part-way through a frame it began in the
// clear. Installing a cipher now would decrypt the rest of that frame and hand
// back a packet that is half one thing and half another, so the switch is
// refused and says why. It is not a guess about the peer: read-ahead is exactly
// how a pump ends up holding the tail of a frame.
func (c *Conduit) readSwitchable() error {
	if c.pending > 0 {
		return fmt.Errorf("%w: %d unread bytes", ErrEncryptionOverrun, c.pending)
	}

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
