package hypersync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Identifier identifies an immutable manifest or blob by the SHA-256 of its
// exact bytes.
type Identifier string

const hashPrefix = "sha256:"

// Validate reports whether identifier has the required
// sha256:<lowercase-hex> form.
func (identifier Identifier) Validate() error {
	value := string(identifier)
	if !strings.HasPrefix(value, hashPrefix) || len(value) != len(hashPrefix)+sha256.Size*2 {
		return fmt.Errorf("%w: invalid identifier %q", ErrInvalidState, value)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(value, hashPrefix)); err != nil {
		return fmt.Errorf("%w: invalid identifier %q", ErrInvalidState, value)
	}
	if strings.ToLower(value) != value {
		return fmt.Errorf("%w: identifier must use lowercase hexadecimal", ErrInvalidState)
	}
	return nil
}

// Filename returns identifier without its sha256: prefix.
func (identifier Identifier) Filename() string {
	return strings.TrimPrefix(string(identifier), hashPrefix)
}

// NewIdentifier returns the SHA-256 identifier for bytes.
func NewIdentifier(bytes []byte) Identifier {
	digest := sha256.Sum256(bytes)
	return Identifier(hashPrefix + hex.EncodeToString(digest[:]))
}
