import { callTarget, tapeLabel } from './instructions';
import type { GameMap, StepEvent } from './types';

/**
 * Map-card chip. Both states are labelled: "no recursion" is the rule on most
 * maps, and leaving it unmarked made players discover it by wasting steps.
 */
export function recursionChip(map: Pick<GameMap, 'allowRecursion' | 'maxCallDepth'>): {
	label: string;
	title: string;
	classes: string;
} {
	if (map.allowRecursion) {
		return {
			label: 'recursion',
			title: `Recursion allowed: a sub may call itself. Call depth is capped at ${map.maxCallDepth}.`,
			classes: 'bg-violet-100 text-violet-800'
		};
	}
	return {
		label: 'no recursion',
		title:
			'A call to the sub that is running, or to any sub already on the call stack, does nothing — and the step is still spent.',
		classes: 'bg-slate-200 text-slate-700'
	};
}

/**
 * Explains the most recent step when recursion rules turned a CALL into a
 * no-op, so a spent step never looks like a bug. Null for every other step.
 */
export function blockedCallNotice(
	lastEvent: Pick<StepEvent, 'callBlocked' | 'instruction'> | null
): string | null {
	if (!lastEvent?.callBlocked) return null;
	const k = callTarget(lastEvent.instruction);
	const sub = k === null ? 'That sub' : tapeLabel(k);
	return `${sub} is already on the call stack and recursion is off on this map, so the call was ignored — the step was still spent.`;
}
