/**
 * Changing your name on the home page should rename the player on the boards, not start a second one:
 * a leaderboard entry carries the name of the session that won it, so earlier sessions keep the old name
 * unless they are renamed too. This renames every session this browser started.
 */
export interface NameSyncResult {
	renamed: number;
	failed: number;
}

export interface NameSyncDeps {
	/** Ids of the sessions this browser started. */
	ids: string[];
	/** Sessions the server currently lists with their names (may be partial); used to skip ones already right. */
	current: () => Promise<{ id: string; displayName: string }[]>;
	rename: (id: string, name: string) => Promise<unknown>;
	/** Requests in flight at once (default 4). */
	concurrency?: number;
}

/**
 * Renames this browser's sessions to `name`. An empty name changes nothing (it means "a random guest name",
 * which the server picks per new session). A session that cannot be renamed (deleted, or owned by an
 * account) is counted in `failed` and does not stop the others.
 */
export async function renameOwnSessions(name: string, deps: NameSyncDeps): Promise<NameSyncResult> {
	const wanted = name.trim();
	if (!wanted || deps.ids.length === 0) return { renamed: 0, failed: 0 };

	let listed: { id: string; displayName: string }[] = [];
	try {
		listed = await deps.current();
	} catch {
		// Without the list, just try every session.
	}
	const names = new Map(listed.map((s) => [s.id, s.displayName]));
	const todo = deps.ids.filter((id) => names.get(id) !== wanted);

	const result: NameSyncResult = { renamed: 0, failed: 0 };
	let next = 0;
	const worker = async () => {
		while (next < todo.length) {
			const id = todo[next++];
			try {
				await deps.rename(id, wanted);
				result.renamed++;
			} catch {
				result.failed++;
			}
		}
	};
	await Promise.all(Array.from({ length: Math.min(deps.concurrency ?? 4, todo.length) }, worker));
	return result;
}

/** One line for the home page after a rename, or '' when nothing changed. */
export function nameSyncMessage(name: string, r: NameSyncResult): string {
	if (r.renamed === 0) return '';
	const runs = r.renamed === 1 ? '1 earlier session' : `${r.renamed} earlier sessions`;
	return `Updated ${runs} to ${name.trim()}, so the leaderboard shows the new name.${r.failed ? ` ${r.failed} could not be renamed.` : ''}`;
}
