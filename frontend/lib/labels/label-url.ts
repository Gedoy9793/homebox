// Builds the URLs of Homebox's label endpoints.
//
// LabelMaker.vue has its own copy of this for the print dialog; this one exists
// so printing from elsewhere — while creating an item, say — does not have to
// reach into that component.

import { type QueryValue, route } from "../api/base/urls";
import { assetIdFromScanText } from "../scan-result";

/** Which label endpoint to use. "item" and "entity" are the same thing. */
export type LabelKind = "item" | "entity" | "location" | "asset";

const PATHS: Record<LabelKind, string> = {
  item: "entity",
  entity: "entity",
  location: "location",
  asset: "asset",
};

/**
 * The location id encoded in a scanned label, i.e. the `<uuid>` of a
 * `/location/<uuid>` URL. Returns undefined for anything else — an item label, an
 * asset label, or a barcode that is not a URL at all.
 */
export function locationIdFromUrl(text: string): string | undefined {
  try {
    return /^\/location\/([0-9a-f-]{36})\/?$/i.exec(new URL(text).pathname)?.[1];
  } catch {
    return undefined;
  }
}

/**
 * Resolves a scanned location label to the location's entity id. Location labels
 * encode `/a/000-042` in the QR, not `/location/<uuid>`, so asset lookups are
 * needed for those codes.
 */
export async function resolveLocationIdFromScanText(
  text: string,
  lookupByAssetId: (assetId: string) => Promise<{ id: string; isLocation: boolean } | undefined>
): Promise<string | undefined> {
  const direct = locationIdFromUrl(text);
  if (direct) {
    return direct;
  }

  const assetId = assetIdFromScanText(text);
  if (!assetId) {
    return undefined;
  }

  const entity = await lookupByAssetId(assetId);
  return entity?.isLocation ? entity.id : undefined;
}

export function labelUrl(kind: LabelKind, id: string, options: { print?: boolean; tenant?: string } = {}): string {
  const params: Record<string, QueryValue> = { print: options.print ?? false };

  if (options.tenant) {
    params.tenant = options.tenant;
  }

  return route(`/labelmaker/${PATHS[kind]}/${id}`, params);
}
