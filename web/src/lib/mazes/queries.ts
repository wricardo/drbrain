// GraphQL documents. Fields requested here must exist in graph/schema.graphqls.

const POSITION = `level x y`;

const MOVE_EVENT_FIELDS = `
  move
  direction
  from { ${POSITION} }
  stepped { ${POSITION} }
  to { ${POSITION} }
  blocked
  via
  status
  lossReason
`;

const PORTAL_FIELDS = `glyph a { ${POSITION} } b { ${POSITION} }`;

/** Public catalog fields: nothing beyond the start level. */
const MAP_FIELDS = `
  id
  name
  description
  difficulty
  width
  height
  levelCount
  preview
  maxMoves
  parMoves
  minLevelChanges
  forcedDescents
  status
  ownedByMe
`;

/** Full layout, admin only (editor). */
const ADMIN_MAP_FIELDS = `
  id
  name
  description
  difficulty
  width
  height
  levelCount
  levels
  start { ${POSITION} }
  finish { ${POSITION} }
  portals { ${PORTAL_FIELDS} }
  maxMoves
  parMoves
  minLevelChanges
  forcedDescents
  status
  ownedByMe
`;

/** Full session snapshot: everything the board, tower and counters need. */
export const SESSION_FIELDS = `
  id
  displayName
  mapId
  map { ${MAP_FIELDS} }
  view { level levelCount rows portals { ${PORTAL_FIELDS} } }
  state {
    pos { ${POSITION} }
    moves
    status
    lossReason
    path { ${POSITION} }
  }
  seq
  createdAt
  lastActionAt
  attempts
  bestMoves
  lastEvent { ${MOVE_EVENT_FIELDS} }
`;

export const MAPS_QUERY = `
  query Maps {
    mazes { maps { ${MAP_FIELDS} } }
  }
`;

export const MAP_QUERY = `
  query MapById($id: ID!) {
    mazes { map(id: $id) { ${MAP_FIELDS} } }
  }
`;

export const ADMIN_MAP_QUERY = `
  query AdminMapById($id: ID!) {
    mazes { adminMap(id: $id) { ${ADMIN_MAP_FIELDS} } }
  }
`;

export const SESSION_QUERY = `
  query Session($id: ID!) {
    mazes { session(id: $id) { ${SESSION_FIELDS} } }
  }
`;

export const SESSIONS_QUERY = `
  query Sessions($limit: Int, $sort: Mazes_SessionSort, $mapId: ID) {
    mazes {
      sessions(sort: $sort, limit: $limit, mapId: $mapId) {
        id
        displayName
        mapId
        createdAt
        lastActionAt
        map { name parMoves maxMoves }
        state { status moves }
      }
    }
  }
`;

export const CREATE_SESSION_MUTATION = `
  mutation CreateSession($mapID: ID!, $displayName: String) {
    mazes { createSession(mapID: $mapID, displayName: $displayName) { session { ${SESSION_FIELDS} } ownerToken } }
  }
`;

export const CAN_PLAY_QUERY = `
  query CanPlaySession($id: ID!, $ownerToken: String) {
    mazes { canPlaySession(id: $id, ownerToken: $ownerToken) }
  }
`;

export const DELETE_SESSION_MUTATION = `
  mutation DeleteSession($id: ID!, $ownerToken: String) {
    mazes { deleteSession(id: $id, ownerToken: $ownerToken) }
  }
`;

export const RENAME_SESSION_MUTATION = `
  mutation RenameSession($id: ID!, $displayName: String!, $ownerToken: String) {
    mazes { renameSession(id: $id, displayName: $displayName, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const MOVE_MUTATION = `
  mutation Move($sessionID: ID!, $direction: Mazes_Direction!, $ownerToken: String) {
    mazes { move(sessionID: $sessionID, direction: $direction, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const BULK_MOVE_MUTATION = `
  mutation BulkMove($sessionID: ID!, $directions: [Mazes_Direction!]!, $ownerToken: String) {
    mazes { bulkMove(sessionID: $sessionID, directions: $directions, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const RESET_MUTATION = `
  mutation Reset($sessionID: ID!, $ownerToken: String) {
    mazes { reset(sessionID: $sessionID, ownerToken: $ownerToken) { ${SESSION_FIELDS} } }
  }
`;

export const SESSION_UPDATED_SUBSCRIPTION = `
  subscription SessionUpdated($sessionID: ID!) {
    mazesSessionUpdated(sessionID: $sessionID) {
      seq
      session { ${SESSION_FIELDS} }
      event { ${MOVE_EVENT_FIELDS} }
    }
  }
`;

export const CREATE_MAP_MUTATION = `
  mutation CreateMap($map: Mazes_MapInput!) {
    mazes { createMap(map: $map) { ${ADMIN_MAP_FIELDS} } }
  }
`;

export const UPDATE_MAP_MUTATION = `
  mutation UpdateMap($map: Mazes_MapInput!) {
    mazes { updateMap(map: $map) { ${ADMIN_MAP_FIELDS} } }
  }
`;

export const PUBLISH_MAP_MUTATION = `
  mutation PublishMap($id: ID!) {
    mazes { publishMap(id: $id) { ${ADMIN_MAP_FIELDS} } }
  }
`;

export const DELETE_MAP_MUTATION = `
  mutation DeleteMap($id: ID!) {
    mazes { deleteMap(id: $id) }
  }
`;

export const VALIDATE_MAP_MUTATION = `
  mutation ValidateMap($map: Mazes_MapInput!) {
    mazes {
      validateMap(map: $map) {
        valid
        issues { message }
        finishReachable
        parMoves
        minLevelChanges
        forcedDescents
      }
    }
  }
`;
