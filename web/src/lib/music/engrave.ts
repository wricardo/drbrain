// VexFlow engraving for Music Region measures.
//
// The score model is tick-based (PPQ 480). This module is the only place that
// knows VexFlow: it maps ticks to note values, fills gaps with rests, applies
// the two involutive flips, and renders one measure per stave so cards can tile
// into a continuous system. VexFlow is imported dynamically so the module can
// be unit-tested without a DOM and never reaches the server bundle.
import type { Flip, Measure, Note } from './types';

/** Ticks per quarter note; the score format's fixed resolution. */
export const PPQ = 480;
/** The fixed inversion axis: B4, the middle line of the treble staff. */
export const AXIS = 71;

/** Ticks -> VexFlow duration code. Only renderable values exist (no tuplets). */
export const VEX_DURATION: Readonly<Record<number, string>> = {
	1920: 'w',
	1440: 'hd',
	960: 'h',
	720: 'qd',
	480: 'q',
	360: '8d',
	240: '8',
	180: '16d',
	120: '16',
	60: '32'
};

const DOTTED = new Set([1440, 720, 360, 180]);

/** True for the dotted note values; the engraver must attach a Dot glyph. */
export function isDotted(ticks: number): boolean {
	return DOTTED.has(ticks);
}

/** VexFlow duration code for a tick length. Throws for unsupported values. */
export function vexDuration(ticks: number): string {
	const code = VEX_DURATION[ticks];
	if (!code) throw new Error(`music: ${ticks} ticks is not a renderable note value`);
	return code;
}

export interface MeasurePart {
	/** Start tick inside the measure (0..len). */
	at: number;
	/** Duration in ticks. */
	dur: number;
	/** Chord pitches; empty for a synthesized rest. */
	pitches: number[];
}

/** Applies the flips with the engine's exact formulas, then sorts by (start, pitch). */
export function flipNotes(notes: Note[], len: number, flip: Flip, axis = AXIS): Note[] {
	const out = notes.map((n) => ({
		at: flip.h ? len - n.at - n.dur : n.at,
		dur: n.dur,
		pitch: flip.v ? 2 * axis - n.pitch : n.pitch
	}));
	out.sort((a, b) => a.at - b.at || a.pitch - b.pitch);
	return out;
}

/** The renderable durations greedy rest synthesis draws from, longest first. */
const REST_VALUES = [1920, 1440, 960, 720, 480, 360, 240, 180, 120, 60] as const;

/** Splits a gap (multiple of 60 ticks) into renderable note values. */
export function decomposeRest(ticks: number): number[] {
	if (!Number.isInteger(ticks) || ticks < 0 || ticks % 60 !== 0) {
		throw new Error(`music: cannot rest ${ticks} ticks (needs a multiple of 60)`);
	}
	const out: number[] = [];
	let left = ticks;
	for (const value of REST_VALUES) {
		while (left >= value) {
			out.push(value);
			left -= value;
		}
	}
	return out;
}

/**
 * Groups the flipped notes by (start, duration) into chords, longest first at a
 * shared start so the melody keeps the upper voice.
 */
function noteGroups(notes: Note[], len: number, flip: Flip, axis: number): MeasurePart[] {
	const flipped = flipNotes(notes, len, flip, axis);
	const byKey = new Map<string, MeasurePart>();
	for (const note of flipped) {
		const key = `${note.at}:${note.dur}`;
		const group = byKey.get(key);
		if (group) group.pitches.push(note.pitch);
		else byKey.set(key, { at: note.at, dur: note.dur, pitches: [note.pitch] });
	}
	return [...byKey.values()].sort((a, b) => a.at - b.at || b.dur - a.dur);
}

