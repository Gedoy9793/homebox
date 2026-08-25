import { describe, expect, it } from "vitest";
import { applyBleLabelSettings, bleLabelSpecOverrides, type BleLabelSettings } from "./use-ble-label-settings";

function settings(overrides: Partial<BleLabelSettings> = {}): BleLabelSettings {
  return {
    copies: 1,
    width: 25,
    height: 15,
    gapType: "",
    gapLength: "",
    horizontalOffset: "",
    verticalOffset: "",
    printSpeed: "",
    printDarkness: "",
    ...overrides,
  };
}

describe("bleLabelSpecOverrides", () => {
  it("converts the persisted controls into printer job values", () => {
    expect(
      bleLabelSpecOverrides(
        settings({
          gapType: "2",
          gapLength: "2.75",
          horizontalOffset: "-0.4",
          verticalOffset: 0.3,
          printSpeed: "4",
          printDarkness: 9,
        })
      )
    ).toEqual({
      gapType: 2,
      gapLength: 2.75,
      horizontalOffset: -0.4,
      verticalOffset: 0.3,
      printSpeed: 4,
      printDarkness: 9,
    });
  });

  it("clears a layout gap when continuous or printer-default stock is selected", () => {
    const spec = {
      width: 25,
      height: 15,
      gapType: 2 as const,
      gapLength: 6,
      items: [],
    };

    expect(applyBleLabelSettings(spec, settings({ gapType: "0" }))).toMatchObject({ gapType: 0, gapLength: 0 });
    expect(applyBleLabelSettings(spec, settings({ gapType: "255" }))).toMatchObject({ gapType: 255, gapLength: 0 });
  });

  it("ignores empty, non-finite and out-of-range values", () => {
    expect(
      bleLabelSpecOverrides(
        settings({
          gapType: "5",
          gapLength: 200,
          horizontalOffset: -21,
          verticalOffset: "not-a-number",
          printSpeed: 6,
          printDarkness: Number.NaN,
        })
      )
    ).toEqual({});
  });
});
