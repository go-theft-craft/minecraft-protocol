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
