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
  pushed
  pushBlocked
  mirrored
  collectedKey
  teleported { ${POSITION} }
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
  status
  ownedByMe
  width
  height
  layout
  start { ${POSITION} }
  startFacing
  rocks { ${POSITION} }
  treats { ${POSITION} }
  conveyors { pos { ${POSITION} } dir }
  mirrors { pos { ${POSITION} } slash }
  keys { pos { ${POSITION} } letter }
  doors { pos { ${POSITION} } letter }
  portals { pos { ${POSITION} } pair }
  mainTapeLength
  subTapeLengths
  maxSteps
  maxCallDepth
  allowRecursion
`;

/** Full session snapshot: everything the board, tapes and counters need. */
export const SESSION_FIELDS = `
  id
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
    openDoors
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
    motorProgramming { maps { ${MAP_FIELDS} } }
  }
`;

export const MAP_QUERY = `
  query MapById($id: ID!) {
    motorProgramming { map(id: $id) { ${MAP_FIELDS} } }
  }
`;

export const SESSION_QUERY = `
  query Session($id: ID!, $ownerToken: String) {
    motorProgramming { session(id: $id, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const SESSIONS_QUERY = `
  query Sessions($limit: Int, $sort: Motor_SessionSort, $mapId: ID) {
    motorProgramming {
      sessions(sort: $sort, limit: $limit, mapId: $mapId) {
        id
        mapId
        playing
        createdAt
        lastActionAt
        map { name }
        vm { status steps treatsRemaining { ${POSITION} } }
      }
    }
  }
`;

export const CREATE_SESSION_MUTATION = `
  mutation CreateSession($mapID: ID!) {
    motorProgramming { createSession(mapID: $mapID) { ${SESSION_FIELDS} ownerToken } }
  }
`;

export const DELETE_SESSION_MUTATION = `
  mutation DeleteSession($id: ID!) {
    motorProgramming { deleteSession(id: $id) }
  }
`;

export const SET_PROGRAM_MUTATION = `
  mutation SetProgram($sessionID: ID!, $program: Motor_ProgramInput!, $ownerToken: String) {
    motorProgramming { setProgram(sessionID: $sessionID, program: $program, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const RUN_MUTATION = `
  mutation Run($sessionID: ID!, $speedMs: Int, $ownerToken: String) {
    motorProgramming { run(sessionID: $sessionID, speedMs: $speedMs, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const PAUSE_MUTATION = `
  mutation Pause($sessionID: ID!, $ownerToken: String) {
    motorProgramming { pause(sessionID: $sessionID, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const STEP_MUTATION = `
  mutation Step($sessionID: ID!, $ownerToken: String) {
    motorProgramming { step(sessionID: $sessionID, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const RESET_MUTATION = `
  mutation Reset($sessionID: ID!, $ownerToken: String) {
    motorProgramming { reset(sessionID: $sessionID, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

/** Whether this browser may change the session's program (it created it, or has its owner token) and so read it. */
export const CAN_PLAY_SESSION_QUERY = `
  query CanPlaySession($id: ID!, $ownerToken: String) {
    motorProgramming { canPlaySession(id: $id, ownerToken: $ownerToken) }
  }
`;

export const SESSION_UPDATED_SUBSCRIPTION = `
  subscription SessionUpdated($sessionID: ID!, $ownerToken: String) {
    motorProgrammingSessionUpdated(sessionID: $sessionID, ownerToken: $ownerToken) {
      seq
      session { ${SESSION_FIELDS} }
      event { ${STEP_EVENT_FIELDS} }
    }
  }
`;

export const CREATE_MAP_MUTATION = `
  mutation CreateMap($map: Motor_MapInput!) {
    motorProgramming { createMap(map: $map) { ${MAP_FIELDS} } }
  }
`;

export const UPDATE_MAP_MUTATION = `
  mutation UpdateMap($map: Motor_MapInput!) {
    motorProgramming { updateMap(map: $map) { ${MAP_FIELDS} } }
  }
`;

export const PUBLISH_MAP_MUTATION = `
  mutation PublishMap($id: ID!) {
    motorProgramming { publishMap(id: $id) { ${MAP_FIELDS} } }
  }
`;

export const DELETE_MAP_MUTATION = `
  mutation DeleteMap($id: ID!) {
    motorProgramming { deleteMap(id: $id) }
  }
`;

export const VALIDATE_MAP_MUTATION = `
  mutation ValidateMap($map: Motor_MapInput!) {
    motorProgramming {
      validateMap(map: $map) {
        valid
        issues { message }
        unreachableTreats { ${POSITION} }
      }
    }
  }
`;

const LEADERBOARD_ENTRY_FIELDS = `rank sessionId displayName handle mine slots steps achievedAt`;

/** Keep in sync with `leaderboardQ` in api/leaderboard_test.go (complexity check). */
export const LEADERBOARD_QUERY = `
  query Leaderboard($mapId: ID!, $order: Motor_LeaderboardOrder, $limit: Int, $sessionId: ID) {
    motorProgramming {
      leaderboard(mapId: $mapId, order: $order, limit: $limit, sessionId: $sessionId) {
        mapId
        order
        total
        entries { ${LEADERBOARD_ENTRY_FIELDS} }
        yours { ${LEADERBOARD_ENTRY_FIELDS} }
      }
    }
  }
`;

/** Keep in sync with `playerRunsQ` in api/leaderboard_test.go (complexity check). */
export const PLAYER_RUNS_QUERY = `
  query PlayerRuns($sessionIds: [ID!]) {
    motorProgramming {
      playerRuns(sessionIds: $sessionIds) {
        mapId
        total
        entry { ${LEADERBOARD_ENTRY_FIELDS} }
      }
    }
  }
`;

export const PLAYER_RUNS_FOR_QUERY = `
  query PlayerRunsFor($handle: String!) {
    motorProgramming {
      playerRunsFor(handle: $handle) {
        mapId
        total
        entry { ${LEADERBOARD_ENTRY_FIELDS} }
      }
    }
  }
`;

export const CLAIM_SESSIONS_MUTATION = `
  mutation ClaimSessions($claims: [Motor_SessionClaimInput!]!) {
    motorProgramming { claimSessions(claims: $claims) { sessionID status } }
  }
`;
