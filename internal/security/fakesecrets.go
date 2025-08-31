// Package security provides secret detection and security scanning.
package security

import "strings"

// rot13 decodes a ROT13 encoded string for test secrets.
// This prevents test secrets from triggering security scans while
// still allowing us to test secret detection functionality.
func rot13(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return 'A' + (r-'A'+13)%26
		case r >= 'a' && r <= 'z':
			return 'a' + (r-'a'+13)%26
		default:
			return r
		}
	}, s)
}

// FakeSecrets provides obfuscated test secrets that won't trigger scanners.
type FakeSecrets struct{}

// GitHubPAT returns a test GitHub Personal Access Token.
func (FakeSecrets) GitHubPAT() string {
	// ROT13 encoded test token.
	return rot13("tuc_1234567890nopqrs1234567890nopqrs123456")
}

// AWSAccessKey returns a test AWS Access Key ID.
func (FakeSecrets) AWSAccessKey() string {
	// ROT13 encoded test AWS key.
	return rot13("NXVNVBFSBQAA7RKNZCYR")
}

// PrivateKeyHeader returns a test private key header.
func (FakeSecrets) PrivateKeyHeader() string {
	// ROT13 encoded PEM header.
	return rot13("-----ORTVA CEVINGR XRL-----")
}

// PrivateKeyFooter returns a test private key footer.
func (FakeSecrets) PrivateKeyFooter() string {
	// ROT13 encoded PEM footer.
	return rot13("-----RAQ CEVINGR XRL-----")
}

// PrivateKeyContent returns test private key content (fake base64).
func (FakeSecrets) PrivateKeyContent() string {
	// Realistic-looking fake base64 content that matches gitleaks patterns.
	return "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC9W8bA7TrCkwAZ"
}

// FullPrivateKey returns a complete test private key.
func (fs FakeSecrets) FullPrivateKey() string {
	return fs.PrivateKeyHeader() + "\n" +
		fs.PrivateKeyContent() + "\n" +
		fs.PrivateKeyFooter()
}
