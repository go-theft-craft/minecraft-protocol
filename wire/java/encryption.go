package java

import (
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"fmt"
	"math/big"

	"github.com/go-theft-craft/minecraft-protocol"
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
//
// The scheme is not a choice. Java Edition's encryption request and response
// are defined in terms of PKCS#1 v1.5, so OAEP would produce a ciphertext no
// vanilla server can decrypt. The deprecation is suppressed here rather than
// repository-wide so any other use of it still fails the linter.
func EncryptToServerKey(key *rsa.PublicKey, plaintext []byte) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrInvalidServerKey)
	}

	//nolint:staticcheck // SA1019: the wire format mandates PKCS#1 v1.5.
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

	//nolint:staticcheck // SA1019: the wire format mandates PKCS#1 v1.5.
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

	// Java uses the key as its own initialization vector, and the mode is
	// CFB8 rather than the standard library's block-wide CFB.
	return conduit.EnableEncryption(
		newCFB8Decrypter(block, key),
		newCFB8Encrypter(block, key),
	)
}

var (
	_ protocol.TransportControl = EncryptionControl{}
	_ protocol.SecretDisclosure = EncryptionControl{}
)
