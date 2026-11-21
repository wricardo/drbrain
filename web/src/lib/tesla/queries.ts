export const UPDATE_SESSION_MUTATION = `
  mutation UpdateSession($id: ID!, $displayName: String!) {
    tesla {
      updateSession(id: $id, displayName: $displayName) {
        id
        displayName
        mapName
      }
    }
  }
`;

export const SESSIONS_QUERY = `
  query Sessions {
    tesla {
      sessions(sort: ACTION, order: DESC) {
        sessions {
          id
          displayName
          mapName
          lastActionAt
          gameState {
            battery
            maxBattery
            score
            victory
            gameOver
            totalMoves
            resetCount
            fogEnabled
            fogRadius
            playerPos { x y }
            nearbyGrid { type visited id allowedDirections }
          }
        }
      }
    }
  }
`;

export const LOBBY_SUBSCRIPTION = `
  subscription LobbyUpdated {
    teslaLobbyUpdated {
      mapName
      battery
      maxBattery
      score
      victory
      gameOver
      totalMoves
      resetCount
      playerPos { x y }
    }
  }
`;

export const SESSION_SUBSCRIPTION = `
  subscription SessionUpdated($sessionID: ID!) {
    teslaSessionUpdated(sessionID: $sessionID) {
      battery
      maxBattery
      score
      victory
      gameOver
      totalMoves
      resetCount
      message
      mapName
      playerPos { x y }
      nearbyGrid { type visited id allowedDirections }
    }
  }
`;

export const MAPS_QUERY = `
  query Maps {
    tesla {
      maps {
        mapId
        name
        description
        gridSize
        maxBattery
        builtin
        status
        ownedByMe
      }
    }
  }
`;

export const MAP_QUERY = `
  query Map($name: String!, $password: String) {
    tesla {
      map(name: $name, password: $password) {
        name
        description
        gridSize
        maxBattery
        startingBattery
        builtin
        status
        ownedByMe
        layout
        legend { key value }
        cellConfigs { key type allowedDirections }
      }
    }
  }
`;

export const PUBLISH_MAP_MUTATION = `
  mutation PublishMap($name: String!) {
    tesla {
      publishMap(name: $name) {
        name
        description
        gridSize
        maxBattery
        startingBattery
        builtin
        status
        ownedByMe
      }
    }
  }
`;

export const CLAIM_MAP_MUTATION = `
  mutation ClaimMap($name: String!, $editKey: String!) {
    tesla {
      claimMap(name: $name, editKey: $editKey) {
        name
        description
        gridSize
        maxBattery
        startingBattery
        builtin
        status
        ownedByMe
      }
    }
  }
`;

export const CREATE_SESSION_MUTATION = `
  mutation CreateSession($mapID: String, $fogEnabled: Boolean, $fogRadius: Int, $gridPassword: String, $moveDelayMs: Int) {
    tesla {
      createSession(mapID: $mapID, fogEnabled: $fogEnabled, fogRadius: $fogRadius, gridPassword: $gridPassword, moveDelayMs: $moveDelayMs) {
        id
        mapName
        generatedGridPassword
      }
    }
  }
`;

export const MOVE_MUTATION = `
  mutation Move($sessionID: ID!, $direction: Tesla_Direction!) {
    tesla {
      move(sessionID: $sessionID, direction: $direction) {
        success
        message
        attemptedTo { x y tileChar tileType passable }
        gameState {
          battery
          maxBattery
          score
          victory
          gameOver
          totalMoves
          resetCount
          totalParks
          message
          mapName
          fogEnabled
          fogRadius
          playerPos { x y }
          nearbyGrid { type visited id allowedDirections }
          currentMoves { fromPosition { x y } toPosition { x y } success }
        }
      }
    }
  }
`;

export const RESET_MUTATION = `
  mutation Reset($sessionID: ID!) {
    tesla {
      reset(sessionID: $sessionID) {
        battery
        maxBattery
        score
        victory
        gameOver
        totalMoves
        resetCount
        totalParks
        message
        mapName
        fogEnabled
        fogRadius
        playerPos { x y }
        nearbyGrid { type visited id allowedDirections }
        currentMoves { fromPosition { x y } toPosition { x y } success }
      }
    }
  }
`;
