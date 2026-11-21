#!/usr/bin/env node
// Headless smoke test for the DrBrain Delivery UI.
//
// Drives real Chrome over the DevTools protocol (no Playwright, no npm deps) to
// prove the page renders data rather than merely returning HTML. It:
//   1. starts a session through the API so the UI has something to show
//   2. loads the page and waits for the map grid to appear
//   3. reads rendered DOM text back and asserts real content is on screen
//   4. collects console errors and failed requests
//   5. saves a screenshot for human review
//
//   6. phase 2: drops this browser's owner token and proves the same session
//      opens read-only (spectator notice shown, no speed controls)
//
// Usage: node scripts/ui-smoke.mjs [baseUrl] [ownerPng] [spectatorPng]

import { spawn } from 'node:child_process';
import { writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const BASE = process.argv[2] ?? 'http://localhost:8081';
const OUT = process.argv[3] ?? '/tmp/ui.png';
const SPECTATOR_OUT = process.argv[4] ?? OUT.replace(/\.png$/, '') + '-spectator.png';
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

const VIEW = { width: 1440, height: 950 };

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// GraphQL is the only transport. Every Delivery operation lives in the `delivery`
// namespace of the unified /graphql schema. These helpers post straight to it.
async function gql(query, variables) {
	const res = await fetch(BASE + '/graphql', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ query, variables })
	});
	const body = await res.json();
	if (body.errors?.length) throw new Error(`gql: ${body.errors[0].message}`);
	if (!res.ok) throw new Error(`gql -> ${res.status}`);
	return body.data;
}

/**
 * Connects straight to a page target's own WebSocket URL, so every message is
 * flat on the socket and no session multiplexing is needed.
 */
class Page {
	#ws;
	#id = 0;
	#pending = new Map();
	errors = [];
	failedRequests = [];

	constructor(ws) {
		this.#ws = ws;
		ws.addEventListener('message', (ev) => {
			const msg = JSON.parse(ev.data);
			if (msg.id !== undefined) {
				const p = this.#pending.get(msg.id);
				if (!p) return;
				this.#pending.delete(msg.id);
				msg.error ? p.reject(new Error(JSON.stringify(msg.error))) : p.resolve(msg.result);
				return;
			}
			this.#onEvent(msg);
		});
	}

	#onEvent(msg) {
		const { method, params } = msg;
		if (method === 'Runtime.exceptionThrown') {
			this.errors.push(params.exceptionDetails?.text ?? 'exception');
		}
		if (method === 'Runtime.consoleAPICalled' && params.type === 'error') {
			this.errors.push(params.args.map((a) => a.value ?? a.description ?? '?').join(' '));
		}
		if (method === 'Network.loadingFailed') {
			this.failedRequests.push(params.errorText ?? 'failed');
		}
		if (method === 'Network.responseReceived' && params.response.status >= 400) {
			this.failedRequests.push(`${params.response.status} ${params.response.url}`);
		}
	}

	static async connect(url) {
		const ws = new WebSocket(url);
		await new Promise((resolve, reject) => {
			ws.addEventListener('open', resolve, { once: true });
			ws.addEventListener('error', () => reject(new Error('ws connect failed')), { once: true });
		});
		return new Page(ws);
	}

	send(method, params = {}) {
		const id = ++this.#id;
		this.#ws.send(JSON.stringify({ id, method, params }));
		return new Promise((resolve, reject) => this.#pending.set(id, { resolve, reject }));
	}

	async eval(expression) {
		const res = await this.send('Runtime.evaluate', {
			expression,
			returnByValue: true,
			awaitPromise: true
		});
		if (res.exceptionDetails) {
			throw new Error(`eval failed: ${res.exceptionDetails.text}`);
		}
		return res.result.value;
	}

	/** Polls an expression until it is truthy. */
	async waitFor(expression, timeoutMs = 8000) {
		const until = Date.now() + timeoutMs;
		while (Date.now() < until) {
			if (await this.eval(`(() => { try { return !!(${expression}); } catch { return false; } })()`)) {
				return true;
			}
			await sleep(100);
		}
		return false;
	}

	close() {
		this.#ws.close();
	}
}

