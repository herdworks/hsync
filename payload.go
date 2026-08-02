package hsync

import (
	"os"
)

// Payload is the deployable directory to which a Changeset is applied. It has
// no knowledge of how its ordinary files were generated.
type Payload struct {
	Root string
}

func writePayloadFile(root, relative string, contents []byte) error {
	if err := validateRelativePath(relative); err != nil {
		return err
	}
	filename, err := safeWriteTarget(root, relative)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, contents, 0o644)
}
