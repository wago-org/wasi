package core

import "testing"

func FuzzGuestBytes(f *testing.F) {
	f.Add([]byte("path"), uint32(0), uint32(4))
	f.Add([]byte{}, uint32(^uint32(0)), uint32(1))
	f.Fuzz(func(t *testing.T, mem []byte, ptr, n uint32) { _, _ = guestBytes(mem, ptr, n) })
}

func FuzzIOVecDecoder(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0}, uint32(0), uint32(1))
	f.Add([]byte{}, uint32(^uint32(0)), uint32(^uint32(0)))
	f.Fuzz(func(t *testing.T, mem []byte, ptr, count uint32) {
		e := &Plugin{cfg: Config{MaxIOVecs: 64}}
		_, _ = e.iovecs(mem, ptr, count)
	})
}

func FuzzPollSubscriptionDecoder(f *testing.F) {
	f.Add(make([]byte, 48), uint32(1))
	f.Add([]byte{}, uint32(^uint32(0)))
	f.Fuzz(func(t *testing.T, mem []byte, count uint32) {
		if len(mem) > 4096 {
			t.Skip()
		}
		e := newTestPlugin(t, Config{MaxSubscriptionsPerPoll: 64})
		space := make([]byte, len(mem)+4096)
		copy(space, mem)
		result := []uint64{0}
		e.pollOneoff(testModule{space}, []uint64{0, uint64(len(mem)), uint64(count), uint64(len(space) - 4)}, result)
	})
}
