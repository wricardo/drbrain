// GraphQL documents. Fields requested here must exist in graph/schema.graphqls.
// Every selection is wrapped in the `music` namespace; graphql.ts unwraps it.

const NOTE_FIELDS = `
  at
  dur
  pitch
`;

const MEASURE_FIELDS = `
  id
  len
  notes { ${NOTE_FIELDS} }
`;

/** An excerpt: the target order, its flattened recording and display metadata. */
export const EXCERPT_FIELDS = `
  id
  title
  composer
  tempoBPM
  timeSig
  key
  clef
  axis
  measures { ${MEASURE_FIELDS} }
  notes { ${NOTE_FIELDS} }
`;

/** Full session snapshot: the arrangement, counters and the frozen excerpt. */
export const SESSION_FIELDS = `
  id
  displayName
  difficulty
  excerpt { ${EXCERPT_FIELDS} }
  slots { position measureID flipH flipV }
  solved
  seq
  wins
  percentPerWin
  createdAt
  lastActionAt
  lastChange { kind a b }
`;

export const EXCERPTS_QUERY = `
  query MusicExcerpts {
    music { excerpts { ${EXCERPT_FIELDS} } }
  }
`;

export const EXCERPT_QUERY = `
  query MusicExcerpt($id: ID!) {
    music { excerpt(id: $id) { ${EXCERPT_FIELDS} } }
  }
`;

export const SESSION_QUERY = `
  query MusicSession($id: ID!) {
    music { session(id: $id) { ${SESSION_FIELDS} } }
  }
`;

export const CREATE_SESSION_MUTATION = `
  mutation MusicCreateSession($excerptID: ID!, $difficulty: Music_Difficulty!, $displayName: String, $seed: Int) {
    music { createSession(excerptID: $excerptID, difficulty: $difficulty, displayName: $displayName, seed: $seed) { ${SESSION_FIELDS} } }
  }
`;

export const SWITCH_MEASURES_MUTATION = `
  mutation MusicSwitchMeasures($sessionID: ID!, $a: Int!, $b: Int!) {
    music { switchMeasures(sessionID: $sessionID, a: $a, b: $b) { ${SESSION_FIELDS} } }
  }
`;

export const TRANSFORM_MEASURE_MUTATION = `
  mutation MusicTransformMeasure($sessionID: ID!, $position: Int!, $flipH: Boolean!, $flipV: Boolean!) {
    music { transformMeasure(sessionID: $sessionID, position: $position, flipH: $flipH, flipV: $flipV) { ${SESSION_FIELDS} } }
  }
`;

export const SET_ARRANGEMENT_MUTATION = `
  mutation MusicSetArrangement($sessionID: ID!, $slots: [Music_PlacementInput!]!) {
    music { setArrangement(sessionID: $sessionID, slots: $slots) { ${SESSION_FIELDS} } }
  }
`;

export const RESET_ARRANGEMENT_MUTATION = `
  mutation MusicResetArrangement($sessionID: ID!) {
    music { resetArrangement(sessionID: $sessionID) { ${SESSION_FIELDS} } }
  }
`;

/** Flat subscription root; graphql.ts maps it back to `sessionUpdated`. */
export const SESSION_UPDATED_SUBSCRIPTION = `
  subscription MusicSessionUpdated($sessionID: ID!) {
    musicSessionUpdated(sessionID: $sessionID) {
      session { ${SESSION_FIELDS} }
      lastChange { kind a b }
    }
  }
`;
