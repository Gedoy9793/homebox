// Builds the URLs of Homebox's label endpoints.
//
// LabelMaker.vue has its own copy of this for the print dialog; this one exists
// so printing from elsewhere — while creating an item, say — does not have to
// reach into that component.

import { type QueryValue, route } from "../api/base/urls";

/** Which label endpoint to use. "item" and "entity" are the same thing. */
export type LabelKind = "item" | "entity" | "location" | "asset";

const PATHS: Record<LabelKind, string> = {
  item: "entity",
  entity: "entity",
  location: "location",
  asset: "asset",
};

export function labelUrl(kind: LabelKind, id: string, options: { print?: boolean; tenant?: string } = {}): string {
  const params: Record<string, QueryValue> = { print: options.print ?? false };

  if (options.tenant) {
    params.tenant = options.tenant;
  }

  return route(`/labelmaker/${PATHS[kind]}/${id}`, params);
}
