package invitation_test

import (
	"testing"

	"github.com/joycesilva/acolhe-api/internal/invitation"
	"github.com/stretchr/testify/require"
)

func TestTokenIsOpaqueAndCiphertextDoesNotContainPlaintext(t *testing.T) {
	token, hash, err := invitation.NewToken()
	require.NoError(t, err)
	require.Len(t, hash, 32)
	ciphertext, err := invitation.Encrypt(token, "test-key")
	require.NoError(t, err)
	require.NotContains(t, string(ciphertext), token)
	decoded, err := invitation.Decrypt(ciphertext, "test-key")
	require.NoError(t, err)
	require.Equal(t, token, decoded)
}

func TestTokensRotate(t *testing.T) {
	first, firstHash, err := invitation.NewToken()
	require.NoError(t, err)
	second, secondHash, err := invitation.NewToken()
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NotEqual(t, firstHash, secondHash)
}
