import{at as e,ct as t}from"./qFQH_apL.js";import"./CU6ONQUM.js";var n=`$$_urql`,r=()=>e(n),i=e=>{t(n,e)},a={NOT_FOUND:`Not found`,INVALID_PROGRAM:`Invalid program`,SESSION_PLAYING:`Session is running — pause it first`,SESSION_TERMINAL:`Run is over — reset to try again`,INVALID_ARGUMENT:`Invalid input`,FORBIDDEN:`Not allowed`};function o(e){let t=e.find(e=>e&&(e.message||e.extensions?.code));if(!t)return null;let n=typeof t.extensions?.code==`string`?t.extensions.code:null,r=t.message??``;return{code:n,title:n&&a[n]||`Request failed`,message:r}}function s(e){if(Array.isArray(e)){let t=o(e);if(t)return t}else if(e&&typeof e==`object`){let t=e;if(t.graphQLErrors?.length){let e=o(t.graphQLErrors);if(e)return e}if(t.networkError)return{code:null,title:`Cannot reach the server`,message:t.networkError.message??``};if(t.message)return{code:null,title:`Request failed`,message:t.message}}return{code:null,title:`Request failed`,message:typeof e==`string`?e:``}}function c(e){switch(e){case`STEP_LIMIT`:return`The robot ran out of steps before collecting every treat.`;case`CALL_DEPTH`:return`Subroutine calls nested too deep (call depth limit exceeded).`;case`PROGRAM_ENDED`:return`The program finished with treats still left on the board.`;default:return`The run ended without collecting every treat.`}}var l=`x y`,u=`tape pc`,d=`
  step
  instruction
  tapeIndex
  slotIndex
  from { ${l} }
  to { ${l} }
  facingBefore
  facingAfter
  blocked
  callBlocked
  collected { ${l} }
  callStack { ${u} }
  status
  lossReason
`,f=`
  id
  name
  description
  difficulty
  builtin
  width
  height
  layout
  start { ${l} }
  startFacing
  rocks { ${l} }
  treats { ${l} }
  mainTapeLength
  subTapeLengths
  maxSteps
  maxCallDepth
  allowRecursion
`,p=`
  id
  displayName
  mapId
  map { ${f} }
  program { main subs }
  vm {
    pos { ${l} }
    facing
    treatsRemaining { ${l} }
    callStack { ${u} }
    steps
    status
    lossReason
    visited { ${l} }
  }
  playing
  speedMs
  seq
  createdAt
  lastActionAt
  attempts
  bestSteps
  lastEvent { ${d} }
`,m=`
  query Maps {
    maps { ${f} }
  }
`,h=`
  query MapById($id: ID!) {
    map(id: $id) { ${f} }
  }
`,g=`
  query Session($id: ID!) {
    session(id: $id) { ${p} }
  }
`,_=`
  query Sessions($limit: Int, $sort: SessionSort, $mapId: ID) {
    sessions(sort: $sort, limit: $limit, mapId: $mapId) {
      id
      displayName
      mapId
      playing
      createdAt
      lastActionAt
      map { name }
      vm { status steps treatsRemaining { ${l} } }
    }
  }
`,v=`
  mutation CreateSession($mapID: ID!, $displayName: String) {
    createSession(mapID: $mapID, displayName: $displayName) { ${p} ownerToken }
  }
`,y=`
  mutation RenameSession($id: ID!, $displayName: String!) {
    renameSession(id: $id, displayName: $displayName) { ${p} }
  }
`,b=`
  mutation SetProgram($sessionID: ID!, $program: ProgramInput!, $ownerToken: String) {
    setProgram(sessionID: $sessionID, program: $program, ownerToken: $ownerToken) { ${p} }
  }
`,x=`
  mutation Run($sessionID: ID!, $speedMs: Int, $ownerToken: String) {
    run(sessionID: $sessionID, speedMs: $speedMs, ownerToken: $ownerToken) { ${p} }
  }
`,S=`
  mutation Pause($sessionID: ID!, $ownerToken: String) {
    pause(sessionID: $sessionID, ownerToken: $ownerToken) { ${p} }
  }
`,C=`
  mutation Step($sessionID: ID!, $ownerToken: String) {
    step(sessionID: $sessionID, ownerToken: $ownerToken) { ${p} }
  }
`,w=`
  mutation Reset($sessionID: ID!, $ownerToken: String) {
    reset(sessionID: $sessionID, ownerToken: $ownerToken) { ${p} }
  }
`,T=`
  query CanPlaySession($id: ID!, $ownerToken: String) {
    canPlaySession(id: $id, ownerToken: $ownerToken)
  }
`,E=`
  subscription SessionUpdated($sessionID: ID!) {
    sessionUpdated(sessionID: $sessionID) {
      seq
      session { ${p} }
      event { ${d} }
    }
  }
`,D=`
  mutation CreateMap($map: MapInput!) {
    createMap(map: $map) { ${f} }
  }
`,O=`
  mutation UpdateMap($map: MapInput!) {
    updateMap(map: $map) { ${f} }
  }
`,k=`
  mutation DeleteMap($id: ID!) {
    deleteMap(id: $id)
  }
`,A=`
  mutation ValidateMap($map: MapInput!) {
    validateMap(map: $map) {
      valid
      issues { message }
      unreachableTreats { ${l} }
    }
  }
`,j=`rank sessionId displayName handle mine slots steps achievedAt`,M=`
  query Leaderboard($mapId: ID!, $order: LeaderboardOrder, $limit: Int, $sessionId: ID) {
    leaderboard(mapId: $mapId, order: $order, limit: $limit, sessionId: $sessionId) {
      mapId
      order
      total
      entries { ${j} }
      yours { ${j} }
    }
  }
`,N=`
  query PlayerRuns($sessionIds: [ID!], $name: String) {
    playerRuns(sessionIds: $sessionIds, name: $name) {
      mapId
      total
      entry { ${j} }
    }
  }
`,P=`handle displayName profilePublic createdAt handleChangeableAt hasPassword`,F=`
  query Account {
    me { ${P} }
    authProviders { id name }
  }
`,I=`
  query Profile($handle: String!) {
    profile(handle: $handle) {
      handle
      displayName
      createdAt
      isMine
      runs { mapId total entry { ${j} } }
    }
  }
`,L=`
  mutation Logout {
    logout
  }
`,R=`
  mutation RegisterWithPassword($handle: String!, $password: String!) {
    registerWithPassword(handle: $handle, password: $password) { ${P} }
  }
`,z=`
  mutation LogInWithPassword($handle: String!, $password: String!) {
    logInWithPassword(handle: $handle, password: $password) { ${P} }
  }
`,B=`
  mutation UpdateAccount($input: AccountInput!) {
    updateAccount(input: $input) { ${P} }
  }
`,V=`
  mutation ChangePassword($currentPassword: String!, $newPassword: String!) {
    changePassword(currentPassword: $currentPassword, newPassword: $newPassword)
  }
`,H=`
  mutation DeleteAccount($confirmHandle: String!) {
    deleteAccount(confirmHandle: $confirmHandle)
  }
`,U=`
  mutation ClaimSessions($claims: [SessionClaimInput!]!) {
    claimSessions(claims: $claims) { sessionID status }
  }
`;export{c as A,E as C,O as D,B as E,i as M,A as O,g as S,C as T,R as _,D as a,x as b,k as c,z as d,m as f,I as g,N as h,U as i,r as j,s as k,M as l,S as m,T as n,v as o,h as p,V as r,H as s,F as t,L as u,y as v,b as w,_ as x,w as y};