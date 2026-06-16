package assets

import (
	"crypto/sha256"
	"encoding/hex"
)

func Hash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func ShardedKey(hash string) string {
	prefix := hash
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return "assets/sha256/" + prefix + "/" + hash
}
