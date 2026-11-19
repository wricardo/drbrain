package httpserve

import (
	"errors"
	"testing"
)

// frame builds a client frame header plus its payload. The guard parses the
// stream, so the payload bytes must be fed too.
func frame(final bool, opcode byte, payload int) []byte {
	b0 := opcode
	if final {
		b0 |= 0x80
	}
	return append([]byte{b0, byte(payload)}, make([]byte, payload)...)
}

func TestWSFrameGuardAcceptsMessageUnderLimit(t *testing.T) {
	g := wsFrameGuard{limit: 10}
	if err := g.feed(frame(true, 0x1, 10)); err != nil {
		t.Fatalf("feed: %v", err)
	}
	if g.err != nil {
		t.Fatalf("guard tripped: %v", g.err)
	}
}

func TestWSFrameGuardRejectsOversizedMessage(t *testing.T) {
	g := wsFrameGuard{limit: 10}
	err := g.feed(frame(true, 0x1, 11))
	if !errors.Is(err, errWSMessageTooLarge) {
		t.Fatalf("err = %v, want errWSMessageTooLarge", err)
	}
}

func TestWSFrameGuardCountsFragments(t *testing.T) {
	g := wsFrameGuard{limit: 10}
	if err := g.feed(frame(false, 0x1, 6)); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	// 6 + 6 > 10: the second fragment of the same message must trip.
	if err := g.feed(frame(true, 0x0, 6)); !errors.Is(err, errWSMessageTooLarge) {
		t.Fatalf("second fragment err = %v, want errWSMessageTooLarge", err)
	}
}

func TestWSFrameGuardIgnoresControlFrames(t *testing.T) {
	g := wsFrameGuard{limit: 1}
	// A 4-byte pong is control traffic, not part of a message.
	if err := g.feed(frame(true, 0xA, 4)); err != nil {
		t.Fatalf("control frame: %v", err)
	}
}
