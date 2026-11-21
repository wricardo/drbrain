/** Site brand: the Dr Brain collection of reimplemented games. */
export const SITE = 'Dr Brain';
/** This game within the collection. */
export const GAME = 'Delivery';
/** Full product name used in titles, prompts and docs. */
export const TITLE = `${GAME} · drbrain`;

/** Document title for a page: "Delivery · drbrain" or "<page> · Delivery · drbrain". */
export function pageTitle(page?: string): string {
	return page ? `${page} · ${TITLE}` : TITLE;
}
