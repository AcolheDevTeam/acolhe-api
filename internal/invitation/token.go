package invitation

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

func NewToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func Encrypt(token string, key string) ([]byte, error) {
	k := sha256.Sum256([]byte(key))
	b, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, []byte(token), nil), nil
}

func Decrypt(ciphertext []byte, key string) (string, error) {
	k := sha256.Sum256([]byte(key))
	b, err := aes.NewCipher(k[:])
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < g.NonceSize() {
		return "", errors.New("ciphertext inválido")
	}
	plain, err := g.Open(nil, ciphertext[:g.NonceSize()], ciphertext[g.NonceSize():], nil)
	return string(plain), err
}
