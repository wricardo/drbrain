package graph

import (
	"time"

	"github.com/wricardo/drbrain/games/music/game/engine"
	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/games/music/graph/model"
)

func fromDifficulty(d model.Difficulty) engine.Difficulty {
	switch d {
	case model.DifficultyNovice:
		return engine.Novice
	case model.DifficultyExpert:
		return engine.Expert
	case model.DifficultyGenius:
		return engine.Genius
	}
	return 0
}

func toDifficulty(d engine.Difficulty) model.Difficulty {
	switch d {
	case engine.Novice:
		return model.DifficultyNovice
	case engine.Expert:
		return model.DifficultyExpert
	case engine.Genius:
		return model.DifficultyGenius
	}
	return model.DifficultyNovice
}

func toNotes(notes []score.Note) []*model.Note {
	out := make([]*model.Note, len(notes))
	for i, n := range notes {
		out[i] = &model.Note{At: n.Start, Dur: n.Dur, Pitch: n.Pitch}
	}
	return out
}

func toMeasures(measures []score.Measure) []*model.Measure {
	out := make([]*model.Measure, len(measures))
	for i, m := range measures {
		out[i] = &model.Measure{ID: m.ID, Len: m.Len, Notes: toNotes(m.Notes)}
	}
	return out
}

// toTargetNotes flattens the piece onto the absolute tick timeline: the
// recording the browser synthesizes and the stream the solved check compares.
func toTargetNotes(p *score.Piece) []*model.Note {
	total := 0
	for _, m := range p.Measures {
		total += len(m.Notes)
	}
	out := make([]*model.Note, 0, total)
	offset := 0
	for _, m := range p.Measures {
		for _, n := range m.Notes {
			out = append(out, &model.Note{At: offset + n.Start, Dur: n.Dur, Pitch: n.Pitch})
		}
		offset += m.Len
	}
	return out
}

func toExcerpt(p *score.Piece) *model.Excerpt {
	if p == nil {
		return nil
	}
	return &model.Excerpt{
		ID: p.ID, Title: p.Title, Composer: p.Composer, TempoBpm: p.TempoBPM,
		TimeSig: p.TimeSig, Key: p.Key, Clef: p.Clef, Axis: p.Axis,
		Measures: toMeasures(p.Measures),
		Notes:    toTargetNotes(p),
	}
}

func toSlots(slots []engine.Placement) []*model.Slot {
	out := make([]*model.Slot, len(slots))
	for i, p := range slots {
		out[i] = &model.Slot{
			Position: i, MeasureID: p.MeasureID,
			FlipH: p.Transform.FlipH, FlipV: p.Transform.FlipV,
		}
	}
	return out
}

func toLastChange(ch *service.Change) *model.LastChange {
	if ch == nil {
		return nil
	}
	return &model.LastChange{Kind: ch.Kind, A: ch.A, B: ch.B}
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func displayName(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

// toSession converts an immutable session snapshot (a Clone). It is the only
// Session conversion: queries, mutations and subscriptions all go through it.
func toSession(s *service.Session) *model.Session {
	if s == nil {
		return nil
	}
	return &model.Session{
		ID:            s.ID,
		DisplayName:   displayName(s.DisplayName),
		Difficulty:    toDifficulty(s.Difficulty),
		Excerpt:       toExcerpt(s.Piece),
		Slots:         toSlots(s.Slots),
		Solved:        s.Solved,
		Seq:           int(s.Seq),
		Wins:          s.Wins,
		PercentPerWin: s.Difficulty.PercentPerWin(),
		CreatedAt:     formatTime(s.CreatedAt),
		LastActionAt:  formatTime(s.LastActionAt),
		LastChange:    toLastChange(s.LastChange),
	}
}

func toSessions(list []*service.Session) []*model.Session {
	out := make([]*model.Session, len(list))
	for i, s := range list {
		out[i] = toSession(s)
	}
	return out
}

func toExcerpts(pieces []*score.Piece) []*model.Excerpt {
	out := make([]*model.Excerpt, len(pieces))
	for i, p := range pieces {
		out[i] = toExcerpt(p)
	}
	return out
}

func fromPlacements(in []*model.PlacementInput) []engine.Placement {
	out := make([]engine.Placement, len(in))
	for i, p := range in {
		if p == nil {
			continue
		}
		out[i] = engine.Placement{
			MeasureID: p.MeasureID,
			Transform: engine.Transform{FlipH: p.FlipH, FlipV: p.FlipV},
		}
	}
	return out
}
