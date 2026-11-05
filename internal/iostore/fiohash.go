//go:build cgo

package iostore

// FIoHash is Unreal's 20-byte content hash: the first 20 bytes of BLAKE3-256 over the chunk's uncompressed
// payload. The system BLAKE3 library is used rather than a re-implementation, because the value is compared
// byte for byte by the engine and a home-grown digest would only agree with itself.

/*
#cgo LDFLAGS: -lblake3
#include <blake3.h>
#include <stdint.h>
#include <stddef.h>

static void unburger_fio_hash(const void *p, size_t n, uint8_t *out) {
	blake3_hasher h;
	blake3_hasher_init(&h);
	blake3_hasher_update(&h, p, n);
	blake3_hasher_finalize(&h, out, 20);
}
*/
import "C"

import "unsafe"

// FIoHash returns the 20-byte hash the engine stores for a chunk.
func FIoHash(data []byte) [20]byte {
	var out [20]byte
	var p unsafe.Pointer
	if len(data) > 0 {
		p = unsafe.Pointer(&data[0])
	}
	C.unburger_fio_hash(p, C.size_t(len(data)), (*C.uint8_t)(unsafe.Pointer(&out[0])))
	return out
}

// FIoHashAvailable reports whether the digest can be computed in this build.
func FIoHashAvailable() bool { return true }
