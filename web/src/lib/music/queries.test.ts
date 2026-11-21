import { describe, expect, it } from 'vitest';
import { unwrapMusic } from './graphql';
import { EXCERPTS_QUERY, SESSION_QUERY, SESSION_UPDATED_SUBSCRIPTION, TRANSFORM_MEASURE_MUTATION } from './queries';

describe('music documents', () => {
	it('select under the music namespace', () => {
		expect(EXCERPTS_QUERY).toContain('music { excerpts');
		expect(SESSION_QUERY).toContain('music { session');
		expect(TRANSFORM_MEASURE_MUTATION).toContain('music { transformMeasure');
		expect(SESSION_UPDATED_SUBSCRIPTION).toContain('musicSessionUpdated(sessionID: $sessionID)');
	});

	it('request the player model fields', () => {
		expect(EXCERPTS_QUERY).toContain('measures');
		expect(EXCERPTS_QUERY).toContain('notes {');
		expect(SESSION_QUERY).toContain('slots {');
		expect(SESSION_QUERY).toContain('percentPerWin');
	});
});

describe('unwrapMusic', () => {
	it('unwraps a namespaced query result to the game-local shape', () => {
		const excerpts = [{ id: 'minuet-in-g', title: 'Minuet in G' }];
		expect(unwrapMusic({ music: { excerpts } })).toEqual({ excerpts });
	});

	it('maps the flat subscription back to sessionUpdated', () => {
		const update = { session: { id: 's1', seq: 2 }, lastChange: { kind: 'switch', a: 0, b: 1 } };
		expect(unwrapMusic({ musicSessionUpdated: update })).toEqual({ sessionUpdated: update });
	});

	it('leaves unrelated payloads alone', () => {
		const data = { account: { viewer: null } };
		expect(unwrapMusic(data)).toBe(data);
	});
});
