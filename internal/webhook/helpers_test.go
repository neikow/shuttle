package webhook

import "encoding/hex"

// ComputeHeader returns the X-Hub-Signature-256 header value for body+secret.
// Test-only: the production path only ever verifies signatures, never mints
// them, so this lives beside the tests that exercise VerifySignature.
func ComputeHeader(body []byte, secret string) string {
	return signaturePrefix + hex.EncodeToString(computeMAC(body, []byte(secret)))
}
