import { describe, expect, it } from 'vitest';
import { blockedCallNotice, recursionChip } from './recursion';

describe('recursionChip', () => {
	it('gives an explicit chip for each state', () => {
		const on = recursionChip({ allowRecursion: true, maxCallDepth: 16 });
		expect(on.label).toBe('recursion');
		expect(on.title).toContain('16');

		const off = recursionChip({ allowRecursion: false, maxCallDepth: 16 });
		expect(off.label).toBe('no recursion');
		expect(off.title).toContain('still spent');
		expect(off.classes).not.toBe(on.classes);
	});
});

describe('blockedCallNotice', () => {
	it('names the sub only for an ignored call', () => {
		expect(blockedCallNotice(null)).toBeNull();
		expect(blockedCallNotice({ callBlocked: false, instruction: 'CALL_SUB_1' })).toBeNull();
		const notice = blockedCallNotice({ callBlocked: true, instruction: 'CALL_SUB_2' });
		expect(notice).toContain('Sub 2');
		expect(notice).toContain('ignored');
		expect(notice).toContain('still spent');
	});
});
