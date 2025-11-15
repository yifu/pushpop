package blake

import (
	"fmt"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// CalcBlake3 computes the BLAKE3 hash of a local file and returns the hexadecimal string.
func CalcBlake3(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := blake3.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}
