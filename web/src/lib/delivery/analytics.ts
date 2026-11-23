import type { SessionSummary } from './api';

/** The label used in the analytics picker. */
export function sessionLabel(
	s: Pick<SessionSummary, 'id' | 'ownerDisplayName' | 'mine'>
): string {
	if (s.mine) return 'Your city';
	if (s.ownerDisplayName && s.ownerDisplayName !== 'Guest') {
		return `${s.ownerDisplayName}’s city`;
	}
	return `City …${s.id.slice(-6)}`;
}

/**
 * Pick a useful starting overlay: sessions that have progressed, capped at
 * limit. Fall back to the newest saves when every session is still at tick 0.
 */
export function defaultSelection(saves: SessionSummary[], limit = 5): string[] {
	const progressed = saves.filter((s) => s.tick > 0 || s.money > 0);
	const pool = progressed.length > 0 ? progressed : saves;
	return pool.slice(0, limit).map((s) => s.id);
}
