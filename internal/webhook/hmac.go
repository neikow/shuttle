package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

const signaturePrefix = "sha256="

// VerifySignature verifies an X-Hub-Signature-256 header value against the body and secret.
func VerifySignature(body []byte, secret, headerValue string) error {
	if len(headerValue) < len(signaturePrefix) {
		return fmt.Errorf("missing signature prefix")
	}
	got, err := hex.DecodeString(headerValue[len(signaturePrefix):])
	if err != nil {
		return fmt.Errorf("invalid signature hex: %w", err)
	}
	want := computeMAC(body, []byte(secret))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// ComputeHeader returns the X-Hub-Signature-256 header value for body+secret.
func ComputeHeader(body []byte, secret string) string {
	return signaturePrefix + hex.EncodeToString(computeMAC(body, []byte(secret)))
}

func computeMAC(body, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return mac.Sum(nil)
}
