package java

import "crypto/cipher"

// newCFB8Encrypter and newCFB8Decrypter build the stream cipher Java Edition
// uses: AES in cipher feedback mode with an eight-bit segment size.
//
// The standard library's cipher.NewCFBEncrypter is cipher feedback with a
// segment size equal to the block size, which is a different cipher producing
// different bytes. Two peers that both use it interoperate with each other and
// with nothing else, so a loopback test cannot tell the two modes apart. The
// pinned Node interoperability lane is what catches the difference, and this
// type is what it caught.
//
// One AES block encryption per plaintext byte is inherent to CFB8 and is what
// vanilla Java does.
type cfb8 struct {
	block cipher.Block
	// register is the shifting feedback register, one block wide.
	register []byte
	// keyStream holds one encrypted register so the loop allocates nothing.
	keyStream []byte
	// decrypting selects which byte is fed back. The register always takes the
	// ciphertext byte, which is the input when decrypting and the output when
	// encrypting.
	decrypting bool
}

func newCFB8(block cipher.Block, iv []byte, decrypting bool) cipher.Stream {
	size := block.BlockSize()
	stream := &cfb8{
		block:      block,
		register:   make([]byte, size),
		keyStream:  make([]byte, size),
		decrypting: decrypting,
	}
	copy(stream.register, iv)

	return stream
}

// newCFB8Encrypter returns a CFB8 encrypting stream. iv must be one block long.
func newCFB8Encrypter(block cipher.Block, iv []byte) cipher.Stream {
	return newCFB8(block, iv, false)
}

// newCFB8Decrypter returns a CFB8 decrypting stream. iv must be one block long.
func newCFB8Decrypter(block cipher.Block, iv []byte) cipher.Stream {
	return newCFB8(block, iv, true)
}

// XORKeyStream implements cipher.Stream. dst and src may be the same slice.
func (c *cfb8) XORKeyStream(dst, src []byte) {
	last := len(c.register) - 1

	for index := range src {
		input := src[index]
		c.block.Encrypt(c.keyStream, c.register)
		output := input ^ c.keyStream[0]

		copy(c.register, c.register[1:])
		if c.decrypting {
			c.register[last] = input
		} else {
			c.register[last] = output
		}

		dst[index] = output
	}
}
