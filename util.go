package spoon

import (
	"crypto/sha256"
	"fmt"
)

// GenerateChecksum generate a checksum based of of the provided bytes
func GenerateChecksum(b []byte) string {

	hash := sha256.New()
	hash.Write(b)

	return fmt.Sprintf("%x", string(hash.Sum(nil)))
}
