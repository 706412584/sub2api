package kiro

import (
	"errors"
	"strings"
)

const APIKeyPrefix = "ksk_"

type Endpoint string

const (
	EndpointCLI Endpoint = "cli"
	EndpointIDE Endpoint = "ide"
)

// Credentials contains only data-plane credential fields. API key credentials
// deliberately never refresh and never expose a profile ARN to requests.
type Credentials struct {
	AccessToken  string   `json:"accessToken,omitempty"`
	RefreshToken string   `json:"refreshToken,omitempty"`
	ProfileARN   string   `json:"profileArn,omitempty"`
	APIKey       string   `json:"kiroApiKey,omitempty"`
	AuthMethod   string   `json:"authMethod,omitempty"`
	APIRegion    string   `json:"apiRegion,omitempty"`
	Endpoint     Endpoint `json:"endpoint,omitempty"`
}

func (c Credentials) IsAPIKey() bool {
	return strings.HasPrefix(c.APIKey, APIKeyPrefix) || strings.HasPrefix(c.AccessToken, APIKeyPrefix) ||
		strings.EqualFold(c.AuthMethod, "api_key")
}

func (c Credentials) Validate() error {
	if c.IsAPIKey() {
		if _, err := c.BearerToken(); err != nil {
			return err
		}
		return nil
	}
	if c.AccessToken == "" {
		return errors.New("kiro: OAuth access token is required")
	}
	return nil
}

func (c Credentials) BearerToken() (string, error) {
	if c.APIKey != "" {
		if !strings.HasPrefix(c.APIKey, APIKeyPrefix) {
			return "", errors.New("kiro: API key must start with ksk_")
		}
		return c.APIKey, nil
	}
	if strings.HasPrefix(c.AccessToken, APIKeyPrefix) || strings.EqualFold(c.AuthMethod, "api_key") {
		if !strings.HasPrefix(c.AccessToken, APIKeyPrefix) {
			return "", errors.New("kiro: API key must start with ksk_")
		}
		return c.AccessToken, nil
	}
	if c.AccessToken == "" {
		return "", errors.New("kiro: OAuth access token is required")
	}
	return c.AccessToken, nil
}

func (c Credentials) TokenType() string {
	if c.IsAPIKey() {
		return "API_KEY"
	}
	return ""
}

func (c Credentials) ShouldRefresh() bool { return !c.IsAPIKey() && c.RefreshToken != "" }

// EffectiveEndpoint defaults to the Kiro CLI data plane for both credential
// kinds; OAuth credentials can opt into the IDE endpoint explicitly.
func (c Credentials) EffectiveEndpoint() Endpoint {
	if c.Endpoint == EndpointIDE {
		return EndpointIDE
	}
	return EndpointCLI
}

func (c Credentials) EffectiveRegion(fallback string) string {
	if c.APIRegion != "" {
		return c.APIRegion
	}
	if fallback != "" {
		return fallback
	}
	return "us-east-1"
}

func (c Credentials) StreamingProfileARN() string {
	if c.IsAPIKey() {
		return ""
	}
	return c.ProfileARN
}
