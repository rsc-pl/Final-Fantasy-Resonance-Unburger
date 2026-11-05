//go:build !cgo

package pak

import (
	"bytes"
	"compress/zlib"
)

// deflate falls back to the Go implementation when cgo is unavailable.
func deflate(data []byte, level int) ([]byte, error) {
	var b bytes.Buffer
	w, err := zlib.NewWriterLevel(&b, level)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
