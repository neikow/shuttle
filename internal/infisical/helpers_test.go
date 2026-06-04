package infisical

import (
	"encoding/hex"
	"fmt"
)

// ComputeHeader builds the signature header value for a timestamp + body.
// Test-only: the production path only verifies inbound signatures, so the
// minting helper lives beside the tests that use it.
func ComputeHeader(ts string, body []byte, secret string) string {
	return fmt.Sprintf("t=%s;%s", ts, hex.EncodeToString(computeMAC(body, secret)))
}
