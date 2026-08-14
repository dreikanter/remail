// Package atomicfile writes via a temp file plus rename, so readers never
// observe a partial file and an interrupted run leaves nothing that looks
// complete.
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

// WriteFrom creates path from a streaming writer, for content too large to
// hold in memory.
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
	// Without this a crash can leave the renamed file present but empty.
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
