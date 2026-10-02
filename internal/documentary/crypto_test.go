package documentary

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testKeys(t *testing.T) *Keyring {
	t.Helper()
	k, err := ParseKeyring("v1", `{"v1":"`+strings.Repeat("ab", 32)+`","v2":"`+strings.Repeat("cd", 32)+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
func TestKeyringValidation(t *testing.T) {
	for _, raw := range []string{"", `null`, `[]`, `{}`, `{"v1":null}`, `{"v1":"abc"}`, `{"v1":"` + strings.Repeat("ab", 32) + `","v1":"` + strings.Repeat("cd", 32) + `"}`, `{"bad.id":"` + strings.Repeat("ab", 32) + `"}`, `{"v1":"` + strings.Repeat("ab", 32) + `"} {}`} {
		if _, err := ParseKeyring("v1", raw); err == nil {
			t.Fatalf("accepted invalid keyring")
		}
	}
}
func TestEncryptionAuthenticatesContentAndAssociation(t *testing.T) {
	k := testKeys(t)
	for _, plain := range []string{"", "  Olá 📝\n\tconteúdo  "} {
		encrypted, err := k.Encrypt(plain, []byte("association"))
		if err != nil {
			t.Fatal(err)
		}
		value, err := k.Decrypt(encrypted, []byte("association"))
		if err != nil || value != plain {
			t.Fatal("round trip failed")
		}
		again, _ := k.Encrypt(plain, []byte("association"))
		if bytes.Equal(encrypted, again) {
			t.Fatal("nonce reused")
		}
		if _, err = k.Decrypt(encrypted, []byte("other")); err == nil {
			t.Fatal("transplant accepted")
		}
		var e envelope
		_ = json.Unmarshal(encrypted, &e)
		e.Ciphertext[0] ^= 1
		tampered, _ := json.Marshal(e)
		if _, err = k.Decrypt(tampered, []byte("association")); err == nil {
			t.Fatal("tamper accepted")
		}
		delete(k.keys, "v1")
		if _, err = k.Decrypt(encrypted, []byte("association")); err == nil {
			t.Fatal("missing key accepted")
		}
		k = testKeys(t)
	}
}
