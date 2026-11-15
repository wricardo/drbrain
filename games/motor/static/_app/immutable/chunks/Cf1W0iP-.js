import{at as e,ct as t}from"./qFQH_apL.js";import"./CU6ONQUM.js";var n=`$$_urql`,r=()=>e(n),i=e=>{t(n,e)},a=`x y`,o=`tape pc`,s=`
  step
  instruction
  tapeIndex
  slotIndex
  from { ${a} }
  to { ${a} }
  facingBefore
  facingAfter
  blocked
  callBlocked
  collected { ${a} }
  callStack { ${o} }
  status
  lossReason
`,c=`
  id
  name
  description
  difficulty
  builtin
  width
  height
  layout
  start { ${a} }
  startFacing
  rocks { ${a} }
  treats { ${a} }
  mainTapeLength
  subTapeLengths
  maxSteps
  maxCallDepth
  allowRecursion
`,l=`
  id
  displayName
  mapId
  map { ${c} }
  program { main subs }
  vm {
    pos { ${a} }
    facing
    treatsRemaining { ${a} }
    callStack { ${o} }
    steps
    status
    lossReason
    visited { ${a} }
  }
  playing
  speedMs
  seq
  createdAt
  lastActionAt
  attempts
  bestSteps
  lastEvent { ${s} }
`,u=`
  query Maps {
    maps { ${c} }
  }
`,d=`
  query MapById($id: ID!) {
    map(id: $id) { ${c} }
  }
`,f=`
  query Session($id: ID!) {
    session(id: $id) { ${l} }
  }
`,p=`
  query Sessions($limit: Int, $sort: SessionSort, $mapId: ID) {
    sessions(sort: $sort, limit: $limit, mapId: $mapId) {
      id
      displayName
      mapId
      playing
      createdAt
      lastActionAt
      map { name }
      vm { status steps treatsRemaining { ${a} } }
    }
  }
`,m=`
  mutation CreateSession($mapID: ID!, $displayName: String) {
    createSession(mapID: $mapID, displayName: $displayName) { ${l} ownerToken }
  }
`;`${l}`;var h=`
  mutation SetProgram($sessionID: ID!, $program: ProgramInput!) {
    setProgram(sessionID: $sessionID, program: $program) { ${l} }
  }
`,g=`
  mutation Run($sessionID: ID!, $speedMs: Int) {
    run(sessionID: $sessionID, speedMs: $speedMs) { ${l} }
  }
`,_=`
  mutation Pause($sessionID: ID!) {
    pause(sessionID: $sessionID) { ${l} }
  }
`,v=`
  mutation Step($sessionID: ID!) {
    step(sessionID: $sessionID) { ${l} }
  }
`,y=`
  mutation Reset($sessionID: ID!) {
    reset(sessionID: $sessionID) { ${l} }
  }
`,b=`
  subscription SessionUpdated($sessionID: ID!) {
    sessionUpdated(sessionID: $sessionID) {
      seq
      session { ${l} }
      event { ${s} }
    }
  }
`,x=`
  mutation CreateMap($map: MapInput!) {
    createMap(map: $map) { ${c} }
  }
`,S=`
  mutation UpdateMap($map: MapInput!) {
    updateMap(map: $map) { ${c} }
  }
`,C=`
  mutation DeleteMap($id: ID!) {
    deleteMap(id: $id)
  }
`,w=`
  mutation ValidateMap($map: MapInput!) {
    validateMap(map: $map) {
      valid
      issues { message }
      unreachableTreats { ${a} }
    }
  }
`,T=`rank sessionId displayName handle mine slots steps achievedAt`,E=`
  query Leaderboard($mapId: ID!, $order: LeaderboardOrder, $limit: Int, $sessionId: ID) {
    leaderboard(mapId: $mapId, order: $order, limit: $limit, sessionId: $sessionId) {
      mapId
      order
      total
      entries { ${T} }
      yours { ${T} }
    }
  }
`,D=`
  query PlayerRuns($sessionIds: [ID!], $name: String) {
    playerRuns(sessionIds: $sessionIds, name: $name) {
      mapId
      total
      entry { ${T} }
    }
  }
`,O=`handle displayName profilePublic createdAt handleChangeableAt hasPassword`,k=`
  query Account {
    me { ${O} }
    authProviders { id name }
  }
`,A=`
  query Profile($handle: String!) {
    profile(handle: $handle) {
      handle
      displayName
      createdAt
      isMine
      runs { mapId total entry { ${T} } }
    }
  }
`,j=`
  mutation Logout {
    logout
  }
`,M=`
  mutation UpdateAccount($input: AccountInput!) {
    updateAccount(input: $input) { ${O} }
  }
`,N=`
  mutation ChangePassword($currentPassword: String!, $newPassword: String!) {
    changePassword(currentPassword: $currentPassword, newPassword: $newPassword)
  }
`,P=`
  mutation DeleteAccount($confirmHandle: String!) {
    deleteAccount(confirmHandle: $confirmHandle)
  }
`,F=`
  mutation ClaimSessions($claims: [SessionClaimInput!]!) {
    claimSessions(claims: $claims) { sessionID status }
  }
`,I={NOT_FOUND:`Not found`,INVALID_PROGRAM:`Invalid program`,SESSION_PLAYING:`Session is running — pause it first`,SESSION_TERMINAL:`Run is over — reset to try again`,INVALID_ARGUMENT:`Invalid input`,FORBIDDEN:`Not allowed`};function L(e){let t=e.find(e=>e&&(e.message||e.extensions?.code));if(!t)return null;let n=typeof t.extensions?.code==`string`?t.extensions.code:null,r=t.message??``;return{code:n,title:n&&I[n]||`Request failed`,message:r}}function R(e){if(Array.isArray(e)){let t=L(e);if(t)return t}else if(e&&typeof e==`object`){let t=e;if(t.graphQLErrors?.length){let e=L(t.graphQLErrors);if(e)return e}if(t.networkError)return{code:null,title:`Cannot reach the server`,message:t.networkError.message??``};if(t.message)return{code:null,title:`Request failed`,message:t.message}}return{code:null,title:`Request failed`,message:typeof e==`string`?e:``}}function z(e){switch(e){case`STEP_LIMIT`:return`The robot ran out of steps before collecting every treat.`;case`CALL_DEPTH`:return`Subroutine calls nested too deep (call depth limit exceeded).`;case`PROGRAM_ENDED`:return`The program finished with treats still left on the board.`;default:return`The run ended without collecting every treat.`}}export{v as C,r as D,w as E,i as O,h as S,S as T,y as _,F as a,f as b,P as c,j as d,u as f,A as g,D as h,N as i,C as l,_ as m,z as n,x as o,d as p,k as r,m as s,R as t,E as u,g as v,M as w,b as x,p as y};