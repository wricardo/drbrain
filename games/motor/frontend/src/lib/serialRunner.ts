/**
 * Wraps an async task so overlapping calls run one after another, never together.
 * Used for saving the program: a debounced autosave and a Run that saves first must
 * not send two different programs at once, or the server could keep the older one.
 *
 * Every call runs the task (the task decides whether there is anything to do) and
 * resolves or rejects with that run's own outcome; one failure never blocks later calls.
 */
export function serialRunner(task: () => Promise<void>): () => Promise<void> {
	let tail: Promise<void> = Promise.resolve();
	return () => {
		const run = tail.then(task);
		tail = run.catch(() => {});
		return run;
	};
}
