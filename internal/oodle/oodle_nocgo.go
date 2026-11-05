//go:build !cgo

package oodle

import "errors"

// ErrUnavailable is returned when the toolchain was built without cgo.
var ErrUnavailable = errors.New("oodle: built without cgo, no dynamic loader available")

func Load(string) error { return ErrUnavailable }

func Decode(string, []byte, int) ([]byte, error) { return nil, ErrUnavailable }

func Compress(string, []byte, int) ([]byte, error) { return nil, ErrUnavailable }
