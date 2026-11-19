/**
 * Replaces the browser's default drag preview (a faded screenshot of the whole
 * button) with a compact instruction tile, so the player sees exactly what they hold.
 * Silently does nothing where setDragImage is unavailable (older browsers, jsdom).
 */
export function setTileDragImage(e: DragEvent, glyph: string, classes: string): void {
	const dt = e.dataTransfer;
	if (!dt || typeof dt.setDragImage !== 'function' || typeof document === 'undefined') return;
	const tile = document.createElement('div');
	tile.className = `${classes} flex h-12 w-12 items-center justify-center rounded-md border-2 text-base font-bold shadow-lg`;
	tile.textContent = glyph;
	// Must be in the DOM and rendered for the browser to snapshot it; park it off-screen.
	tile.style.cssText = 'position:fixed;top:-100px;left:-100px;pointer-events:none;';
	document.body.appendChild(tile);
	dt.setDragImage(tile, 24, 24);
	setTimeout(() => tile.remove(), 0);
}
