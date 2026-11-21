import type { MapSummary } from '$lib/tesla/maps';

/**
 * Absolute URL of the GraphQL endpoint an AI agent should call.
 *
 * The UI normally talks to a relative `/graphql`, which is meaningless to an
 * agent running elsewhere (or to a human pasting a prompt into a chat), so this
 * resolves against the page origin and prefers an explicit PUBLIC_GRAPHQL_URL.
 */
export function graphqlEndpointUrl(): string {
	const configured =
		typeof import.meta !== 'undefined' ? import.meta.env?.PUBLIC_GRAPHQL_URL : undefined;
	if (configured) return configured;
	if (typeof window !== 'undefined') return `${window.location.origin}/graphql`;
	return '/graphql';
}

export type EditPromptInput = {
	map: Pick<MapSummary, 'mapId' | 'name' | 'gridSize' | 'maxBattery'>;
	/** The one-time legacy token, or null if this browser lacks it. */
	editKey: string | null;
	/** True when the signed-in caller owns the map, so no edit key is involved. */
	owned?: boolean;
	/** Absolute GraphQL endpoint, normally from graphqlEndpointUrl(). */
	apiUrl: string;
};

/**
 * Builds a self-contained prompt that hands an AI agent everything it needs to
 * edit one map: the endpoint, the map id, the authorization argument, and the
 * exact mutation shape. Kept pure so the wording and interpolation are
 * testable.
 */
export function buildEditWithAIPrompt({ map, editKey, owned = false, apiUrl }: EditPromptInput): string {
	const name = map.name || map.mapId;
	// A legacy token is a bearer secret. An owned map has no token: the server
	// checks the signed-in account, so the agent needs that account's bearer
	// token, which the UI must not print into a prompt preview.
	const authSection = owned
		? `This map belongs to my drbrain account, so updateMap needs no edit key. It needs a request header
    "Authorization: Bearer <my drbrain token>"; I will paste that token here when you ask.`
		: editKey
			? `The map's legacy edit key is:

    ${editKey}`
			: `I do not have the legacy edit key for this map, so you will need to ask me for it before you can save.`;

	const saveInstruction = owned
		? `Save the result with updateMap and no editKey, sending my bearer token in the Authorization header.`
		: editKey
			? `Save the result with updateMap, passing the legacy edit key above as \`editKey\`.`
			: `Once I give you the edit key, save with updateMap passing it as \`editKey\`.`;

	// Owned maps carry no key: the account is the authorization. Legacy maps
	// must send the one-time key, and the agent is told to ask when it is absent.
	const editKeyLine = owned ? '' : `      "editKey": ${editKey ? `"${editKey}"` : '"<ASK_ME>"'},\n`;

	return `I want to change a map in the Tesla Road Trip game. Please help me edit it with GraphQL.

GRAPHQL ENDPOINT
    ${apiUrl}

MAP
    id:     ${map.mapId}
    name:   ${name}
    grid:   ${map.gridSize}x${map.gridSize}
    battery: max ${map.maxBattery}

${authSection}

WHAT I WANT CHANGED
    <describe your change here — e.g. "move the park in the top-left to the middle",
     "add two superchargers near the home tile", "make the outer walls solid">

HOW TO DO IT
1. Read the current map first ${owned ? '(send my bearer token: a draft is visible only to its owner)' : '(this requires no credentials)'}:

    query GetMap {
      tesla {
        map(name: "${map.mapId}", password: "tesla-ui-map") {
          name description gridSize maxBattery startingBattery
          layout
          legend { key value }
          cellConfigs { key type allowedDirections }
        }
      }
    }

   Send it as POST ${apiUrl} with header "content-type: application/json" and a
   body of {"query": "...", "variables": {}}${owned ? '.\n   Add my "Authorization: Bearer <token>" header: a draft is visible only to its owner.' : '.'}

2. Change the layout. Rules the server enforces:
   - layout must have exactly ${map.gridSize} rows of ${map.gridSize} characters.
   - Characters: R road, H home, P park, S supercharger, W water, B building.
   - At least one H and at least one P are required.
   - \`legend\` and \`cellConfigs\` are LISTS of {key, value} / {key, type,
     allowedDirections} objects, both when read and when written. The query
     above returns them in exactly the shape updateMap accepts, so pass the
     read values straight back rather than rebuilding them.
   - Every park must be reachable from a home without running out of battery.
     Homes and superchargers refill the battery.
   - Keep \`legend\` with all six standard characters.

3. ${saveInstruction}

    mutation UpdateMap($name: String!, $patch: Tesla_GameMapPatchInput!, $editKey: String) {
      tesla {
        updateMap(name: $name, patch: $patch, editKey: $editKey) {
          name gridSize maxBattery
        }
      }
    }

    variables:
    {
      "name": "${map.mapId}",
${editKeyLine}      "patch": {
        "description": "...",
        "layout": ["...", "..."],
        "legend": [{"key":"R","value":"road"}, ...],
        "cellConfigs": [],
        "startingBattery": ${map.maxBattery}
      }
    }

4. To check a candidate map before saving, call validateMap with the full map
   (same fields plus "name"). It needs no credentials and reports whether the
   map parses and is solvable.

Only the fields you put in "patch" change; omitted fields keep their current
values. Tell me what you are about to change before you save.`;
}
