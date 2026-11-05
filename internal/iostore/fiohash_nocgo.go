//go:build !cgo

package iostore

import "errors"

// ErrNoBLAKE3 is returned when the build cannot compute Unreal's FIoHash.
var ErrNoBLAKE3 = errors.New("iostore: FIoHash needs BLAKE3 (libblake3), which this build does not link")

// FIoHash is unavailable without cgo and libblake3.
func FIoHash([]byte) [20]byte {
	panic(ErrNoBLAKE3)
}

// FIoHashAvailable reports whether the digest can be computed in this build.
func FIoHashAvailable() bool { return false }
