package lamigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// checksumFile reads the file at path and returns its SHA-256 digest.
func checksumFile(path string) ([32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("lamigrate: read for checksum %s: %w", path, err)
	}
	return sha256.Sum256(data), nil
}

// checksumBytes returns the SHA-256 digest of the given byte slice.
func checksumBytes(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// checksumHex returns the lowercase hex encoding of a SHA-256 digest.
func checksumHex(sum [32]byte) string {
	return hex.EncodeToString(sum[:])
}
