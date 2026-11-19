import { gql, type Client } from '@urql/svelte';
import { MAP_QUERY } from '$lib/tesla/queries';
import type { MapLayout } from '$lib/tesla/maps';
import uiAuthConfig from '$lib/tesla/config/ui-auth.json';

const mapQueryPassword = uiAuthConfig.uiMapPassword ?? '';

/** Loads one map's layout for previews; resolves to null on error. */
export async function fetchMapLayout(client: Client, mapId: string): Promise<MapLayout | null> {
	const result = await client.query(gql(MAP_QUERY), { name: mapId, password: mapQueryPassword || null }).toPromise();
	if (result.error) return null;
	return (result.data?.map as MapLayout | undefined) ?? null;
}
