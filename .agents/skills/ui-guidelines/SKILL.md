---
name: ui-guidelines
description: Applies drbrain’s shared design system when creating, changing, or reviewing Svelte UI. Use for routes, navigation, auth, account screens, gameplay interfaces, styling, artwork, responsive layouts, accessibility, and browser QA.
---

# drbrain UI guidelines

Build a calm, light indigo suite with a consistent shell and room for each game to express its own character. Make the page’s purpose and primary action obvious. Give people a clear path from choosing a game to playing it.

## Start with the shared system

- Read [AGENTS.md](../../../AGENTS.md) for repository conventions. Inspect the closest working screen and its states before editing. Use [tokens.css](../../../web/packages/ui/src/tokens.css) for exact values and [app.css](../../../web/src/app.css) for suite-wide layout and focus styles.
- Import components from `@drbrain/ui/components/<Name>.svelte`. Put reusable primitives in the [shared UI package](../../../web/packages/ui/src/components). Keep game-specific controls, boards, and rules in the owning game. Use Tailwind utilities for local layout and scoped CSS for behavior specific to a component. Extend the shared tokens when a pattern needs to recur across features.
- The [root layout](../../../web/src/routes/+layout.svelte) provides `Header`, `AccountMenu`, the skip link, the main landmark, and `Footer`. A game layout uses `GameNav` for the all-games link, current game, and route tabs. Highlight the current tab with `aria-current` and keep it accessible when navigation scrolls.
- Game names, primary routes, category labels, pitches, and artwork come from the [game catalog](../../../web/packages/ui/src/catalog.ts). Use `Hero` for a landing page introduction and `GameCard` for catalog entries. In a live session, use compact game context and reserve the space for the board and controls.

## Color, type, and space

- Use `--db-page` for the light indigo canvas and `--db-surface` for cards, dialogs, and navigation. Use `--db-ink` and `--db-muted` for text, `--db-line` for separation, and `--db-control` for visible interactive boundaries. Reserve `--db-danger` and `--db-success` for feedback.
- Use the shared indigo brand for suite actions. Within `[data-game]`, use `--db-accent` and `--db-accent-wash` for game entry actions, selected navigation, and small identifiers. Mazes is violet, Motor Programming green, Tesla Road Trip terracotta, Delivery blue, and Music Region amber. Gameplay colors may convey map or score information. Keep their meaning consistent and pair important colors with a shape, icon, or text.
- Use the system sans stack for body text, headings, and interface labels. Use the Georgia stack for the short italic emphasis in a hero. Let `db-heading` set the fluid display scale and use `db-kicker` for meaningful context like a game name. Prefer compact headings and readable body copy. Give numeric HUDs and scores tabular numerals. Avoid adding a font download for a single feature.
- Align content with `db-container` and its fluid gutters. Use a consistent spacing rhythm within cards and larger gaps between sections. Use `db-panel` or `Card` for grouped surfaces, the large radius for hero and modal surfaces, and shallow elevation for cards. Reserve raised shadows for menus and dialogs. Keep motion short and purposeful, using `--db-motion` and the shared reduced-motion rule.

## Components and states

- Use `Button` for actions and navigation CTAs. Give a section one obvious primary action. Use secondary buttons for alternatives, quiet buttons for lower-priority actions, and `db-text-link` for links in prose. Give icon-only controls an accessible name.
- Use `Input` for text fields and the shared input styles for native controls that need specialized behavior. Give fields persistent labels, useful autocomplete, and help tied through `aria-describedby`. Validate where the user can fix the issue, set `aria-invalid`, and retain entered values on a recoverable error.
- Use `Modal` for a short, focused decision. The native dialog handles focus containment. Keep the close action visible and return focus to the trigger when it closes. Use `Toast` for transient notices and inline feedback for errors that users need while working.
- Give async screens deliberate loading, empty, and error states. Use `Skeleton` to reserve space while loading and `EmptyState` to explain the next available action. Keep the pending action legible and disable duplicate submission while it runs. Announce meaningful state changes without reading out every live update.
- Keep the voice direct and calm. Prefer labels like “Choose a map”, “Start a new city”, and “Save changes”. Use real features and data. Skip invented metrics, testimonials, and decorative labels.

## Responsive and accessible interaction

- Start with the narrow layout. Stack content, wrap actions, and keep wide tables, navigation, and boards inside their own scroll containers when they need extra width. Keep the page itself free of horizontal overflow. On wide screens, use columns for independent panels and align them to the same content grid.
- Keep keyboard focus visible with the shared `--db-focus` ring. Aim for 44 × 44 CSS px on touch actions where practical. Keep tab order aligned with visual order, offer a touch control for keyboard-only gameplay actions, and preserve Tab for moving focus.
- Target WCAG AA contrast in each state. Use at least 4.5:1 for normal text and 3:1 for large text and essential control boundaries or indicators. Check light, dark, selected, focus, error, and hover states as applicable. Handle reduced motion without losing a status cue.
- Keep game surfaces crisp at high DPI using responsive SVG or correctly sized canvas backing stores. Test board zoom, touch controls, and long labels with real data. In Music Region, preserve the engraved SVG’s fitted viewBox and allow the card to fit notation, including stems and ledger lines.
- Motor Programming owns the dark theme. Keep theme pre-paint in [app.html](../../../web/src/app.html) gated to `/motor` and verify light mode returns on exit.

## Artwork, metadata, and integration

- Match the existing [art collection](../../../web/static/art). Use tactile isometric illustrations with a game-specific palette, a clear subject, and enough breathing room for mobile crops. Use lightweight WebP for decorative page art and give images dimensions or an aspect ratio. Use empty alt text for redundant decorative art and meaningful alt text for informative imagery.
- Give each route a useful title and description. Follow the root layout’s metadata conventions and keep the suite favicon and social preview in `app.html`. Unknown routes should offer a clear return to the game collection.
- Preserve routes and deep links, including query parameters and fragments where supported. Reuse the [account helpers](../../../web/src/lib/account/account.ts) for return paths and provider URLs. Show providers reported by the server. Keep the JSON transport for account mutations that set or clear the session cookie.
- Reuse the existing game clients and namespace operations at the shared GraphQL endpoint. Direct WebSocket consumers read the flat subscription field specified by their operation. Only link to API guides and tools the server provides. Keep dependencies and new styles proportionate to the work.

## Verify in the running app

1. Open the affected route and its entry points in a real browser at 1440 px and 390 px. Check the page top and the complete flow below it. Inspect intermediate widths when the layout changes there.
2. Exercise the primary action and any changed loading, empty, error, and success states with real data. Check keyboard navigation, touch-sized controls, visible focus, overflow, and the menu or modal close path. For changes to the shell, click into each game and back home.
3. For auth and account changes, verify registration, sign-in, sign-out, the return path, and the resulting signed-in screen. For gameplay changes, start a session and check the actual HUD and board or score, including live updates when relevant.
4. Run `cd web && npm run check && npm test && npm run build` with Node 22. Use the npm workspace setup and `just restart` when verifying the full stack. Report any browser states or integrations that could not be verified.
