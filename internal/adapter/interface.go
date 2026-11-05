package adapter

import (
	"context"
	"errors"
)

var ErrMissingAdapter = errors.New("game resource writer not installed")

type Resource struct {
	Path   string
	SHA256 string
}
type Record struct {
	Resource   string
	ID         string
	SourceHash string
	JP         string
	EN         string
}
type Change struct {
	File   string
	Offset uint64
	Before []byte
	After  []byte
}
type Plan struct {
	Changes []Change
	Source  []Resource
}
type Engine interface {
	Detect(context.Context, string) ([]Resource, error)
	Extract(context.Context, []Resource) ([]Record, error)
	Validate(context.Context, []Record) error
	Plan(context.Context, []Record) (Plan, error)
	Compose(context.Context, Plan, string) error
	Verify(context.Context, Plan, string) error
}
