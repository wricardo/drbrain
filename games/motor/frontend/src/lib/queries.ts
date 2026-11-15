// GraphQL documents. Fields requested here must exist in graph/schema.graphqls.

const POSITION = `x y`;
const FRAME = `tape pc`;

const STEP_EVENT_FIELDS = `
  step
  instruction
  tapeIndex
  slotIndex
  from { ${POSITION} }
  to { ${POSITION} }
  facingBefore
  facingAfter
  blocked
  callBlocked
  collected { ${POSITION} }
  callStack { ${FRAME} }
  status
  lossReason
`;

const MAP_FIELDS = `
  id
  name
  description
  difficulty
  builtin
  width
  height
  layout
  start { ${POSITION} }
  startFacing
  rocks { ${POSITION} }
  treats { ${POSITION} }
  mainTapeLength
  subTapeLengths
  maxSteps
  maxCallDepth
  allowRecursion
`;

/** Full session snapshot: everything the board, tapes and counters need. */
export const SESSION_FIELDS = `
  id
  displayName
  mapId
  map { ${MAP_FIELDS} }
  program { main subs }
  vm {
    pos { ${POSITION} }
    facing
    treatsRemaining { ${POSITION} }
    callStack { ${FRAME} }
    steps
    status
    lossReason
    visited { ${POSITION} }
  }
  playing
  speedMs
  seq
  createdAt
  lastActionAt
  attempts
  bestSteps
  lastEvent { ${STEP_EVENT_FIELDS} }
`;

export const MAPS_QUERY = `
  query Maps {
    maps { ${MAP_FIELDS} }
  }
`;

export const MAP_QUERY = `
  query MapById($id: ID!) {
    map(id: $id) { ${MAP_FIELDS} }
  }
`;

export const SESSION_QUERY = `
  query Session($id: ID!) {
    session(id: $id) { ${SESSION_FIELDS} }
  }
`;

export const SESSIONS_QUERY = `
  query Sessions($limit: Int, $sort: SessionSort, $mapId: ID) {
    sessions(sort: $sort, limit: $limit, mapId: $mapId) {
      id
      displayName
      mapId
      playing
      createdAt
      lastActionAt
      map { name }
      vm { status steps treatsRemaining { ${POSITION} } }
    }
  }
`;

export const CREATE_SESSION_MUTATION = `
  mutation CreateSession($mapID: ID!, $displayName: String) {
    createSession(mapID: $mapID, displayName: $displayName) { ${SESSION_FIELDS} ownerToken }
  }
`;

export const DELETE_SESSION_MUTATION = `
  mutation DeleteSession($id: ID!) {
    deleteSession(id: $id)
  }
`;

export const RENAME_SESSION_MUTATION = `
  mutation RenameSession($id: ID!, $displayName: String!) {
    renameSession(id: $id, displayName: $displayName) { ${SESSION_FIELDS} }
  }
`;

export const SET_PROGRAM_MUTATION = `
  mutation SetProgram($sessionID: ID!, $program: ProgramInput!) {
    setProgram(sessionID: $sessionID, program: $program) { ${SESSION_FIELDS} }
  }
`;

export const RUN_MUTATION = `
  mutation Run($sessionID: ID!, $speedMs: Int) {
    run(sessionID: $sessionID, speedMs: $speedMs) { ${SESSION_FIELDS} }
  }
`;

export const PAUSE_MUTATION = `
  mutation Pause($sessionID: ID!) {
    pause(sessionID: $sessionID) { ${SESSION_FIELDS} }
  }
`;

export const STEP_MUTATION = `
  mutation Step($sessionID: ID!) {
    step(sessionID: $sessionID) { ${SESSION_FIELDS} }
  }
`;

export const RESET_MUTATION = `
  mutation Reset($sessionID: ID!) {
    reset(sessionID: $sessionID) { ${SESSION_FIELDS} }
  }
`;

export const SESSION_UPDATED_SUBSCRIPTION = `
  subscription SessionUpdated($sessionID: ID!) {
    sessionUpdated(sessionID: $sessionID) {
      seq
      session { ${SESSION_FIELDS} }
      event { ${STEP_EVENT_FIELDS} }
    }
  }
`;

export const CREATE_MAP_MUTATION = `
  mutation CreateMap($map: MapInput!) {
    createMap(map: $map) { ${MAP_FIELDS} }
  }
`;

export const UPDATE_MAP_MUTATION = `
  mutation UpdateMap($map: MapInput!) {
    updateMap(map: $map) { ${MAP_FIELDS} }
  }
`;

export const DELETE_MAP_MUTATION = `
  mutation DeleteMap($id: ID!) {
    deleteMap(id: $id)
  }
`;

export const VALIDATE_MAP_MUTATION = `
  mutation ValidateMap($map: MapInput!) {
    validateMap(map: $map) {
      valid
      issues { message }
      unreachableTreats { ${POSITION} }
    }
  }
`;

const LEADERBOARD_ENTRY_FIELDS = `rank sessionId displayName handle mine slots steps achievedAt`;

/** Keep in sync with `leaderboardQ` in api/leaderboard_test.go (complexity check). */
export const LEADERBOARD_QUERY = `
  query Leaderboard($mapId: ID!, $order: LeaderboardOrder, $limit: Int, $sessionId: ID) {
    leaderboard(mapId: $mapId, order: $order, limit: $limit, sessionId: $sessionId) {
      mapId
      order
      total
      entries { ${LEADERBOARD_ENTRY_FIELDS} }
      yours { ${LEADERBOARD_ENTRY_FIELDS} }
    }
  }
`;

/** Keep in sync with `playerRunsQ` in api/leaderboard_test.go (complexity check). Give exactly one of sessionIds or name. */
export const PLAYER_RUNS_QUERY = `
  query PlayerRuns($sessionIds: [ID!], $name: String) {
    playerRuns(sessionIds: $sessionIds, name: $name) {
      mapId
      total
      entry { ${LEADERBOARD_ENTRY_FIELDS} }
    }
  }
`;

const ACCOUNT_FIELDS = `handle displayName profilePublic createdAt handleChangeableAt`;

/** Keep in sync with `accountQ` in api/hardening_test.go (complexity check). */
export const ACCOUNT_QUERY = `
  query Account {
    me { ${ACCOUNT_FIELDS} }
    authProviders { id name }
  }
`;

/** Keep in sync with `profileQ` in api/hardening_test.go (complexity check). */
export const PROFILE_QUERY = `
  query Profile($handle: String!) {
    profile(handle: $handle) {
      handle
      displayName
      createdAt
      isMine
      runs { mapId total entry { ${LEADERBOARD_ENTRY_FIELDS} } }
    }
  }
`;

export const LOGOUT_MUTATION = `
  mutation Logout {
    logout
  }
`;

export const UPDATE_ACCOUNT_MUTATION = `
  mutation UpdateAccount($input: AccountInput!) {
    updateAccount(input: $input) { ${ACCOUNT_FIELDS} }
  }
`;

export const DELETE_ACCOUNT_MUTATION = `
  mutation DeleteAccount($confirmHandle: String!) {
    deleteAccount(confirmHandle: $confirmHandle)
  }
`;

export const CLAIM_SESSIONS_MUTATION = `
  mutation ClaimSessions($claims: [SessionClaimInput!]!) {
    claimSessions(claims: $claims) { sessionID status }
  }
`;
