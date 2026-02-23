package search

import (
	"os"
	"path/filepath"

	"github.com/vmihailenco/msgpack/v5"
)

// writeMsgpackSnapshot creates parent directories, writes snapshot to file, and encodes msgpack.
func writeMsgpackSnapshot(path string, snapshot any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return msgpack.NewEncoder(file).Encode(snapshot)
}
