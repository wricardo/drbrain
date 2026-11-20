// Motor-specific profile summaries: solved maps, tallies and guest-run claims.
import { isOfficial } from './maps';
import { buildRunRows, type RunRow } from './runs';
import type { GameMap, PlayerRun, SessionClaimResult } from './types';

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
