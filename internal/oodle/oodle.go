//go:build cgo

// Package oodle decodes Oodle LZ blocks through a locally provided shared library.
//
// The library is never bundled and never downloaded: it is supplied locally under
// .local/toolchain/vendor/oodle/ and the toolchain loads it read-only at runtime.
// A missing library is a hard, explicable refusal, never a silent fallback.
package oodle

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

typedef long long (*oodle_decompress_t)(const void *, long long, void *, long long,
	int, int, int, void *, long long, void *, void *, void *, long long, int);

static void *load_lib(const char *path, char **err) {
	// RTLD_NOW: refuse a library that cannot resolve its own symbols.
	void *h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (h == 0) {
		*err = (char *)dlerror();
	}
	return h;
}

static void *load_sym(void *h, const char *name, char **err) {
	void *p = dlsym(h, name);
	if (p == 0) {
		*err = (char *)dlerror();
	}
	return p;
}

static long long call_decompress(void *fn, const void *src, long long srcLen,
	void *dst, long long dstLen) {
	oodle_decompress_t f = (oodle_decompress_t)fn;
	// fuzzSafe=0 (this compressor rejects fuzz-safe), no CRC check, no verbosity.
	return f(src, srcLen, dst, dstLen, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0);
}

static long long call_compress(void *fn, const void *src, long long srcLen,
	void *dst, long long dstLen, int level, int compressor) {
	typedef long long (*oodle_compress_t)(int, const void *, long long, void *, int,
		const void *, const void *, void *, void *, long long);
	oodle_compress_t f = (oodle_compress_t)fn;
	return f(compressor, src, srcLen, dst, level, 0, 0, 0, 0, 0);
}

static void close_lib(void *h) { dlclose(h); }
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"
)

// CompressorKraken is the Oodle compressor id used by Unreal for block streams.
const CompressorKraken = 8

// MaxBlock bounds a single decode; pak blocks are 64 KiB in the titles seen so far.
const MaxBlock = 1 << 24

type library struct {
	handle   unsafe.Pointer
	decode   unsafe.Pointer
	compress unsafe.Pointer
}

var (
	mu   sync.Mutex
	libs = map[string]*library{}
)

// Load opens a shared library once and caches it for the process.
func Load(path string) error {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := libs[path]; ok {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("oodle library not available at %s: %w", path, err)
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var cerr *C.char
	h := C.load_lib(cpath, &cerr)
	if h == nil {
		return fmt.Errorf("oodle: dlopen %s: %s", path, C.GoString(cerr))
	}
	name := C.CString("OodleLZ_Decompress")
	defer C.free(unsafe.Pointer(name))
	var serr *C.char
	decode := C.load_sym(h, name, &serr)
	if decode == nil {
		C.close_lib(h)
		return fmt.Errorf("oodle: %s has no OodleLZ_Decompress: %s", path, C.GoString(serr))
	}
	cname := C.CString("OodleLZ_Compress")
	defer C.free(unsafe.Pointer(cname))
	var cerr2 *C.char
	compress := C.load_sym(h, cname, &cerr2)
	libs[path] = &library{handle: h, decode: decode, compress: compress}
	return nil
}

func get(path string) (*library, error) {
	mu.Lock()
	defer mu.Unlock()
	if l, ok := libs[path]; ok {
		return l, nil
	}
	return nil, errors.New("oodle: library not loaded: " + path)
}

// Decode expands one compressed block into exactly rawSize bytes.
func Decode(path string, comp []byte, rawSize int) ([]byte, error) {
	if rawSize <= 0 || rawSize > MaxBlock {
		return nil, fmt.Errorf("oodle: implausible raw size %d", rawSize)
	}
	if len(comp) == 0 {
		return nil, errors.New("oodle: empty input")
	}
	lib, err := get(path)
	if err != nil {
		return nil, err
	}
	out := make([]byte, rawSize)
	got := C.call_decompress(lib.decode, unsafe.Pointer(&comp[0]), C.longlong(len(comp)),
		unsafe.Pointer(&out[0]), C.longlong(rawSize))
	if got <= 0 {
		return nil, fmt.Errorf("oodle: decode failed (%d bytes in, %d expected)", len(comp), rawSize)
	}
	if int(got) != rawSize {
		return nil, fmt.Errorf("oodle: decoded %d of %d bytes", got, rawSize)
	}
	return out, nil
}

// Compress is used by tests to build self-contained fixtures; production never compresses.
func Compress(path string, raw []byte, level int) ([]byte, error) {
	lib, err := get(path)
	if err != nil {
		return nil, err
	}
	if lib.compress == nil {
		return nil, errors.New("oodle: library has no OodleLZ_Compress")
	}
	bound := len(raw) + len(raw)/2 + 4096
	out := make([]byte, bound)
	got := C.call_compress(lib.compress, unsafe.Pointer(&raw[0]), C.longlong(len(raw)),
		unsafe.Pointer(&out[0]), C.longlong(bound), C.int(level), C.int(CompressorKraken))
	if got <= 0 {
		return nil, fmt.Errorf("oodle: compress failed (%d bytes)", len(raw))
	}
	return out[:int(got)], nil
}
