//go:build cgo

package pak

// The reference archives are compressed with the C zlib. Go's compress/flate emits a slightly larger stream for the
// same input, which is valid but makes byte comparison against the shipped tooling impossible; linking zlib keeps
// the produced blocks identical to what the engine's own packer writes.

/*
#cgo LDFLAGS: -lz
#include <zlib.h>
#include <stdlib.h>

static int unburger_deflate(const unsigned char *in, unsigned long inlen,
                            unsigned char *out, unsigned long *outlen, int level) {
	return compress2(out, outlen, in, inlen, level);
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ErrDeflate is returned when zlib refuses the input.
var ErrDeflate = errors.New("pak: deflate failed")

// deflate compresses one block with zlib at the given level.
func deflate(data []byte, level int) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	bound := C.compressBound(C.ulong(len(data)))
	out := make([]byte, int(bound))
	outLen := C.ulong(len(out))
	rc := C.unburger_deflate((*C.uchar)(unsafe.Pointer(&data[0])), C.ulong(len(data)),
		(*C.uchar)(unsafe.Pointer(&out[0])), &outLen, C.int(level))
	if rc != 0 {
		return nil, ErrDeflate
	}
	return out[:int(outLen)], nil
}
