import { describe, expect, it, vi } from 'vitest';
import { midiToFrequency, secondsPerTick, SynthPlayer } from './audio';
import type { NoteEvent } from './types';

interface ParamCall {
	value: number;
	time: number;
}

class FakeParam {
	value = 0;
	calls: ParamCall[] = [];
	setValueAtTime(value: number, time: number) {
		this.value = value;
		this.calls.push({ value, time });
	}
	linearRampToValueAtTime(value: number, time: number) {
		this.calls.push({ value, time });
	}
	exponentialRampToValueAtTime(value: number, time: number) {
		this.calls.push({ value, time });
	}
}

class FakeOscillator {
	type = 'sine';
	frequency = new FakeParam();
	detune = new FakeParam();
	starts: number[] = [];
	stops: number[] = [];
	start(time: number) {
		this.starts.push(time);
	}
	stop(time: number) {
		this.stops.push(time);
	}
	connect() {}
	disconnect() {}
}

class FakeNode {
	connect() {}
	disconnect() {}
}

class FakeGain extends FakeNode {
	gain = new FakeParam();
}

class FakeFilter extends FakeNode {
	type = '';
	frequency = new FakeParam();
}

class FakeContext {
	currentTime = 10;
	state: AudioContextState = 'running';
	destination = {};
	oscillators: FakeOscillator[] = [];
	gains: FakeGain[] = [];
	filters: FakeFilter[] = [];
	resumed = 0;
	createOscillator() {
		const osc = new FakeOscillator();
		this.oscillators.push(osc);
		return osc;
	}
	createGain() {
		const gain = new FakeGain();
		this.gains.push(gain);
		return gain;
	}
	createBiquadFilter() {
		const filter = new FakeFilter();
		this.filters.push(filter);
		return filter;
	}
	resume() {
		this.resumed++;
		return Promise.resolve();
	}
}

function setup() {
	const context = new FakeContext();
	// The player only uses the narrow WebAudio surface above; the cast is the test boundary.
	const audio = context as unknown as AudioContext;
	return { context, player: new SynthPlayer(audio) };
}

const EVENTS: NoteEvent[] = [
	{ at: 0, dur: 480, pitch: 69 },
	{ at: 480, dur: 480, pitch: 72 }
];

describe('SynthPlayer scheduling', () => {
	it('starts 50ms ahead and schedules ticks at the tempo', () => {
		const { context, player } = setup();
		player.play(EVENTS, 120);

		// 120 bpm => 0.5 s per quarter => 1/960 s per tick.
		expect(secondsPerTick(120)).toBeCloseTo(1 / 960);
		// Two detuned oscillators per note.
		expect(context.oscillators).toHaveLength(4);
		const starts = context.oscillators.map((o) => o.starts[0]).sort((a, b) => a - b);
		expect(starts[0]).toBeCloseTo(10.05);
		expect(starts[1]).toBeCloseTo(10.05);
		expect(starts[2]).toBeCloseTo(10.55);
		expect(starts[3]).toBeCloseTo(10.55);

		const stops = context.oscillators.map((o) => o.stops[0]);
		expect(Math.min(...stops)).toBeCloseTo(10.57);
		expect(Math.max(...stops)).toBeCloseTo(11.07);

		// The two oscillators are detuned around the same frequency.
		expect(context.oscillators[0].frequency.value).toBeCloseTo(midiToFrequency(69));
		expect(context.oscillators[0].detune.value).toBe(-6);
		expect(context.oscillators[1].detune.value).toBe(6);
		expect(context.oscillators[0].type).toBe('sawtooth');

		// Lowpass around 4 kHz on every note.
		expect(context.filters).toHaveLength(2);
		expect(context.filters[0].frequency.value).toBe(4000);
	});

	it('stop() cancels every scheduled node', () => {
		const { context, player } = setup();
		player.play(EVENTS, 120);
		expect(player.playing).toBe(true);

		player.stop();
		expect(player.playing).toBe(false);
		for (const osc of context.oscillators) {
			expect(osc.stops).toContain(10);
			const cancellations = osc.stops.filter((time) => time === 10);
			expect(cancellations).toHaveLength(1);
		}
		// Idempotent: a second stop has nothing left to cancel.
		player.stop();
		expect(context.oscillators.every((osc) => osc.stops.filter((t) => t === 10).length === 1)).toBe(true);
	});

	it('a new play cancels the previous one', () => {
		const { context, player } = setup();
		player.play(EVENTS, 120);
		const first = [...context.oscillators];
		player.play([{ at: 0, dur: 240, pitch: 60 }], 60);

		expect(first.every((osc) => osc.stops.includes(10))).toBe(true);
		expect(context.oscillators).toHaveLength(6);
		expect(player.playing).toBe(true);
		player.stop();
	});

	it('dispose detaches the master gain', () => {
		const { context, player } = setup();
		const disconnect = vi.spyOn(context.gains[0], 'disconnect');
		player.dispose();
		expect(disconnect).toHaveBeenCalledOnce();
	});
});
