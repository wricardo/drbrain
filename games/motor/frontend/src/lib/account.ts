import { isOfficial } from './maps';
import { buildRunRows, type RunRow } from './runs';
import type { Account, GameMap, PlayerRun, SessionClaimResult } from './types';

/** Handles are 3-20 characters: lower-case letters, digits and underscores (the server is the authority). */
export const HANDLE_RE = /^[a-z0-9_]{3,20}$/;
export const MAX_HANDLE = 20;
export const MAX_DISPLAY_NAME = 40;

/** Lower-cases and trims a handle as typed ("@Speedy_Fox " becomes "speedy_fox"). */
export function normalizeHandle(raw: string): string {
	return raw.trim().replace(/^@/, '').toLowerCase();
}

/** A short reason why a typed handle cannot work, or null. Reserved and blocked words are only known to the server. */
export function handleProblem(raw: string): string | null {
	const h = normalizeHandle(raw);
	if (h.length < 3) return 'Use at least 3 characters.';
	if (h.length > MAX_HANDLE) return `Use at most ${MAX_HANDLE} characters.`;
	if (!HANDLE_RE.test(h)) return 'Use only lower-case letters, digits and underscores.';
	return null;
}

/** Same-site relative path for returning after sign-in; anything else becomes "/". */
export function safeNext(path: string | null | undefined): string {
	const p = path ?? '';
	if (!p.startsWith('/') || p.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(p) || p.length > 512) return '/';
	if (p === '/auth' || p.startsWith('/auth/') || p.startsWith('/auth?')) return '/';
	return p;
}

/** Where a provider's button leads. A plain navigation: the server redirects to the provider and back to `next`. */
export function signInHref(providerId: string, next: string): string {
	return `/auth/${encodeURIComponent(providerId)}/login?next=${encodeURIComponent(safeNext(next))}`;
}

/** The public profile page of a handle. */
export function profileHref(handle: string): string {
	return `/u/${encodeURIComponent(handle)}`;
}

export interface SigninNotice {
	kind: 'error' | 'info';
	message: string;
}

/** The message for the `?signin=` reason the server adds when a sign-in did not complete; null for anything else. */
export function signinNotice(value: string | null | undefined): SigninNotice | null {
	switch (value) {
		case 'cancelled':
			return { kind: 'info', message: 'Sign-in was cancelled. You can keep playing as a guest.' };
		case 'failed':
			return { kind: 'error', message: 'Sign-in did not work. Please try again; you can keep playing as a guest meanwhile.' };
		default:
			return null;
	}
}

/** The URL without the `signin` parameter (so a reload does not repeat the notice). */
export function withoutSigninParam(pathname: string, search: string): string {
	const q = new URLSearchParams(search);
	q.delete('signin');
	const rest = q.toString();
	return rest ? `${pathname}?${rest}` : pathname;
}

/** When the handle can be changed again, as a sentence; null when it can be changed now. */
export function handleChangeText(account: Pick<Account, 'handleChangeableAt'>, now: Date = new Date()): string | null {
	if (!account.handleChangeableAt) return null;
	const at = new Date(account.handleChangeableAt);
	if (Number.isNaN(at.getTime()) || at.getTime() <= now.getTime()) return null;
	const hours = Math.ceil((at.getTime() - now.getTime()) / 3_600_000);
	return hours <= 1 ? 'You can change your handle again in about an hour.' : `You can change your handle again in about ${hours} hours.`;
}

export interface Tally {
	solved: number;
	total: number;
}

export interface ProfileSummary {
	official: Tally;
	community: Tally;
	/** The best run per solved map, in the standard map order. */
	rows: (RunRow & { run: PlayerRun })[];
	/** The latest wins, newest first. */
	recent: PlayerRun[];
}

/** Everything the profile page shows besides the header. Runs for maps that no longer exist are ignored. */
export function summarizeProfile(maps: GameMap[], runs: PlayerRun[], recentCount = 5): ProfileSummary {
	const all = buildRunRows(maps, runs);
	const tally = (official: boolean): Tally => {
		const list = all.filter((r) => isOfficial(r.map) === official);
		return { solved: list.filter((r) => r.run).length, total: list.length };
	};
	const rows = all.filter((r): r is RunRow & { run: PlayerRun } => r.run !== null);
	const known = new Set(rows.map((r) => r.map.id));
	const recent = runs
		.filter((r) => known.has(r.mapId))
		.sort((a, b) => new Date(b.entry.achievedAt).getTime() - new Date(a.entry.achievedAt).getTime())
		.slice(0, recentCount);
	return { official: tally(true), community: tally(false), rows, recent };
}

/** "Solved 5 of 11 official maps". */
export function tallyText(t: Tally, label: string): string {
	return `${t.solved} of ${t.total} ${label}`;
}

/** What the claim banner says for n guest runs. */
export function claimOffer(n: number): string {
	return n === 1
		? 'You have 1 guest run in this browser. Add it to your profile?'
		: `You have ${n} guest runs in this browser. Add them to your profile?`;
}

export interface ClaimOutcome {
	attached: number;
	alreadyYours: number;
	rejected: number;
	/** Every session the server answered for: their tokens are of no further use. */
	handled: string[];
}

/** Counts the results of claimSessions. */
export function summarizeClaims(results: SessionClaimResult[]): ClaimOutcome {
	const out: ClaimOutcome = { attached: 0, alreadyYours: 0, rejected: 0, handled: results.map((r) => r.sessionID) };
	for (const r of results) {
		if (r.status === 'ATTACHED') out.attached++;
		else if (r.status === 'ALREADY_YOURS') out.alreadyYours++;
		else out.rejected++;
	}
	return out;
}

/** "Added 3 runs to your profile." and friends. */
export function claimResultText(o: ClaimOutcome): string {
	const parts: string[] = [];
	const added = o.attached + o.alreadyYours;
	if (added > 0) parts.push(`Added ${added} ${added === 1 ? 'run' : 'runs'} to your profile.`);
	if (o.rejected > 0) parts.push(`${o.rejected} could not be added (already on another account, or not from this browser).`);
	return parts.join(' ') || 'Nothing to add.';
}
