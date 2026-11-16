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
`,h=`
  mutation RenameSession($id: ID!, $displayName: String!) {
    renameSession(id: $id, displayName: $displayName) { ${l} }
  }
`,g=`
  mutation SetProgram($sessionID: ID!, $program: ProgramInput!) {
    setProgram(sessionID: $sessionID, program: $program) { ${l} }
  }
`,_=`
  mutation Run($sessionID: ID!, $speedMs: Int) {
    run(sessionID: $sessionID, speedMs: $speedMs) { ${l} }
  }
`,v=`
  mutation Pause($sessionID: ID!) {
    pause(sessionID: $sessionID) { ${l} }
  }
`,y=`
  mutation Step($sessionID: ID!) {
    step(sessionID: $sessionID) { ${l} }
  }
`,b=`
  mutation Reset($sessionID: ID!) {
    reset(sessionID: $sessionID) { ${l} }
  }
`,x=`
  subscription SessionUpdated($sessionID: ID!) {
    sessionUpdated(sessionID: $sessionID) {
      seq
      session { ${l} }
      event { ${s} }
    }
  }
`,S=`
  mutation CreateMap($map: MapInput!) {
    createMap(map: $map) { ${c} }
  }
`,C=`
  mutation UpdateMap($map: MapInput!) {
    updateMap(map: $map) { ${c} }
  }
`,w=`
  mutation DeleteMap($id: ID!) {
    deleteMap(id: $id)
  }
`,T=`
  mutation ValidateMap($map: MapInput!) {
    validateMap(map: $map) {
      valid
      issues { message }
      unreachableTreats { ${a} }
    }
  }
`,E=`rank sessionId displayName handle mine slots steps achievedAt`,D=`
  query Leaderboard($mapId: ID!, $order: LeaderboardOrder, $limit: Int, $sessionId: ID) {
    leaderboard(mapId: $mapId, order: $order, limit: $limit, sessionId: $sessionId) {
      mapId
      order
      total
      entries { ${E} }
      yours { ${E} }
    }
  }
`,O=`
  query PlayerRuns($sessionIds: [ID!], $name: String) {
    playerRuns(sessionIds: $sessionIds, name: $name) {
      mapId
      total
      entry { ${E} }
    }
  }
`,k=`handle displayName profilePublic createdAt handleChangeableAt hasPassword`,A=`
  query Account {
    me { ${k} }
    authProviders { id name }
  }
`,j=`
  query Profile($handle: String!) {
    profile(handle: $handle) {
      handle
      displayName
      createdAt
      isMine
      runs { mapId total entry { ${E} } }
    }
  }
`,M=`
  mutation Logout {
    logout
  }
`,N=`
  mutation UpdateAccount($input: AccountInput!) {
    updateAccount(input: $input) { ${k} }
  }
`,P=`
  mutation ChangePassword($currentPassword: String!, $newPassword: String!) {
    changePassword(currentPassword: $currentPassword, newPassword: $newPassword)
  }
`,F=`
  mutation DeleteAccount($confirmHandle: String!) {
    deleteAccount(confirmHandle: $confirmHandle)
  }
`,I=`
  mutation ClaimSessions($claims: [SessionClaimInput!]!) {
    claimSessions(claims: $claims) { sessionID status }
  }
`,L={NOT_FOUND:`Not found`,INVALID_PROGRAM:`Invalid program`,SESSION_PLAYING:`Session is running — pause it first`,SESSION_TERMINAL:`Run is over — reset to try again`,INVALID_ARGUMENT:`Invalid input`,FORBIDDEN:`Not allowed`};function R(e){let t=e.find(e=>e&&(e.message||e.extensions?.code));if(!t)return null;let n=typeof t.extensions?.code==`string`?t.extensions.code:null,r=t.message??``;return{code:n,title:n&&L[n]||`Request failed`,message:r}}function z(e){if(Array.isArray(e)){let t=R(e);if(t)return t}else if(e&&typeof e==`object`){let t=e;if(t.graphQLErrors?.length){let e=R(t.graphQLErrors);if(e)return e}if(t.networkError)return{code:null,title:`Cannot reach the server`,message:t.networkError.message??``};if(t.message)return{code:null,title:`Request failed`,message:t.message}}return{code:null,title:`Request failed`,message:typeof e==`string`?e:``}}function B(e){switch(e){case`STEP_LIMIT`:return`The robot ran out of steps before collecting every treat.`;case`CALL_DEPTH`:return`Subroutine calls nested too deep (call depth limit exceeded).`;case`PROGRAM_ENDED`:return`The program finished with treats still left on the board.`;default:return`The run ended without collecting every treat.`}}export{g as C,T as D,C as E,r as O,x as S,N as T,h as _,I as a,p as b,F as c,M as d,u as f,j as g,O as h,P as i,i as k,w as l,v as m,B as n,S as o,d as p,A as r,m as s,z as t,D as u,b as v,y as w,f as x,_ as y};