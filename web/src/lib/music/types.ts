// Types for the Music Region UI. The unified GraphQL schema generates the
// shared shapes in ../graphql-types.ts; this file keeps the game-local names
// screens use and the small shapes the audio/engraver helpers speak.
export type {
	Music_Difficulty as Difficulty,
	Music_Excerpt as Excerpt,
	Music_LastChange as LastChange,
	Music_Measure as Measure,
	Music_Note as Note,
	Music_PlacementInput as PlacementInput,
	Music_Session as Session,
	Music_SessionUpdate as SessionUpdate,
	Music_Slot as Slot
} from '../graphql-types';

/** One sounding note on an absolute tick timeline (PPQ 480). */
export interface NoteEvent {
	at: number;
	dur: number;
	pitch: number;
}

/** A per-measure distortion: retrograde (h) and/or inversion (v). */
export interface Flip {
	h: boolean;
	v: boolean;
}
