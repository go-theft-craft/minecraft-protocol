package java

import (
	"crypto/rsa"
	"crypto/subtle"
	"fmt"
)

// DecryptSharedSecret recovers the session key a client encrypted to the
// server's public key in its encryption response.
//
// It is the server half of the exchange, and it returns a SharedSecret rather
// than a []byte so the recovered key redacts itself exactly as the client's
// own key does. A key of the wrong length never becomes a SharedSecret: it is
// refused here, before any caller can install a cipher with it.
func DecryptSharedSecret(key *rsa.PrivateKey, ciphertext []byte) (SharedSecret, error) {
	plaintext, err := DecryptFromServerKey(key, ciphertext)
	if err != nil {
		return SharedSecret{}, err
	}

	secret, err := SharedSecretFrom(plaintext)
	if err != nil {
		return SharedSecret{}, err
	}

	return secret, nil
}

// VerifyToken decrypts the verify token a client returned and compares it with
// the one the server sent.
//
// It compares in constant time, because a server that leaks the comparison
// position leaks the token. A token of the wrong length fails with the same
// error as a differing one, and fails before any content is compared, so the
// length is not an oracle that a mismatch is not.
//
// This package supplies the comparison so a server implementation does not
// reimplement it and get it wrong.
func VerifyToken(key *rsa.PrivateKey, expected, encrypted []byte) error {
	returned, err := DecryptFromServerKey(key, encrypted)
	if err != nil {
		return err
	}

	if len(expected) == 0 || len(expected) != len(returned) {
		return fmt.Errorf("%w: length %d, want %d", ErrVerifyTokenMismatch, len(returned), len(expected))
	}
	if subtle.ConstantTimeCompare(expected, returned) != 1 {
		return ErrVerifyTokenMismatch
	}

	return nil
}