/** Adds synthesized rests so the parts cover 0..len with no gaps. */
function fillRests(parts: MeasurePart[], len: number): MeasurePart[] {
	const out: MeasurePart[] = [];
	let cursor = 0;
	for (const part of parts) {
		if (part.at > cursor) {
			for (const rest of decomposeRest(part.at - cursor)) out.push({ at: cursor, dur: rest, pitches: [] });
		}
		out.push(part);
		cursor = Math.max(cursor, part.at + part.dur);
	}
	if (cursor < len) {
		for (const rest of decomposeRest(len - cursor)) out.push({ at: cursor, dur: rest, pitches: [] });
	}
	return out;
}

/**
 * Splits a measure into one part list per engraving voice. Ordinary monophonic
 * or chordal bars produce one voice; overlapping notes (a chord whose members
 * have different durations, or a held note under a moving line) produce one
 * voice per layer. Every layer's parts sum to the measure length.
 */
export function measureLayers(measure: Measure, flip: Flip, axis = AXIS): MeasurePart[][] {
	const groups = noteGroups(measure.notes, measure.len, flip, axis);
	const layers: { end: number; parts: MeasurePart[] }[] = [];
	for (const group of groups) {
		let layer = layers.find((candidate) => candidate.end <= group.at);
		if (!layer) {
			layer = { end: 0, parts: [] };
			layers.push(layer);
		}
		layer.parts.push(group);
		layer.end = group.at + group.dur;
	}
	if (!layers.length) return [fillRests([], measure.len)];
	return layers.map((layer) => fillRests(layer.parts, measure.len));
}

/**
 * The parts of the measure's primary (melody) voice. Overlapping notes are
 * engraved by `measureLayers` instead; this helper is for rest synthesis and
 * the common single-voice case.
 */
export function measureParts(measure: Measure, flip: Flip, axis = AXIS): MeasurePart[] {
	return measureLayers(measure, flip, axis)[0];
}

const SHARP_NAMES = ['c', 'c#', 'd', 'd#', 'e', 'f', 'f#', 'g', 'g#', 'a', 'a#', 'b'];
const FLAT_NAMES = ['c', 'db', 'd', 'eb', 'e', 'f', 'gb', 'g', 'ab', 'a', 'bb', 'b'];

const FLAT_MAJOR_KEYS: Record<string, true> = { F: true, Bb: true, Eb: true, Ab: true, Db: true, Gb: true, Cb: true };

