package java

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/subtle"
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
