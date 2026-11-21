// Pure arrangement helpers shared by the score strip, the synth and the tests.
import { flipNotes } from './engrave';
import type { Excerpt, NoteEvent, Session, Slot } from './types';

/** Flattened absolute event stream for the given slots, in arrangement order. */
export function arrangementEvents(excerpt: Excerpt, slots: Slot[]): NoteEvent[] {
	const byID = new Map(excerpt.measures.map((m) => [m.id, m]));
	const out: NoteEvent[] = [];
	let offset = 0;
	for (const slot of [...slots].sort((a, b) => a.position - b.position)) {
		const measure = byID.get(slot.measureID);
		if (!measure) continue;
		for (const note of flipNotes(measure.notes, measure.len, { h: slot.flipH, v: slot.flipV }, excerpt.axis)) {
			out.push({ at: offset + note.at, dur: note.dur, pitch: note.pitch });
		}
		offset += measure.len;
	}
	return out;
}

/** Only the selected slots, in arrangement order; empty means the whole arrangement. */
export function playableEvents(session: Session, positions: number[]): NoteEvent[] {
	const wanted = positions.length ? new Set(positions) : null;
	const slots = wanted ? session.slots.filter((s) => wanted.has(s.position)) : session.slots;
	return arrangementEvents(session.excerpt, slots);
}

/** The recording the player must reproduce. */
export function targetEvents(excerpt: Excerpt): NoteEvent[] {
	return excerpt.notes.map((n) => ({ at: n.at, dur: n.dur, pitch: n.pitch }));
}
