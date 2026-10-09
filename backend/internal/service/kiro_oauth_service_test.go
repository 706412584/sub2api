package service

import "testing"

func TestValidateKiroExternalIDPTokenEndpointRejectsPrivateTargets(t *testing.T) {
	cases := []string{
		"http://example.com/token",
		"https://127.0.0.1/token",
		"https://localhost/token",
		"https://169.254.169.254/latest/meta-data",
		"https://10.0.0.1/token",
		"https://example.com:8443/token",
	}

	for _, endpoint := range cases {
		if _, err := newKiroExternalIDPHTTPClient(endpoint); err == nil {
			t.Fatalf("expected %q to be rejected", endpoint)
		}
	}
}
