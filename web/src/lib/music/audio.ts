// Harpsichord-ish WebAudio synthesis for Music Region.
//
// Events are absolute ticks (PPQ 480). Everything is scheduled against the
// AudioContext clock, so no timers exist to leak: the only live handles are the
// oscillators a stop() cancels. A generation counter makes a new play cancel
// the previous one even if its scheduling loop is still running.
import { PPQ } from './engrave';
import type { NoteEvent } from './types';

/** Equal-tempered frequency for a MIDI pitch. */
export function midiToFrequency(pitch: number): number {
	return 440 * 2 ** ((pitch - 69) / 12);
}

/** Seconds per tick at a tempo. */
export function secondsPerTick(bpm: number): number {
	return 60 / Math.max(1, bpm) / PPQ;
}

interface Voice {
	osc: OscillatorNode;
	gain: GainNode;
	filter: BiquadFilterNode;
}

/**
 * Schedules note events on one AudioContext. Two slightly detuned sawtooth
 * oscillators per note run through a lowpass and a short attack/exponential
 * decay, all into a master gain.
 */
export class SynthPlayer {
	readonly context: AudioContext;
	private readonly master: GainNode;
	private voices: Voice[] = [];
	private generation = 0;

	constructor(context: AudioContext) {
		this.context = context;
		this.master = context.createGain();
		this.master.gain.value = 0.2;
		this.master.connect(context.destination);
	}

	/** True while notes from the current play are still scheduled. */
	get playing(): boolean {
		return this.voices.length > 0;
	}

	/**
	 * Cancels the previous play, then schedules every event. `events` are on an
	 * absolute tick timeline starting at zero; playback begins 50 ms from now.
	 */
	play(events: NoteEvent[], bpm: number): void {
		this.stop();
		const generation = this.generation;
		const ctx = this.context;
		if (ctx.state === 'suspended') void ctx.resume();
		const t0 = ctx.currentTime + 0.05;
		const tick = secondsPerTick(bpm);

		for (const event of events) {
			if (generation !== this.generation) return;
			const start = t0 + event.at * tick;
			const duration = Math.max(0.08, event.dur * tick);
			const frequency = midiToFrequency(event.pitch);
			const filter = ctx.createBiquadFilter();
			filter.type = 'lowpass';
			filter.frequency.setValueAtTime(4000, start);
			const gain = ctx.createGain();
			gain.gain.setValueAtTime(0, start);
			gain.gain.linearRampToValueAtTime(0.22, start + 0.005);
			gain.gain.exponentialRampToValueAtTime(0.0001, start + duration);
			filter.connect(gain);
			gain.connect(this.master);
			for (const detune of [-6, 6]) {
				const osc = ctx.createOscillator();
				osc.type = 'sawtooth';
				osc.frequency.setValueAtTime(frequency, start);
				osc.detune.setValueAtTime(detune, start);
				osc.connect(filter);
				osc.start(start);
				osc.stop(start + duration + 0.02);
				this.voices.push({ osc, gain, filter });
			}
		}
	}

	/** Cancels every scheduled node. Safe to call repeatedly. */
	stop(): void {
		this.generation++;
		const now = this.context.currentTime;
		for (const voice of this.voices) {
			try {
				voice.osc.stop(now);
			} catch {
				// Already stopped by its scheduled end.
			}
			try {
				voice.osc.disconnect();
				voice.filter.disconnect();
				voice.gain.disconnect();
			} catch {
				// Disconnecting a node that already ended is harmless.
			}
		}
		this.voices = [];
	}

	/** Stops playback and detaches the master gain. */
	dispose(): void {
		this.stop();
		try {
			this.master.disconnect();
		} catch {
			// The context may already be closed.
		}
	}
}

let shared: SynthPlayer | null = null;

/** AudioContext constructor, including Safari's legacy prefix. */
function audioContextConstructor(): typeof AudioContext | null {
	if (typeof window === 'undefined') return null;
	if (window.AudioContext) return window.AudioContext;
	// The prefixed property is absent from lib.dom; the cast keeps the read narrow.
	const legacy: Window & { webkitAudioContext?: typeof AudioContext } = window;
	return legacy.webkitAudioContext ?? null;
}

/**
 * The page's player, created on the first user gesture (browsers refuse to
 * start an AudioContext before one). Resumes a suspended context.
 */
export function getSynthPlayer(): SynthPlayer {
	if (!shared) {
		const Ctor = audioContextConstructor();
		if (!Ctor) throw new Error('music: this browser has no WebAudio support');
		shared = new SynthPlayer(new Ctor());
	}
	if (shared.context.state === 'suspended') void shared.context.resume();
	return shared;
}

/** Stops and drops the shared player (page teardown). */
export function disposeSynthPlayer(): void {
	shared?.dispose();
	shared = null;
}
