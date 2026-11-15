import{$ as e,A as t,B as n,C as r,G as i,L as a,M as o,P as s,Q as c,R as l,X as u,a as d,d as f,f as p,it as m,j as h,nt as g,tt as _,ut as v,v as y}from"./C-c4u71Z.js";import"./xihTtKlq.js";import{r as b}from"./CX2fdOv1.js";var x=s(`<p role="alert" class="mt-2 text-xs text-amber-800">Could not access the clipboard — the prompt is selected below, press Ctrl/Cmd+C.</p>`),S=s(`<div class="rounded-xl border border-indigo-200 bg-white p-4" data-testid="ai-prompt-card"><div class="flex items-start justify-between gap-3"><div class="min-w-0"><h2 class="text-xs font-semibold uppercase tracking-widest text-indigo-900"> </h2> <p class="mt-1 text-sm text-slate-700"> </p></div> <button type="button" data-testid="ai-prompt-copy"> </button></div> <p class="sr-only" role="status" aria-live="polite"> </p> <!> <details class="mt-3"><summary class="cursor-pointer select-none text-xs font-medium text-slate-700 hover:text-slate-900">Show full prompt</summary> <textarea readonly="" rows="14" aria-label="Prompt for an AI" class="mt-2 w-full resize-y rounded-lg border border-indigo-100 bg-slate-50 p-3 font-mono text-xs leading-relaxed text-slate-700"></textarea></details></div>`);function C(a,s){let b=d(s,`title`,3,`Play with an AI`),C=g(!1),w=g(!1),T=g(!1),E=g(null),D;async function O(){_(w,!1);try{await navigator.clipboard.writeText(s.prompt),_(C,!0),clearTimeout(D),D=setTimeout(()=>_(C,!1),2e3)}catch{_(w,!0),_(T,!0),queueMicrotask(()=>n(E)?.select())}}var k=S(),A=u(k),j=u(A),M=u(j),N=c(M,!0),P=e(M,2),F=c(P,!0);v(j);var I=e(j,2),L=c(I,!0);v(A);var R=e(A,2),z=c(R,!0),B=e(R,2),V=e=>{var t=x();o(e,t)};t(B,e=>{n(w)&&e(V)});var H=e(B,2),U=e(u(H),2);m(U),p(U,e=>_(E,e),()=>n(E)),v(H),v(k),i(()=>{h(N,b()),h(F,s.blurb),r(I,1,`shrink-0 rounded-full border px-4 py-2 text-sm font-semibold transition-colors ${n(C)?`border-emerald-300 bg-emerald-50 text-emerald-800`:`border-indigo-300 text-indigo-800 hover:bg-indigo-50`}`),h(L,n(C)?`Copied!`:`Copy prompt`),h(z,n(C)?`Prompt copied to clipboard`:``),y(U,s.prompt)}),l(`click`,I,O),l(`click`,U,e=>e.currentTarget.select()),f(`open`,`toggle`,H,e=>_(T,e),()=>n(T)),o(a,k)}a([`click`]);var w=e=>[`EMPTY`,`TURN_LEFT`,`TURN_RIGHT`,`MOVE_FORWARD`,...Array.from({length:e},(e,t)=>`CALL_SUB_${t+1}`)].join(`, `),T=e=>`[${Array(e).fill(`EMPTY`).join(`, `)}]`;function E(e){let t=e.reduce((e,t)=>Math.max(e,t.length),0),n=String(Math.max(0,e.length-1)).length;return[` `.repeat(n+3)+Array.from({length:t},(e,t)=>String(t%10)).join(``),...e.map((e,t)=>`y${String(t).padStart(n,` `)}  ${e}`)].join(`
`)}var D={UP:`up (y-1)`,RIGHT:`right (x+1)`,DOWN:`down (y+1)`,LEFT:`left (x-1)`};function O(e,t){let n=t.map,r=n.subTapeLengths,i=[`main = ${n.mainTapeLength} slots`,...r.map((e,t)=>`sub${t+1} (subs[${t}]) = ${e} slots`)].join(`; `),a=`main: ${T(n.mainTapeLength)}\n    subs: [${r.map(T).join(`, `)}]`,o=n.treats.map(e=>`(${e.x},${e.y})`).join(` `),s=n.allowRecursion?`Recursion is ALLOWED on this map: a sub may call itself (or any sub already running). The call stack is bounded by maxCallDepth = ${n.maxCallDepth} and by the step limit, and there is no tail-call optimisation (the stack stays deep until a sub finishes).`:`Recursion is NOT allowed on this map: a CALL_SUB_k aimed at the sub that is running, or at any sub already waiting on the call stack, does nothing (the step is still spent). A sub may call a different sub that is not on the stack, so you can nest subs; maxCallDepth = ${n.maxCallDepth}.`;return`Use this GraphQL API to play an existing ${b} session and WIN it.

${b} is a robot programming puzzle. You do not steer the robot: you write it a program (a main tape plus ${r.length} subroutine ${r.length===1?`tape`:`tapes`} of instructions), the server runs it one instruction per step, and you win the moment every treat has been collected.

Session ID: ${t.id}
GraphQL endpoint: ${e}/graphql
Playground: ${e}/playground
Full rules and every field: ${e}/llms.txt
Spectators can watch live at: ${e}/watch/${t.id}

GraphQL introspection is usually enabled; use the Playground or __schema/__type if you need to discover fields.

## This map: ${n.name} (${n.width}x${n.height}, ${n.difficulty})
${n.description}

Layout (x = column, y = row; row 0 is the TOP and y grows DOWNWARD):
${E(n.layout)}

'.' empty, '#' rock (blocks), '*' treat, '^ > v <' = robot start cell and initial facing.
Robot starts at (${n.start.x},${n.start.y}) facing ${D[n.startFacing]??n.startFacing}.
Treats (${n.treats.length}): ${o}

Tapes (setProgram needs EXACTLY these lengths; pad unused slots with EMPTY): ${i}.
Limits: at most ${n.maxSteps} steps; ${s}

## Rules that matter
- Instructions: ${w(r.length)}.
- Each executed instruction is one step (a blocked move and a CALL both count). EMPTY slots are skipped for free.
- MOVE_FORWARD into a rock or off the map is a harmless no-op (still a step, "blocked: true"). Treats are collected on arrival.
- When recursion is off, a CALL to a sub already on the call stack is ignored but still costs a step ("callBlocked: true").
- When a sub tape ends, control returns to the caller; when the main tape ends with treats left, you lose (PROGRAM_ENDED). Other losses: STEP_LIMIT, CALL_DEPTH.
- Win beats the step limit on the same step. setProgram and reset put the robot back at the start. setProgram is rejected while the session is playing (SESSION_PLAYING); finished sessions refuse run/step until reset (SESSION_TERMINAL).

## How to win
1. Plan on paper first: write the route as straight runs and turns using the coordinates above. Tapes are tiny, so look for repeated patterns and put them in subroutines (a sub can be a "row", a "U-turn", a "spiral side"...). Remember a blocked move costs a step but does no harm, so an over-long run of MOVE_FORWARD is safe when a wall will stop it.
2. Store the program in the session:
mutation {
  setProgram(sessionID: "${t.id}", program: {
    ${a}
  }) { id vm { status steps } }
}
   (Replace the EMPTY slots with real instructions.)
3. Run it on the server (50-5000 ms per step) or step manually, then read the result:
mutation { run(sessionID: "${t.id}", speedMs: 100) { playing vm { status steps } } }
mutation { step(sessionID: "${t.id}") { vm { status steps pos { x y } } lastEvent { instruction blocked callBlocked } } }
query { session(id: "${t.id}") { playing attempts bestSteps vm { status lossReason steps pos { x y } facing treatsRemaining { x y } callStack { tape pc } } } }
4. If the status is LOST, read lossReason and treatsRemaining, fix the program, and repeat from step 2 (call reset first if you want to run again without changing the program). Do not stop until vm.status is WON, then report the winning program and its step count.
5. Winners are ranked per map by slots used (non-EMPTY slots across all tapes; fewer is better), then by steps. Once you have won, you may try to win again with fewer slots or steps (setProgram, then run) and read your rank:
query { leaderboard(mapId: "${t.map.id}", sessionId: "${t.id}") { total yours { rank slots steps } entries { rank displayName slots steps } } }

Other mutations: pause, reset, renameSession, deleteSession. Do not try to guess other sessions' ids and do not delete this one.`}function k(e){return`Play ${b}, a robot programming puzzle, through its GraphQL API and win it.

You write the robot a program (a main tape plus up to 3 subroutine tapes); the server runs it one instruction per step; you win when every treat has been collected. Tapes are tiny, so the trick is spotting repeated patterns and reusing them with subroutines.

1. Read ${e}/llms.txt first: it has the complete rules, the coordinate convention (row 0 is the top, y grows downward) and working examples for every step.
2. GraphQL endpoint: ${e}/graphql (Playground: ${e}/playground).
3. List the maps: query { maps { id name difficulty layout mainTapeLength subTapeLengths maxSteps maxCallDepth allowRecursion } } and pick one (start with an easy one).
4. Create your own session: mutation { createSession(mapID: "MAP_ID", displayName: "my agent") { id } } (displayName is optional: without it you get a random guest name like BraveOtter)
5. Plan the route from the layout, then setProgram, run, and read the session until vm.status is WON. If it is LOST, read lossReason, fix the program and try again.
6. Tell me the session id so I can watch it live at ${e}/watch/SESSION_ID, and report the winning program and its step count. Winners are ranked per map by fewest slots used (non-EMPTY slots across all tapes), then fewest steps: query { leaderboard(mapId: "MAP_ID") { entries { rank displayName slots steps } } }.`}export{O as n,C as r,k as t};