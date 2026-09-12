package account

import "testing"

func TestNormalizeCRPRegion(t *testing.T) {
	for input, expected := range map[string]string{"6": "06", " CRP-06 ": "06", "24": "24"} {
		if got := normalizeCRPRegion(input); got != expected {
			t.Fatalf("normalizeCRPRegion(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestValidCRPRegion(t *testing.T) {
	for _, region := range []string{"01", "06", "24"} {
		if !isValidCRPRegion(region) {
			t.Errorf("expected CRP region %q to be valid", region)
		}
	}
	for _, region := range []string{"00", "25", "SP"} {
		if isValidCRPRegion(region) {
			t.Errorf("expected CRP region %q to be invalid", region)
		}
	}
}

func TestSignupEmailPattern(t *testing.T) {
	for _, email := range []string{"mariana@example.com", "name+tag@sub.example.com"} {
		if !isValidSignupEmail(email) {
			t.Errorf("expected email %q to be valid", email)
		}
	}
	for _, email := range []string{"missing-at.example.com", "name@", "name @example.com", "name@example", ".name@example.com", "na..me@example.com"} {
		if isValidSignupEmail(email) {
			t.Errorf("expected email %q to be invalid", email)
		}
	}
}
