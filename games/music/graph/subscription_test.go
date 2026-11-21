package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/wricardo/drbrain/games/music/game/engine"
	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/games/music/game/session"
	"github.com/wricardo/drbrain/games/music/graph/model"
	"github.com/wricardo/drbrain/games/music/transport/websocket"
	"github.com/wricardo/drbrain/platform/mapstore"
)

type fixedPieces struct{ piece *score.Piece }

func (f fixedPieces) List() ([]score.Entry, error) {
	return []score.Entry{{ID: f.piece.ID, Piece: f.piece.Clone()}}, nil
}

func (f fixedPieces) Get(id string) (*score.Piece, error) {
	if id != f.piece.ID {
		return nil, fmt.Errorf("%w: %s", mapstore.ErrNotFound, id)
	}
	return f.piece.Clone(), nil
}

func subscriptionPiece() *score.Piece {
	measures := make([]score.Measure, 4)
	for i := range measures {
		measures[i] = score.Measure{
			ID:    fmt.Sprintf("m%03d", i+1),
			Len:   480,
			Notes: []score.Note{{Start: 0, Dur: 120, Pitch: 60 + i}},
		}
	}
	return &score.Piece{
		Version: score.Version, ID: "scale", Title: "Scale", Composer: "Anonymous",
		TempoBPM: 80, TimeSig: "4/4", Key: "C major", Clef: score.ClefTreble,
		Measures: measures,
	}
}

func receiveUpdate(t *testing.T, ch <-chan *model.SessionUpdate) *model.SessionUpdate {
	t.Helper()
	select {
	case update, ok := <-ch:
		if !ok {
			t.Fatal("subscription channel closed")
		}
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a subscription update")
		return nil
	}
}

func TestSessionUpdated_DeliversInitialSnapshotAndChanges(t *testing.T) {
	hub := websocket.NewHub()
	svc := service.New(fixedPieces{piece: subscriptionPiece()}, session.NewManager(nil, 0), hub, service.Options{})
	resolver := NewResolver(svc, hub)

	created, err := svc.CreateSession("scale", engine.Novice, "Ada", 5)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := resolver.Subscription().SessionUpdated(ctx, created.ID)
	if err != nil {
		t.Fatalf("SessionUpdated: %v", err)
	}

	first := receiveUpdate(t, ch)
	if first.Session == nil || first.Session.ID != created.ID || first.Session.Seq != int(created.Seq) {
		t.Fatalf("initial update = %+v", first)
	}
	if first.LastChange != nil {
		t.Fatalf("initial update has a last change: %+v", first.LastChange)
	}

	mutated, err := svc.SwitchMeasures(created.ID, 0, 1)
	if err != nil {
		t.Fatalf("SwitchMeasures: %v", err)
	}
	next := receiveUpdate(t, ch)
	if next.Session.Seq != int(mutated.Seq) {
		t.Fatalf("seq = %d, want %d", next.Session.Seq, mutated.Seq)
	}
	if next.LastChange == nil || next.LastChange.Kind != service.KindSwitch || next.LastChange.A != 0 || next.LastChange.B != 1 {
		t.Fatalf("last change = %+v", next.LastChange)
	}
}

func TestSessionUpdated_UnknownSessionIsNotFound(t *testing.T) {
	hub := websocket.NewHub()
	svc := service.New(fixedPieces{piece: subscriptionPiece()}, session.NewManager(nil, 0), hub, service.Options{})
	resolver := NewResolver(svc, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := resolver.Subscription().SessionUpdated(ctx, "0123456789abcdef")
	var ge *gqlerror.Error
	if !errors.As(err, &ge) || ge.Extensions["code"] != service.CodeNotFound {
		t.Fatalf("SessionUpdated = %v, want NOT_FOUND", err)
	}
}
