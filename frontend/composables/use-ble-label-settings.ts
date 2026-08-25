import { useLocalStorage } from "@vueuse/core";
import type { Ref } from "vue";
import type { LabelGapType, LabelSpec } from "~~/lib/labels/label-spec";

export type BleLabelSettings = {
  copies: number;
  width: number;
  height: number;
  /** Empty means use the value embedded in the label layout. */
  gapType: string | number;
  /** Empty means use the value embedded in the label layout. */
  gapLength: string | number;
  /** Empty means do not add a local drawing offset. */
  horizontalOffset: string | number;
  verticalOffset: string | number;
  printSpeed: string | number;
  printDarkness: string | number;
};

export type BleLabelSpecOverrides = Pick<
  LabelSpec,
  "gapType" | "gapLength" | "horizontalOffset" | "verticalOffset" | "printSpeed" | "printDarkness"
>;

const DEFAULT_SETTINGS: BleLabelSettings = {
  copies: 1,
  width: 25,
  height: 15,
  gapType: "",
  gapLength: "",
  horizontalOffset: "",
  verticalOffset: "",
  printSpeed: "",
  printDarkness: "",
};

const settings = useLocalStorage("homebox/labels/bluetooth", DEFAULT_SETTINGS, {
  mergeDefaults: true,
});

function finiteSetting(value: unknown): number | undefined {
  if (value === undefined || value === null || (typeof value === "string" && value.trim() === "")) {
    return undefined;
  }
  const parsed = typeof value === "number" ? value : Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function boundedSetting(value: unknown, min: number, max: number): number | undefined {
  const parsed = finiteSetting(value);
  return parsed !== undefined && parsed >= min && parsed <= max ? parsed : undefined;
}

/** Converts the persisted controls into safe per-job printer overrides. */
export function bleLabelSpecOverrides(value: BleLabelSettings): Partial<BleLabelSpecOverrides> {
  const requestedGapType = finiteSetting(value.gapType);
  const gapType = [0, 1, 2, 3, 4, 255].includes(requestedGapType ?? -1) ? requestedGapType : undefined;
  const gapLength = boundedSetting(value.gapLength, 0, 163.83);
  const horizontalOffset = boundedSetting(value.horizontalOffset, -20, 20);
  const verticalOffset = boundedSetting(value.verticalOffset, -20, 20);
  const printSpeed = boundedSetting(value.printSpeed, 1, 5);
  const printDarkness = boundedSetting(value.printDarkness, 1, 15);
  const overrides: Partial<BleLabelSpecOverrides> = {};

  if (gapType !== undefined) {
    overrides.gapType = gapType as LabelGapType;
    // Continuous/default jobs must not carry a die-cut gap inherited from the
    // server profile when the user has not entered a replacement.
    if ((gapType === 0 || gapType === 255) && gapLength === undefined) {
      overrides.gapLength = 0;
    }
  }
  if (gapLength !== undefined) {
    overrides.gapLength = gapLength;
  }
  if (horizontalOffset !== undefined) {
    overrides.horizontalOffset = horizontalOffset;
  }
  if (verticalOffset !== undefined) {
    overrides.verticalOffset = verticalOffset;
  }
  if (printSpeed !== undefined) {
    overrides.printSpeed = Math.round(printSpeed);
  }
  if (printDarkness !== undefined) {
    overrides.printDarkness = Math.round(printDarkness);
  }

  return overrides;
}

export function applyBleLabelSettings(spec: LabelSpec, value: BleLabelSettings): LabelSpec {
  return { ...spec, ...bleLabelSpecOverrides(value) };
}

export function useBleLabelSettings(): Ref<BleLabelSettings> {
  return settings as Ref<BleLabelSettings>;
}
