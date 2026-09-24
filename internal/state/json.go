package state

import (
	"encoding/json"
	"os"
)

func MarshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func WriteJSONAtomic(path string, value any, perm os.FileMode) error {
	data, err := MarshalJSON(value)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data, perm)
}

func WriteJSONAtomicRoot(root *os.Root, path string, value any, perm os.FileMode) error {
	data, err := MarshalJSON(value)
	if err != nil {
		return err
	}
	return WriteFileAtomicRoot(root, path, data, perm)
}
