package patient_test

import (
	"testing"

	"github.com/joycesilva/acolhe-api/internal/patient"
	"github.com/stretchr/testify/require"
)

func TestSMTPMailerRequiresProviderConfiguration(t *testing.T) {
	err := (patient.SMTPMailer{}).Send("patient@example.com", "invite", "opaque link")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "opaque link")
}