// --- start a session so the UI has data ------------------------------------

const sessionId = `smoke${Date.now()}`;
const created = await gql(
	`mutation ($id: ID, $seed: Int) { delivery { createSession(id: $id, seed: $seed) { session { id } ownerToken } } }`,
	{ id: sessionId, seed: 12345 }
);
const ownerToken = created.delivery.createSession.ownerToken;
if (!ownerToken) throw new Error('createSession returned no ownerToken');
// Pause so the screenshot is deterministic, then step so orders and movement
// exist. (New sessions already start paused; explicit for clarity.)
await gql(
	`mutation ($id: ID!, $ownerToken: String, $speed: Int!) { delivery { setSpeed(id: $id, ownerToken: $ownerToken, speed: $speed) { tick } } }`,
	{ id: sessionId, ownerToken, speed: 0 }
);
await gql(
	`mutation ($id: ID!, $ownerToken: String, $count: Int!) { delivery { tick(id: $id, ownerToken: $ownerToken, count: $count) { tick } } }`,
	{ id: sessionId, ownerToken, count: 6 }
);

// --- launch chrome ---------------------------------------------------------

const userDataDir = mkdtempSync(join(tmpdir(), 'drbrain-chrome-'));
const chrome = spawn(CHROME, [
	'--headless=new',
	'--disable-gpu',
	'--no-first-run',
	'--no-default-browser-check',
	'--hide-scrollbars',
	`--user-data-dir=${userDataDir}`,
	'--remote-debugging-port=9223',
	`--window-size=${VIEW.width},${VIEW.height}`,
	'about:blank'
]);

const cleanup = () => {
	try {
		chrome.kill('SIGKILL');
	} catch {
		/* already gone */
	}
};

