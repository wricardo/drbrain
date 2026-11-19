import { describe, expect, it, vi } from 'vitest';
import { nameSyncMessage, renameOwnSessions } from './nameSync';

const deps = (over: Record<string, unknown> = {}) => ({
	ids: ['a', 'b', 'c'],
	current: async () => [
		{ id: 'a', displayName: 'Old' },
		{ id: 'b', displayName: 'Sam' },
		{ id: 'z', displayName: 'Someone else' }
	],
	rename: vi.fn(async () => ({})),
	...over
});

describe('renameOwnSessions', () => {
	it('renames the browser’s sessions, skipping ones already named right', async () => {
		const d = deps();
		const r = await renameOwnSessions('  Sam ', d);
		expect(r).toEqual({ renamed: 2, failed: 0 });
		expect(d.rename).toHaveBeenCalledTimes(2);
		expect(d.rename).toHaveBeenCalledWith('a', 'Sam');
		expect(d.rename).toHaveBeenCalledWith('c', 'Sam'); // not in the listing: still tried
		expect(d.rename).not.toHaveBeenCalledWith('b', 'Sam');
		expect(d.rename).not.toHaveBeenCalledWith('z', 'Sam'); // never touches other players' sessions
	});

	it('does nothing for an empty name or no sessions', async () => {
		const d = deps();
		expect(await renameOwnSessions('   ', d)).toEqual({ renamed: 0, failed: 0 });
		expect(await renameOwnSessions('Sam', deps({ ids: [] }))).toEqual({ renamed: 0, failed: 0 });
		expect(d.rename).not.toHaveBeenCalled();
	});

	it('keeps going when one rename fails, and when the listing fails', async () => {
		const rename = vi.fn(async (id: string) => {
			if (id === 'a') throw new Error('gone');
		});
		const r = await renameOwnSessions('Sam', deps({ rename, current: async () => Promise.reject(new Error('offline')) }));
		expect(r).toEqual({ renamed: 2, failed: 1 });
		expect(rename).toHaveBeenCalledTimes(3);
	});

	it('limits how many renames run at once', async () => {
		let active = 0;
		let peak = 0;
		const rename = async () => {
			peak = Math.max(peak, ++active);
			await new Promise((res) => setTimeout(res, 5));
			active--;
		};
		const ids = Array.from({ length: 10 }, (_, i) => `s${i}`);
		await renameOwnSessions('Sam', { ids, current: async () => [], rename, concurrency: 3 });
		expect(peak).toBe(3);
	});
});

describe('nameSyncMessage', () => {
	it('says what changed, and stays quiet when nothing did', () => {
		expect(nameSyncMessage('Sam', { renamed: 0, failed: 0 })).toBe('');
		expect(nameSyncMessage(' Sam ', { renamed: 1, failed: 0 })).toContain('Updated 1 earlier session to Sam');
		expect(nameSyncMessage('Sam', { renamed: 3, failed: 1 })).toMatch(/3 earlier sessions.*1 could not be renamed/);
	});
});
