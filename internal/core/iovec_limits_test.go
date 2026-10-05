package core

import (
	"encoding/binary"
	"os"
	"testing"
)

type countedVectorStream struct{ reads, writes int }

func (s *countedVectorStream) Read(buf []byte) (int, error) {
	s.reads++
	return len(buf), nil
}

func (s *countedVectorStream) Write(buf []byte) (int, error) {
	s.writes++
	return len(buf), nil
}

func aggregateIOVecMemory(count, length uint32) []byte {
	data := count*8 + 4 // table followed by the output count and one shared buffer
	mem := make([]byte, int(data)+int(length))
	for i := uint32(0); i < count; i++ {
		binary.LittleEndian.PutUint32(mem[i*8:], data)
		binary.LittleEndian.PutUint32(mem[i*8+4:], length)
	}
	return mem
}

func TestIOVecAggregateLimit(t *testing.T) {
	const count, length = uint32(1024), uint32(4 << 20)
	mem := aggregateIOVecMemory(count, length)
	e := &Plugin{cfg: Config{MaxIOVecs: count}}
	if _, code := e.iovecs(mem, 0, count); code != wasiEOverflow {
		t.Fatalf("4 GiB aggregate = errno %d, want EOVERFLOW", code)
	}

	// Every individual view is valid; removing one byte makes the aggregate
	// exactly the largest Preview 1 size, which must remain valid.
	binary.LittleEndian.PutUint32(mem[(count-1)*8+4:], length-1)
	bufs, code := e.iovecs(mem, 0, count)
	if code != wasiOK || len(bufs) != int(count) || len(bufs[count-1]) != int(length-1) {
		t.Fatalf("MaxUint32 aggregate = errno %d, vectors %d", code, len(bufs))
	}
}

func TestDescriptorIORejectsUnrepresentableAggregate(t *testing.T) {
	const count, length = uint32(1024), uint32(4 << 20)
	for _, operation := range []string{"read", "write", "pread", "pwrite"} {
		t.Run(operation, func(t *testing.T) {
			// Repeated views require only 4 MiB of guest memory, not 4 GiB.
			mem := aggregateIOVecMemory(count, length)
			resultPtr := count * 8
			binary.LittleEndian.PutUint32(mem[resultPtr:], 0x12345678)
			stream := new(countedVectorStream)
			e := newTestPlugin(t, Config{Stdin: stream, Stdout: stream})
			result := make([]uint64, 1)
			module := testModule{mem}
			switch operation {
			case "read":
				e.fdRead(module, []uint64{0, 0, uint64(count), uint64(resultPtr)}, result)
			case "write":
				e.fdWrite(module, []uint64{1, 0, uint64(count), uint64(resultPtr)}, result)
			default:
				// The discard device keeps the red positional-write control fast
				// and avoids writing any test data to disk.
				file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				e.fs.fds[3] = &fdEntry{file: file, rights: rightFDRead | rightFDWrite | rightFDSeek}
				params := []uint64{3, 0, uint64(count), 0, uint64(resultPtr)}
				if operation == "pread" {
					e.fdPread(module, params, result)
				} else {
					e.fdPwrite(module, params, result)
				}
			}
			if result[0] != wasiEOverflow {
				t.Errorf("%s = errno %d, want EOVERFLOW", operation, result[0])
			}
			if stream.reads != 0 || stream.writes != 0 {
				t.Errorf("host I/O before rejection: reads %d, writes %d", stream.reads, stream.writes)
			}
			if got := binary.LittleEndian.Uint32(mem[resultPtr:]); got != 0x12345678 {
				t.Errorf("output count changed before rejection: %#x", got)
			}
		})
	}
}

func BenchmarkIOVecDecoder(b *testing.B) {
	for _, count := range []uint32{8, 1024} {
		name := "8 vectors"
		if count == 1024 {
			name = "1024 vectors"
		}
		b.Run(name, func(b *testing.B) {
			mem := aggregateIOVecMemory(count, 32)
			e := &Plugin{cfg: Config{MaxIOVecs: 1024}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				bufs, code := e.iovecs(mem, 0, count)
				if code != wasiOK || len(bufs) != int(count) {
					b.Fatal(code)
				}
			}
		})
	}
}
