//go:build linux || (darwin && !go1.27)

package p2

import "sync"

// Match os.ReadDir's bounded native buffer, and reuse it between streams.
var directoryBuffers = sync.Pool{New: func() any { return new([8192]byte) }}

type directoryReadState struct {
	buffer *[8192]byte
	pos    int
	end    int
}

func closeDirectoryStream(d *directoryStream) error {
	// Keep the descriptor alive for a native read/stat until both have finished.
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reader.releaseBuffer()
	return d.file.Close()
}

func (r *directoryReadState) releaseBuffer() {
	if r.buffer != nil {
		directoryBuffers.Put(r.buffer)
		r.buffer = nil
	}
	r.pos, r.end = 0, 0
}
