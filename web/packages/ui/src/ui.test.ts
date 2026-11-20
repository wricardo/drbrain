import { describe, expect, it } from 'vitest';
import { absoluteTime, relativeTime } from './time';
import { describeError } from './errors';
import { graphqlUrls } from './graphql';

describe('shared ui core', () => {
	it('formats relative and absolute time', () => {
		const now = new Date('2026-01-02T12:00:00Z');
		expect(relativeTime('2026-01-02T11:59:30Z', now)).toBe('just now');
		expect(relativeTime('2026-01-01T12:00:00Z', now)).toBe('1d ago');
		expect(absoluteTime('not a date')).toBe('not a date');
	});

	it('describes GraphQL errors', () => {
		expect(describeError([{ message: 'gone', extensions: { code: 'NOT_FOUND' } }])).toMatchObject({
			code: 'NOT_FOUND',
			title: 'Not found'
		});
	});

	it('uses the one GraphQL endpoint for every game', () => {
		expect(graphqlUrls('/motor').http).toBe('/graphql');
		expect(graphqlUrls('/motor').ws).toContain('/graphql');
	});
});
