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
// Usage: node scripts/ui-smoke.mjs [baseUrl] [outPng]

import { spawn } from 'node:child_process';
import { writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const BASE = process.argv[2] ?? 'http://localhost:8081';
const OUT = process.argv[3] ?? '/tmp/ui.png';
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

const VIEW = { width: 1440, height: 950 };

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// GraphQL is the only transport. These helpers post operations straight to it.
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
await gql(`mutation ($id: ID, $seed: Int) { createSession(id: $id, seed: $seed) { id } }`, {
	id: sessionId,
	seed: 12345
});
// Pause so the screenshot is deterministic, then step so orders and movement
// exist. (New sessions already start paused; explicit for clarity.)
await gql(`mutation ($id: ID!, $speed: Int!) { setSpeed(id: $id, speed: $speed) { tick } }`, {
	id: sessionId,
	speed: 0
});
await gql(`mutation ($id: ID!, $count: Int!) { tick(id: $id, count: $count) { tick } }`, {
	id: sessionId,
	count: 6
});

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
	const nav = await page.send('Page.navigate', { url: BASE + '/' });
	if (nav.errorText) throw new Error(`navigate: ${nav.errorText}`);

	// localStorage must be set on the origin before the app reads it. The app
	// only calls resume() on mount, so set it then reload.
	await page.eval('1');
	await page.eval(`localStorage.setItem('drbrain:session', ${JSON.stringify(sessionId)})`);
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
		// Clipping check: every panel that should be on screen must end above the
		// viewport bottom. This is what catches "activity panel cut off".
		const activity = [...document.querySelectorAll('h2')].find((h) =>
			h.textContent.toLowerCase().includes('activity')
		);
		const activityCard = activity ? activity.closest('div.rounded-lg') : null;
		const activityBottom = activityCard
			? Math.round(activityCard.getBoundingClientRect().bottom)
			: -1;
		return {
			tiles,
			hasTick: /tick\\s*\\d+/.test(lower),
			hasMoney: text.includes('$'),
			hasFleet: lower.includes('fleet'),
			hasOrders: lower.includes('orders'),
			hasGrowth: lower.includes('city growth'),
			hasDpad: !!document.querySelector('button[aria-label="Move north"]'),
			viewportH: window.innerHeight,
			activityBottom,
			docHeight: document.documentElement.scrollHeight,
			docScrollable: document.documentElement.scrollHeight > window.innerHeight,
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
			const el = document.querySelector('button[aria-label^="Car"]');
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
			const m = /tick\\s*(\\d+)/.exec(document.body.innerText);
			return m ? Number(m[1]) : null;
		})()`);

	const tickBefore = await headerTick();
	await gql(`mutation ($id: ID!, $count: Int!) { tick(id: $id, count: $count) { tick } }`, {
		id: sessionId,
		count: 3
	});
	const refreshOk = await page.waitFor(`(() => {
		const m = /tick\\s*(\\d+)/.exec(document.body.innerText);
		return m && Number(m[1]) > ${tickBefore};
	})()`, 4000);
	const tickAfter = await headerTick();
	console.log('external tick while paused:', tickBefore, '->', tickAfter, refreshOk ? '(refreshed)' : '(STALE)');

	// Screenshot for human review.
	const shot = await page.send('Page.captureScreenshot', { format: 'png' });
	writeFileSync(OUT, Buffer.from(shot.data, 'base64'));
	console.log('screenshot:', OUT);

	const problems = [];
	if (!report.hasTick) problems.push('no tick counter in header');
	if (!report.hasMoney) problems.push('no money in header');
	if (upkeepAfterMove === null)
		problems.push('fleet upkeep chip absent after leaving the depot (player is billed invisibly)');
	if (upkeepAfterMove === 0) problems.push('fleet upkeep chip shows $0 for a car off the depot');
	if (!report.hasFleet) problems.push('fleet panel missing');
	if (!report.hasOrders) problems.push('orders panel missing');
	if (!report.hasGrowth) problems.push('city growth panel missing');
	if (report.tiles < 100) problems.push(`only ${report.tiles} tiles rendered`);
	if (!report.hasDpad) problems.push('drive D-pad missing');
	// The activity panel is the bottom-most element; if it runs past the
	// viewport the dashboard is clipped and the player cannot see their feed.
	if (report.activityBottom > report.viewportH) {
		problems.push(
			`layout clipped: activity panel ends at ${report.activityBottom}px but viewport is ${report.viewportH}px`
		);
	}
	// The page itself must never scroll: panels scroll internally instead.
	// A scrolling document means the chrome (header, controls) can drift off
	// screen, which is what made the activity feed unreachable earlier.
	if (report.docScrollable) {
		problems.push(
			`page overflows: document is ${report.docHeight}px tall in a ${report.viewportH}px viewport (panels should scroll internally)`
		);
	}
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
	if (page.errors.length) problems.push(`console errors: ${page.errors.join(' | ')}`);
	if (page.failedRequests.length)
		problems.push(`failed requests: ${page.failedRequests.join(' | ')}`);

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
		await gql(`mutation ($id: ID!) { deleteSession(id: $id) }`, { id: sessionId });
	} catch {
		/* ignore */
	}
}
