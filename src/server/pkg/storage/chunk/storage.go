package chunk

import (
	"context"

	"github.com/pachyderm/pachyderm/src/server/pkg/obj"
)

const (
	prefix = "chunks"
)

// Annotation is used to associate information with a set of bytes
// written into the chunk storage layer.
type Annotation struct {
	Offset      int64
	RefDataRefs []*DataRef
	NextDataRef *DataRef
	Meta        interface{}
}

// Storage is the abstraction that manages chunk storage.
type Storage struct {
	objC obj.Client
}

// NewStorage creates a new Storage.
func NewStorage(objC obj.Client) *Storage {
	return &Storage{
		objC: objC,
	}
}

// NewReader creates an io.ReadCloser for a chunk.
// (bryce) The whole chunk is in-memory right now. Could be a problem with
// concurrency, particularly the merge process.
// May want to handle concurrency here (pass in multiple data refs)
func (s *Storage) NewReader(ctx context.Context, f ...ReaderFunc) *Reader {
	return newReader(ctx, s.objC, f...)
}

// NewWriter creates an io.WriteCloser for a stream of bytes to be chunked.
// Chunks are created based on the content, then hashed and deduplicated/uploaded to
// object storage.
// The callback arguments are the chunk hash and content.
func (s *Storage) NewWriter(ctx context.Context, averageBits int, f WriterFunc) *Writer {
	return newWriter(ctx, s.objC, averageBits, f)
}

// List lists all of the chunks in object storage.
func (s *Storage) List(ctx context.Context, f func(string) error) error {
	return s.objC.Walk(ctx, prefix, f)
}

// DeleteAll deletes all of the chunks in object storage.
func (s *Storage) DeleteAll(ctx context.Context) error {
	return s.objC.Walk(ctx, prefix, func(hash string) error {
		return s.objC.Delete(ctx, hash)
	})
}