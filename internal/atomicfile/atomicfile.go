// Package atomicfile writes files so that readers never observe a partial one.
//
// Every write lands in a temp file in the destination directory and is then
// renamed into place. Rename within a filesystem is atomic, so a file either
// exists complete or does not exist at all. An interrupted sync therefore
// leaves no truncated .eml that a later run would mistake for a full message.
package atomicfile

import (
	"io"
	"os"
	"path/filepath"
)

// Write creates path with the given contents, replacing any existing file.
// Parent directories are created as needed.
func Write(path string, data []byte, perm os.FileMode) error {
	return WriteFrom(path, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}, perm)
}

// WriteFrom creates path from a streaming writer, so large attachments never
// need to be held in memory in full.
func WriteFrom(path string, fn func(io.Writer) error, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".remail-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// On any failure past this point the temp file must not survive.
	defer func() {
		if tmpName != "" {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := fn(tmp); err != nil {
		return err
	}
	// Flush to disk before the rename; otherwise a crash can leave the renamed
	// file present but empty.
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	tmpName = "" // committed; disarm the cleanup
	return nil
}
