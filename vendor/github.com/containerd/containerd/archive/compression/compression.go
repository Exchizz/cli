/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package compression

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"sync"
)

type (
	// Compression is the state represents if compressed or not.
	Compression int
)

const (
	// Uncompressed represents the uncompressed.
	Uncompressed Compression = iota
	// Gzip is gzip compression algorithm.
	Gzip
)

var (
	bufioReader32KPool = &sync.Pool{
		New: func() interface{} { return bufio.NewReaderSize(nil, 32*1024) },
	}
)

// DecompressReadCloser include the stream after decompress and the compress method detected.
type DecompressReadCloser interface {
	io.ReadCloser
	// GetCompression returns the compress method which is used before decompressing
	GetCompression() Compression
}

type readCloserWrapper struct {
	io.Reader
	compression Compression
	closer      func() error
}

func (r *readCloserWrapper) Close() error {
	if r.closer != nil {
		return r.closer()
	}
	return nil
}

func (r *readCloserWrapper) GetCompression() Compression {
	return r.compression
}

type writeCloserWrapper struct {
	io.Writer
	closer func() error
}

func (w *writeCloserWrapper) Close() error {
	if w.closer != nil {
		w.closer()
	}
	return nil
}

// DetectCompression detects the compression algorithm of the source.
func DetectCompression(source []byte) Compression {
	for compression, m := range map[Compression][]byte{
		Gzip: {0x1F, 0x8B, 0x08},
	} {
		if len(source) < len(m) {
			// Len too short
			continue
		}
		if bytes.Equal(m, source[:len(m)]) {
			return compression
		}
	}
	return Uncompressed
}

// DecompressStream decompresses the archive and returns a ReaderCloser with the decompressed archive.
func DecompressStream(archive io.Reader) (DecompressReadCloser, error) {
	buf := bufioReader32KPool.Get().(*bufio.Reader)
	buf.Reset(archive)
	bs, err := buf.Peek(10)
	if err != nil && err != io.EOF {
		// Note: we'll ignore any io.EOF error because there are some odd
		// cases where the layer.tar file will be empty (zero bytes) and
		// that results in an io.EOF from the Peek() call. So, in those
		// cases we'll just treat it as a non-compressed stream and
		// that means just create an empty layer.
		// See Issue docker/docker#18170
		return nil, err
	}

	closer := func() error {
		buf.Reset(nil)
		bufioReader32KPool.Put(buf)
		return nil
	}
	switch compression := DetectCompression(bs); compression {
	case Uncompressed:
		readBufWrapper := &readCloserWrapper{buf, compression, closer}
		return readBufWrapper, nil
	case Gzip:
		gzReader, err := gzip.NewReader(buf)
		if err != nil {
			return nil, err
		}
		readBufWrapper := &readCloserWrapper{gzReader, compression, closer}
		return readBufWrapper, nil
	default:
		return nil, fmt.Errorf("unsupported compression format %s", (&compression).Extension())
	}
}

// CompressStream compresseses the dest with specified compression algorithm.
func CompressStream(dest io.Writer, compression Compression) (io.WriteCloser, error) {
	switch compression {
	case Uncompressed:
		return &writeCloserWrapper{dest, nil}, nil
	case Gzip:
		return gzip.NewWriter(dest), nil
	default:
		return nil, fmt.Errorf("unsupported compression format %s", (&compression).Extension())
	}
}

// Extension returns the extension of a file that uses the specified compression algorithm.
func (compression *Compression) Extension() string {
	switch *compression {
	case Gzip:
		return "gz"
	}
	return ""
}
