// Package documentary implements private, encrypted clinical notebooks.
package documentary

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

var ErrCrypto = errors.New("registro documental indisponível: não foi possível validar a criptografia")

type Keyring struct {
	active string
	keys   map[string]cipher.AEAD
}
type envelope struct {
	Format     int    `json:"format"`
	KeyID      string `json:"keyId"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ParseKeyring rejects duplicate keys rather than silently accepting the last value.
func ParseKeyring(active, raw string) (*Keyring, error) {
	d := json.NewDecoder(bytes.NewBufferString(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, ErrCrypto
	}
	k := &Keyring{active: active, keys: map[string]cipher.AEAD{}}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return nil, ErrCrypto
		}
		id, ok := t.(string)
		if !ok || !keyIDPattern.MatchString(id) || k.keys[id] != nil {
			return nil, ErrCrypto
		}
		var value string
		if d.Decode(&value) != nil {
			return nil, ErrCrypto
		}
		key, err := hex.DecodeString(value)
		if err != nil || len(key) != 32 {
			return nil, ErrCrypto
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, ErrCrypto
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, ErrCrypto
		}
		k.keys[id] = aead
	}
	if _, err := d.Token(); err != nil {
		return nil, ErrCrypto
	}
	if d.Decode(new(any)) != io.EOF || k.keys[active] == nil {
		return nil, ErrCrypto
	}
	return k, nil
}
func (k *Keyring) Encrypt(text string, aad []byte) ([]byte, error) {
	if k == nil {
		return nil, ErrCrypto
	}
	a := k.keys[k.active]
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrCrypto
	}
	return json.Marshal(envelope{1, k.active, nonce, a.Seal(nil, nonce, []byte(text), aad)})
}
func (k *Keyring) Decrypt(raw, aad []byte) (string, error) {
	var e envelope
	if k == nil || json.Unmarshal(raw, &e) != nil || e.Format != 1 {
		return "", ErrCrypto
	}
	a := k.keys[e.KeyID]
	if a == nil || len(e.Nonce) != a.NonceSize() {
		return "", ErrCrypto
	}
	plain, err := a.Open(nil, e.Nonce, e.Ciphertext, aad)
	if err != nil {
		return "", ErrCrypto
	}
	return string(plain), nil
}