/** Keys with flat key signatures spell flats; every other key spells sharps. */
export function usesFlats(key: string): boolean {
	const match = /^([A-G][#b]?)m?$/.exec(key.trim());
	return match ? FLAT_MAJOR_KEYS[match[1]] === true : false;
}

/** VexFlow note key ("c#/4") for a MIDI pitch, spelled to match the key signature. */
export function pitchKey(pitch: number, key = 'C'): string {
	const names = usesFlats(key) ? FLAT_NAMES : SHARP_NAMES;
	const name = names[((pitch % 12) + 12) % 12];
	return `${name}/${Math.floor(pitch / 12) - 1}`;
}

/** Vertical reach of a stem plus its flag or beam, from a notehead centre. */
const STEM_REACH = 46;
/** Vertical half-extent of a notehead glyph. */
const HEAD_REACH = 8;
/** Blank margin above and below the engraved content. */
const VERTICAL_PAD = 8;
/** The card's stave box height; the canvas is never shorter than this. */
export const STAVE_BOX_HEIGHT = 104;
/** Middle staff line in VexFlow key-line coordinates (B4 in treble). */
const MIDDLE_LINE = 3;
/** Room at the right edge for the 1px closing barline, which VexFlow draws at the stave X end. */
const END_BARLINE_INSET = 2;

/** Card width grows with the bar length and note density but stays tappable. */
export function measureWidth(measure: Measure): number {
	const quarters = measure.len / PPQ;
	return Math.max(150, Math.round(quarters * 66 + Math.min(measure.notes.length, 16) * 5 + 70));
}

/** Voice time from the display signature, falling back to the bar length. */
function voiceBeats(timeSig: string | undefined, len: number): [number, number] {
	const match = timeSig ? /^(\d+)\/(\d+)$/.exec(timeSig) : null;
	if (match) return [Number(match[1]), Number(match[2])];
	return [Math.max(1, Math.round(len / PPQ)), 4];
}

export interface EngraveOptions {
	/** Key signature, e.g. "D" or "Am". Accidentals are applied against it. */
	key?: string;
	timeSig?: string;
	clef?: string;
	/** Inversion axis; the score fixes it at 71. */
	axis?: number;
	width?: number;
	height?: number;
	/** Print the time signature on this card (usually only the first one). */
	showTimeSignature?: boolean;
}

const cache = new Map<string, string>();

/** Cache key: one measure engraved at one flip state. */
export function cacheKey(measureID: string, flip: Flip): string {
	return `${measureID}:${flip.h}:${flip.v}`;
}

/** Drops every cached SVG (tests and score reloads). */
export function clearEngraveCache(): void {
	cache.clear();
}

/**
 * Renders one measure to an SVG string. Client only: VexFlow is imported on
 * demand and needs a DOM. The stave has no left barline so adjacent cards read
 * as one continuous system in a wrapping row.
 */
export async function engraveMeasure(measure: Measure, flip: Flip, opts: EngraveOptions = {}): Promise<string> {
	const keySig = opts.key ?? 'C';
	const clef = opts.clef ?? 'treble';
	const width = opts.width ?? measureWidth(measure);
	// Measure ids restart at m001 for every excerpt, so the id+flip key is
	// scoped by the bar's content and the display options as well.
	const content = measure.notes.map((n) => `${n.at}.${n.dur}.${n.pitch}`).join(',');
	const key = `${cacheKey(measure.id, flip)}|${keySig}|${opts.timeSig ?? ''}|${clef}|${width}|${opts.height ?? ''}|${measure.len}|${content}`;
	const hit = cache.get(key);
	if (hit !== undefined) return hit;

	if (typeof document === 'undefined') {
		throw new Error('music: engraveMeasure needs a browser DOM');
	}
	// Exception to the static-import rule: VexFlow is browser-only and must stay
	// out of the server bundle and off the initial route chunk.
	const vf = await import('vexflow');
	const { Renderer, Stave, StaveNote, Voice, Formatter, Beam, Accidental, Dot, Barline, Stem, VexFlow } = vf;

	// VexFlow registers its bundled Bravura/Academico faces asynchronously at
	// import time. Glyph metrics measured before they load are fallback widths,
	// which VexFlow caches and which shift stem anchors far right of the heads.
	// Wait for the music font before measuring or drawing anything.
	if (typeof document !== 'undefined' && 'fonts' in document) {
		await document.fonts.load('30pt Bravura');
		await document.fonts.ready;
	}

	const layers = measureLayers(measure, flip, opts.axis ?? AXIS);

	// The stave's y decides where the lines land, and the notes are positioned
	// around them. Work out the full vertical reach (staff, heads, stems, flags,
	// beams) up front, then anchor the stave so that reach is centred in a short
	// canvas the card can fit without clipping anything.
	// The closing barline is drawn at the stave's end X, one pixel wide, so the
	// stave stops short of the canvas edge to keep it inside the viewBox.
	const staveWidth = width - END_BARLINE_INSET;
	const probe = new Stave(0, 0, staveWidth);
	let top = probe.getYForLine(0) - 12;
	let bottom = probe.getYForLine(4) + 12;
	for (let layerIndex = 0; layerIndex < layers.length; layerIndex++) {
		for (const part of layers[layerIndex]) {
			const keys = part.pitches.length ? part.pitches.map((p) => pitchKey(p, keySig)) : ['b/4'];
			const lines = keys.map((keyName) => Number(VexFlow.keyProperties(keyName, clef).line));
			const lowest = Math.min(...lines);
			const highest = Math.max(...lines);
			// VexFlow's auto stem rule: below the middle line the stem points up.
			const stemUp = layers.length > 1 ? layerIndex === 0 : (lowest + highest) / 2 < MIDDLE_LINE;
			const ys = lines.map((line) => probe.getYForNote(line));
			top = Math.min(top, Math.min(...ys) - (stemUp ? STEM_REACH : HEAD_REACH));
			bottom = Math.max(bottom, Math.max(...ys) + (stemUp ? HEAD_REACH : STEM_REACH));
		}
	}
	const reach = bottom - top;
	const height = Math.max(opts.height ?? STAVE_BOX_HEIGHT, Math.ceil(reach + VERTICAL_PAD * 2));
	const staveY = Math.round((height - reach) / 2 - top);

	const host = document.createElement('div');
	const renderer = new Renderer(host, Renderer.Backends.SVG);
	renderer.resize(width, height);
	const ctx = renderer.getContext();

	const stave = new Stave(0, staveY, staveWidth);
	stave.setBegBarType(Barline.type.NONE);
	stave.addClef(clef);
	stave.addKeySignature(keySig);
	if (opts.showTimeSignature && opts.timeSig) stave.addTimeSignature(opts.timeSig);
	stave.setContext(ctx).draw();

	const voices: InstanceType<typeof Voice>[] = [];
	const beams: InstanceType<typeof Beam>[] = [];
	layers.forEach((parts, layerIndex) => {
		const notes: InstanceType<typeof StaveNote>[] = [];
		const dotted: InstanceType<typeof StaveNote>[] = [];
		for (const part of parts) {
			const duration = vexDuration(part.dur) + (part.pitches.length === 0 ? 'r' : '');
			const keys = part.pitches.length ? part.pitches.map((p) => pitchKey(p, keySig)) : ['b/4'];
			const note = new StaveNote({ keys, duration, clef, autoStem: layers.length === 1 });
			if (layers.length > 1) note.setStemDirection(layerIndex === 0 ? Stem.UP : Stem.DOWN);
			if (isDotted(part.dur)) dotted.push(note);
			notes.push(note);
		}
		Dot.buildAndAttach(dotted, { all: true });

		const [numBeats, beatValue] = voiceBeats(opts.timeSig, measure.len);
		const voice = new Voice({ numBeats, beatValue }).setStrict(false);
		voice.addTickables(notes);
		Accidental.applyAccidentals([voice], keySig);
		voices.push(voice);
		beams.push(...Beam.generateBeams(notes));
	});

	const noteStart = stave.getNoteStartX();
	const formatWidth = Math.max(40, staveWidth - noteStart - 12);
	new Formatter().joinVoices(voices).format(voices, formatWidth);
	for (const voice of voices) voice.draw(ctx, stave);
	for (const beam of beams) beam.setContext(ctx).draw();

	const svg = host.querySelector('svg');
	if (!svg) {
		host.remove();
		throw new Error('music: VexFlow produced no SVG');
	}
	// Fit the actual ink so beams and ledger lines stay inside the responsive card
	// without carrying over the conservative height of the engraving canvas.
	if (typeof svg.getBBox === 'function') {
		host.style.cssText = 'position:fixed;visibility:hidden;pointer-events:none';
		document.body.append(host);
		try {
			const ink = svg.getBBox();
			const left = Math.min(0, Math.floor(ink.x) - 2);
			const right = Math.max(width, Math.ceil(ink.x + ink.width) + 2);
			const fittedHeight = Math.max(opts.height ?? 88, Math.ceil(ink.height) + 2 * VERTICAL_PAD);
			const top = Math.floor(ink.y + (ink.height - fittedHeight) / 2);
			svg.setAttribute('viewBox', `${left} ${top} ${right - left} ${fittedHeight}`);
			svg.setAttribute('width', String(right - left));
			svg.setAttribute('height', String(fittedHeight));
		} finally {
			host.remove();
		}
	}
	svg.removeAttribute('style');
	const out = svg.outerHTML;
	cache.set(key, out);
	return out;
}
