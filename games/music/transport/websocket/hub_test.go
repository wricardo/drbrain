package websocket

import (
	"context"
	"testing"

	"github.com/wricardo/drbrain/games/music/game/service"
)

func snapshot(id string, seq uint64) *service.Session {
	return &service.Session{ID: id, Seq: seq}
}

func TestBroadcast_LatestWinsDropsIntermediate(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := hub.Subscribe(ctx, "s")

	// The channel holds 8; 12 broadcasts evict the four oldest snapshots.
	for seq := uint64(1); seq <= 12; seq++ {
		hub.Broadcast(snapshot("s", seq))
	}
	if got := len(ch); got != subscriberBuffer {
		t.Fatalf("buffered = %d, want %d", got, subscriberBuffer)
	}
	first := <-ch
	if first.Seq != 5 {
		t.Fatalf("oldest delivered seq = %d, want 5", first.Seq)
	}
	last := first
	for range subscriberBuffer - 1 {
		last = <-ch
	}
	if last.Seq != 12 {
		t.Fatalf("newest delivered seq = %d, want 12", last.Seq)
	}
}

func TestBroadcast_DropsStaleAndOtherSessions(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := hub.Subscribe(ctx, "s")

	hub.Broadcast(snapshot("other", 9))
	hub.Broadcast(snapshot("s", 5))
	hub.Broadcast(snapshot("s", 4)) // stale: must not evict or enqueue

	if got := len(ch); got != 1 {
		t.Fatalf("buffered = %d, want 1", got)
	}
	if got := (<-ch).Seq; got != 5 {
		t.Fatalf("seq = %d, want 5", got)
	}
}

func TestSubscribe_ClosesOnContextCancel(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	ch := hub.Subscribe(ctx, "s")
	if hub.SubscriberCount("s") != 1 {
		t.Fatalf("subscribers = %d, want 1", hub.SubscriberCount("s"))
	}
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel delivered a value instead of closing")
	}
	if hub.SubscriberCount("s") != 0 {
		t.Fatalf("subscribers = %d after cancel, want 0", hub.SubscriberCount("s"))
	}
}

func TestClose_ClosesSubscribersAndIsIdempotent(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := hub.Subscribe(ctx, "a")
	second := hub.Subscribe(ctx, "b")

	hub.Close()
	hub.Close()

	for name, ch := range map[string]<-chan *service.Session{"a": first, "b": second} {
		if _, ok := <-ch; ok {
			t.Fatalf("%s channel delivered a value instead of closing", name)
		}
	}
	if hub.SubscriberCount("a") != 0 || hub.SubscriberCount("b") != 0 {
		t.Fatal("Close left subscribers behind")
	}
	hub.Broadcast(snapshot("a", 1)) // must not panic or send

	late := hub.Subscribe(ctx, "a")
	if _, ok := <-late; ok {
		t.Fatal("subscribing after Close returned a live channel")
	}
}