try {
	// Wait for the HTTP debugger endpoint, then find a page target.
	let targets;
	const until = Date.now() + 15000;
	while (Date.now() < until) {
		try {
			targets = await (await fetch('http://127.0.0.1:9223/json/list')).json();
			if (targets.some((t) => t.type === 'page')) break;
		} catch {
			/* not up yet */
		}
		await sleep(150);
	}
	const target = targets?.find((t) => t.type === 'page');
	if (!target) throw new Error('no page target from Chrome');

	const page = await Page.connect(target.webSocketDebuggerUrl);

	await page.send('Page.enable');
	await page.send('Runtime.enable');
	await page.send('Network.enable');
	await page.send('Emulation.setDeviceMetricsOverride', {
		width: VIEW.width,
		height: VIEW.height,
		deviceScaleFactor: 2,
		mobile: false
	});

	// Seed the session id so the app resumes straight into it.
	const nav = await page.send('Page.navigate', { url: BASE + '/delivery' });
	if (nav.errorText) throw new Error(`navigate: ${nav.errorText}`);

	// localStorage must be set on the origin before the app reads it. The app
	// only calls resume() on mount, so set it then reload.
	await page.eval('1');
	await page.eval(`localStorage.setItem('drbrain-delivery.lastSession', ${JSON.stringify(sessionId)})`);
	await page.eval(
		`localStorage.setItem('drbrain-delivery.ownerTokens', ${JSON.stringify(JSON.stringify({ [sessionId]: ownerToken }))})`
	);
	await page.send('Page.reload', {});

	// The map is 400 buttons; wait for the grid plus a ledger value.
	const rendered = await page.waitFor(
		`document.querySelectorAll('button[aria-label^="Car"], button[aria-label*="at "]').length > 100`
	);
	if (!rendered) {
		const body = await page.eval('document.body.innerText.slice(0, 400)');
		throw new Error(`map grid did not render. Body was:\n${body}`);
	}

	// Poll until the snapshot has been applied (tick shown in the header).
	await page.waitFor(`document.body.innerText.includes('tick')`);

	const report = await page.eval(`(() => {
		const text = document.body.innerText;
		// Panel headers are uppercased with CSS text-transform, and innerText
		// reflects the rendered casing, so compare case-insensitively.
		const lower = text.toLowerCase();
		const tiles = document.querySelectorAll('button[aria-label*="at "]').length;
		// Clipping check. The page scrolls normally inside the shared shell, so a
		// panel below the fold is fine. What is a bug is a panel whose content is
		// taller than its box while overflow is hidden/clip: that content is
		// unreachable. Panels are section elements carrying a heading; anchored on
		// the element, not a class name, so a restyle cannot silently skip it.
		const panels = [...document.querySelectorAll('section')].filter((sec) => sec.querySelector('h2, h3'));
		const clippedPanels = panels
			.filter((sec) => {
				const oy = getComputedStyle(sec).overflowY;
				return (oy === 'hidden' || oy === 'clip') && sec.scrollHeight > sec.clientHeight + 1;
			})
			.map((sec) => ({
				heading: (sec.querySelector('h2, h3')?.textContent ?? '').trim(),
				scrollHeight: sec.scrollHeight,
				clientHeight: sec.clientHeight
			}));
		// Reachability: the Activity panel exists and has height. Below the fold is
		// fine, so scroll it into view and measure there.
		const activity = [...document.querySelectorAll('h2')].find((h) =>
			h.textContent.toLowerCase().includes('activity')
		);
		const activityCard = activity ? activity.closest('section') : null;
		if (activityCard) activityCard.scrollIntoView({ block: 'center' });
		const activityHeight = activityCard ? Math.round(activityCard.getBoundingClientRect().height) : -1;
		window.scrollTo(0, 0);
		// Every locked upgrade must say what it is waiting for. A card that only
		// says "locked" is a dead end, and the client's GraphQL selection is
		// hand-written, so a new criteria field can reach the schema and the Go
		// types while this panel still renders an empty line.
		const growth = [...document.querySelectorAll('h2')].find((h) =>
			h.textContent.toLowerCase().includes('city growth')
		);
		const growthCards = growth ? [...growth.closest('section').querySelectorAll('li')] : [];
		const gated = growthCards.filter((li) => li.textContent.includes('locked'));
		const silent = gated.filter((li) => !/\\d/.test(li.textContent.replace(/^.*?locked/s, '')));
		return {
			tiles,
			hasTick: /tick\\s*\\d+/.test(lower),
			hasMoney: text.includes('$'),
			hasFleet: lower.includes('fleet'),
			hasOrders: lower.includes('orders'),
			hasGrowth: lower.includes('city growth'),
			gatedCards: gated.length,
			silentCards: silent.map((li) => li.textContent.trim().split('\\n')[0]),
			hasDpad: !!document.querySelector('button[aria-label="Move north"]'),
			viewportH: window.innerHeight,
			panelCount: panels.length,
			clippedPanels,
			activityHeight,
			docHeight: document.documentElement.scrollHeight,
			title: document.title,
			header: text.split('\\n').slice(0, 12).join(' | ')
		};
	})()`);

	console.log('rendered report:', JSON.stringify(report, null, 2));

	// --- interaction: drive with the keyboard and tick -----------------------
	//
	// Rendering proves the snapshot is drawn; this proves a keystroke reaches
	// the API and comes back as new state. Read the car's tile from the DOM
	// before and after.
	const carPos = () =>
		page.eval(`(() => {
			// Match the label Map.svelte actually emits. It reads
			// "N cars at x,y, names" (or "1 car at x,y, name" for a single
			// car), so a prefix match on "Car" silently stopped working when
			// the label was reworded for co-located cars. Match the shape, not
			// the first letter.
			const el =
				document.querySelector('button[aria-label*=" car at "]') ||
				document.querySelector('button[aria-label*=" cars at "]');
			if (!el) return null;
			const m = /at (\\d+),(\\d+)/.exec(el.getAttribute('aria-label') || '');
			return m ? [Number(m[1]), Number(m[2])] : null;
		})()`);

	const before = await carPos();
	if (!before) throw new Error('could not read car position from the DOM');

	// Focus the page body so keydown lands on the window listener.
	await page.eval('document.body.focus()');
	// A direction key now drives AND steps, so one keypress must move the car.
	await page.send('Input.dispatchKeyEvent', {
		type: 'keyDown',
		key: 's',
		code: 'KeyS',
		windowsVirtualKeyCode: 83
	});
	await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 's', code: 'KeyS' });
	await sleep(600); // let the move+tick round trip and the UI re-render

	const after = await carPos();
	console.log('car position:', before, '->', after);

	// --- fleet upkeep is visible, not just charged -------------------------
	//
	// The car started on the depot tile, where upkeep is zero, and the move
	// above takes it off. So the running-cost chip must appear ONLY after this
	// point: if it is missing the player is being billed invisibly, and if it
	// was present before the move the rule is wrong.
	const upkeepChip = () =>
		page.eval(`(() => {
			const el = document.querySelector('[data-upkeep-due]');
			return el ? Number(el.getAttribute('data-upkeep-due')) : null;
		})()`);
	const upkeepAfterMove = await upkeepChip();
	console.log('upkeep chip after leaving depot:', upkeepAfterMove);

	// --- live refresh: a tick from another client must reach this tab --------
	//
	// The session stays paused here, which is exactly the case that used to
	// freeze: the page only polled while the clock ran, so ticks stepped by the
	// CLI, an agent or a second browser never showed up. Read the tick number
	// from the header, step from outside, and expect the page to catch up.
	const headerTick = () =>
		page.eval(`(() => {
			const m = /tick\\s*(\\d+)/i.exec(document.body.innerText);
			return m ? Number(m[1]) : null;
		})()`);

	const tickBefore = await headerTick();
	await gql(
		`mutation ($id: ID!, $ownerToken: String, $count: Int!) { delivery { tick(id: $id, ownerToken: $ownerToken, count: $count) { tick } } }`,
		{ id: sessionId, ownerToken, count: 3 }
	);
	const refreshOk = await page.waitFor(`(() => {
		const m = /tick\\s*(\\d+)/i.exec(document.body.innerText);
		return m && Number(m[1]) > ${tickBefore};
	})()`, 4000);
	const tickAfter = await headerTick();
	console.log('external tick while paused:', tickBefore, '->', tickAfter, refreshOk ? '(refreshed)' : '(STALE)');

	// Screenshot for human review.
	const shot = await page.send('Page.captureScreenshot', { format: 'png' });
	writeFileSync(OUT, Buffer.from(shot.data, 'base64'));
	console.log('screenshot:', OUT);
	const ownerErrors = [...page.errors];
	const ownerFailed = [...page.failedRequests];
	console.log('phase 1 console errors:', JSON.stringify(ownerErrors));
	console.log('phase 1 failed requests:', JSON.stringify(ownerFailed));

	// --- phase 2: spectator mode ---------------------------------------------
	//
	// Same browser, same session, but without the owner token. Only the token is
	// cleared; lastSession stays so the app resumes into the session it cannot play.
	page.errors.length = 0;
	page.failedRequests.length = 0;
	await page.eval(`localStorage.removeItem('drbrain-delivery.ownerTokens')`);
	await page.send('Page.reload', {});
	const noticeShown = await page.waitFor(`document.querySelector('[data-testid="spectator-notice"]')`);
	const spectator = noticeShown
		? await page.eval(`(() => {
			const notice = document.querySelector('[data-testid="spectator-notice"]');
			const speedButtons = [...document.querySelectorAll('button')].filter((b) =>
				/Run at|Pause the clock/.test(b.getAttribute('title') ?? '')
			);
			return {
				notice: notice.textContent.trim(),
				speedButtons: speedButtons.length,
				dpad: !!document.querySelector('button[aria-label="Move north"]'),
				tiles: document.querySelectorAll('button[aria-label*="at "]').length
			};
		})()`)
		: null;
	console.log('spectator report:', JSON.stringify(spectator));
	const shot2 = await page.send('Page.captureScreenshot', { format: 'png' });
	writeFileSync(SPECTATOR_OUT, Buffer.from(shot2.data, 'base64'));
	console.log('screenshot:', SPECTATOR_OUT);
	console.log('phase 2 console errors:', JSON.stringify(page.errors));
	console.log('phase 2 failed requests:', JSON.stringify(page.failedRequests));

	const problems = [];
	if (!report.hasTick) problems.push('no tick counter in header');
	if (!report.hasMoney) problems.push('no money in header');
	if (upkeepAfterMove === null)
		problems.push('fleet upkeep chip absent after leaving the depot (player is billed invisibly)');
	if (upkeepAfterMove === 0) problems.push('fleet upkeep chip shows $0 for a car off the depot');
	if (!report.hasFleet) problems.push('fleet panel missing');
	if (!report.hasOrders) problems.push('orders panel missing');
	if (!report.hasGrowth) problems.push('city growth panel missing');
	if (report.gatedCards === 0) problems.push('no locked upgrade cards: the criteria check did not run');
	if (report.silentCards?.length)
		problems.push(`locked upgrades with no criteria shown: ${report.silentCards.join(', ')}`);
	if (report.tiles < 100) problems.push(`only ${report.tiles} tiles rendered`);
	if (!report.hasDpad) problems.push('drive D-pad missing');
	if (report.panelCount === 0) problems.push('found no panel sections: the clipping check did not run');
	for (const c of report.clippedPanels) {
		problems.push(
			`panel "${c.heading}" clips its content: scrollHeight ${c.scrollHeight}px > clientHeight ${c.clientHeight}px with overflow hidden`
		);
	}
	if (report.activityHeight === -1) {
		problems.push('could not locate the activity panel: the reachability check did not run');
	} else if (report.activityHeight <= 0) {
		problems.push('activity panel has zero height');
	}
	// INFO only: the page scrolls normally inside the shared shell.
	console.log(
		`info: document ${report.docHeight}px tall in a ${report.viewportH}px viewport; ${report.panelCount} panels, activity panel ${report.activityHeight}px tall`
	);
	// Keyboard driving must actually move the car, not just redraw.
	if (!after || !before || (after[0] === before[0] && after[1] === before[1])) {
		problems.push(`keyboard drive did not move the car (${before} -> ${after})`);
	}
	// A tick from another client must appear without the player touching this
	// tab. This is the regression that made a paused session look frozen.
	if (!refreshOk) {
		problems.push(
			`UI did not auto-refresh: tick stayed at ${tickAfter} after an external tick (was ${tickBefore})`
		);
	}
	if (ownerErrors.length) problems.push(`phase 1 console errors: ${ownerErrors.join(' | ')}`);
	if (ownerFailed.length) problems.push(`phase 1 failed requests: ${ownerFailed.join(' | ')}`);

	// Spectator phase.
	if (!spectator) {
		const body = await page.eval('document.body.innerText.slice(0, 600)');
		problems.push(`spectator notice never appeared. Body was:\n${body}`);
	} else {
		if (!/read-only/i.test(spectator.notice)) problems.push(`spectator notice does not say read-only: "${spectator.notice}"`);
		if (spectator.speedButtons !== 0) problems.push(`${spectator.speedButtons} speed buttons still shown to a spectator`);
		if (spectator.dpad) problems.push('drive D-pad still shown to a spectator');
		if (spectator.tiles < 100) problems.push(`spectator sees only ${spectator.tiles} tiles`);
	}
	if (page.errors.length) problems.push(`phase 2 console errors: ${page.errors.join(' | ')}`);
	if (page.failedRequests.length) problems.push(`phase 2 failed requests: ${page.failedRequests.join(' | ')}`);

	page.close();

	if (problems.length) {
		console.error('\nUI SMOKE FAILED:');
		for (const p of problems) console.error('  -', p);
		process.exitCode = 1;
	} else {
		console.log('\nUI SMOKE OK');
	}
} finally {
	cleanup();
	// Best-effort session cleanup; the save file is harmless if this fails.
	try {
		await gql(
			`mutation ($id: ID!, $ownerToken: String) { delivery { deleteSession(id: $id, ownerToken: $ownerToken) } }`,
			{ id: sessionId, ownerToken }
		);
	} catch {
		/* ignore */
	}
}
