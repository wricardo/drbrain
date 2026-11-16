---
name: verify-sveltekit-ui-change
description: "Verify a SvelteKit UI change in drbrain-motor-programming end-to-end with real headless Chrome over CDP, when the vite dev proxy may point at a port owned by another project."
---

# Verify a SvelteKit UI change in drbrain-motor-programming

`npm run dev` is unreliable here: `frontend/vite.config.ts` proxies `/graphql` to `localhost:8000`, and that port is often occupied by a *different* Go project. Symptom: the page loads but shows `Cannot query field "width" on type "Map"` — a schema mismatch, not a bug in your change. Confirm with `lsof -nP -iTCP:8000 -sTCP:LISTEN` and by querying `{ maps { id } }` for unfamiliar field names.

## Recipe

1. Build the real UI (also the documented workflow — `static/` is committed):
   ```sh
   just build-ui
   ```
2. Start *this* repo's server on a free port; the Go server serves `static/` and falls back to `index.html` for SPA routes:
   ```sh
   go run . -port 8123 -debug   # check `lsof -nP -iTCP:8123` first
   ```
   Verify the schema is the right one before driving the UI.
3. Launch headless Chrome with CDP and a scratch profile:
   ```sh
   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new \
     --remote-debugging-port=9333 --user-data-dir=/tmp/drbrain-chrome --no-first-run --disable-gpu
   ```
   Confirm with `curl -s http://127.0.0.1:9333/json/version`.
4. Drive it with a Node script importing `ws` straight from the installed tree:
   ```js
   import WebSocket from '<repo>/frontend/node_modules/ws/index.js';
   ```
   Open a tab with `PUT http://127.0.0.1:9333/json/new?about:blank`, connect to `webSocketDebuggerUrl`, then use `Page.enable`, `Runtime.enable`, `Page.navigate`, `Runtime.evaluate` (`returnByValue: true`, `userGesture: true`), and `Page.captureScreenshot`. (The `browser` eval global is quicker if the session has it: `browser.open`, `tab.evaluate`, `tab.screenshot`.)
5. Seed state over GraphQL instead of clicking your way in. Pass enum arguments as **typed variables**, not inline literals (`Instruction!` rejects string literals inside the document). Tape lengths must match the map exactly (`invalid program: sub1 has 1 slots, want 10`). Play mutations need the `ownerToken` from `createSession`.
6. Wait for render by polling a `data-testid`, never a fixed sleep:
   ```js
   for (let i = 0; i < 100; i++) { if (await ev(`!!document.querySelector('[data-testid="slot--1-0"]')`)) break; await wait(100); }
   ```
7. Handle `window.confirm` without stubbing, by intercepting the CDP event — this proves the real dialog appears:
   ```js
   if (m.method === 'Page.javascriptDialogOpening') {
     dialogs.push(m.params.message);
     await send('Page.handleJavaScriptDialog', { accept: dialogAccept });
   }
   ```
   Flip `dialogAccept` and click twice to assert the cancel path is a true no-op *and* the accept path works.
8. Read state back from the DOM (`aria-label` is a good stable channel when glyphs are visual) and cross-check server state with a GraphQL query to prove whether an edit was only a draft.
9. Screenshot and review it (`read` the PNG, or `read` with `?q=<question>`). For a board change, screenshot the board `svg` itself: at 32 cells a legal layout can still read as a dark slab.
10. Take stock before reporting: `git stash push` the source, rebuild, and diff `static/` — asset-hash churn reproduces on untouched HEAD, so it is pre-existing drift, not your change.
11. Always run `npx vitest run` and `npm run check` (svelte-check) afterwards, and `pkill` the server, Chrome and probe scripts.

## Colours

Tailwind v4 compiles to `oklch()`, so `getComputedStyle(el).backgroundColor` returns e.g. `oklch(0.586 0.253 17.585)` and any `rgb()` assertion fails even when the colour is correct. Convert inside the page instead of guessing:

```js
const ctx = document.createElement('canvas').getContext('2d', { colorSpace: 'srgb' });
ctx.fillStyle = 'oklch(0.586 0.253 17.585)';
ctx.fillRect(0, 0, 1, 1);
const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data; // 236,0,63
```

## Repo conventions worth matching

- Destructive controls use `rose` (`bg-rose-600` + `hover:bg-rose-700`, or `border-rose-300 text-rose-800 hover:bg-rose-50` for secondary), never `red-*`; confirm with `window.confirm` as the map editor does.
- Svelte 5 runes only. Logic goes in plain `lib/*.ts` with Vitest tests; Svelte components stay thin.
- Explicit `onchange`/`oninput` handlers run *before* `bind:value` updates — read `event.currentTarget.value`.
