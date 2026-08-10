import { describe, expect, it } from "vitest";
import { locationIdFromUrl } from "./label-url";

const uuid = "0198f0a1-0000-7000-8000-000000000001";

describe("locationIdFromUrl", () => {
  it("reads the id out of a location label", () => {
    expect(locationIdFromUrl(`https://homebox.example.com/location/${uuid}`)).toBe(uuid);
    // Trailing slash, other ports, http — all the same label.
    expect(locationIdFromUrl(`http://localhost:7745/location/${uuid}/`)).toBe(uuid);
  });

  it("ignores anything that is not a location label", () => {
    const others = [
      // Item and asset labels: valid Homebox URLs, wrong kind of thing.
      `https://homebox.example.com/item/${uuid}`,
      "https://homebox.example.com/a/000-042",
      // A product barcode, which is what else a camera is likely to catch.
      "8801073141735",
      "",
      "not a url",
      // Deeper paths must not match: this is a page about the location, not it.
      `https://homebox.example.com/location/${uuid}/edit`,
      // Something shaped like a UUID but not one.
      "https://homebox.example.com/location/not-a-uuid",
    ];

    for (const text of others) {
      expect(locationIdFromUrl(text), text).toBeUndefined();
    }
  });
});
