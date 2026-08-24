// The label layout contract between Homebox (or an external label service) and
// the browser-side label printer.
//
// A label endpoint returns a PNG preview with this JSON embedded in a text
// chunk keyed by LABEL_SPEC_KEYWORD. All geometry is in millimetres so the
// browser can re-draw the label at the printer's own resolution instead of
// scaling up a low-DPI bitmap. Everything here is untrusted input, so the
// parser validates shapes and rejects the whole layout on anything unexpected
// rather than silently printing a label with missing content.

import { readPngTextChunks } from "./png-text";

export const LABEL_SPEC_KEYWORD = "homebox:label";

/** Largest label edge we accept, in millimetres. */
const MAX_EDGE_MM = 2000;
const MAX_ITEMS = 200;
const MAX_COPIES = 99;

export type LabelAlignment = "start" | "center" | "end" | "stretch";
export type LabelWrapMode = "none" | "char" | "word";
export type LabelRotation = 0 | 90 | 180 | 270;

export interface LabelItemBase {
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  rotation?: LabelRotation;
  align?: LabelAlignment;
  valign?: LabelAlignment;
}

export interface LabelTextItem extends LabelItemBase {
  type: "text";
  text: string;
  /** Character height in millimetres. */
  fontHeight?: number;
  fontName?: string;
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
  /** Millimetres, or one of the multipliers "1_0", "1_2", "1_5", "2_0". */
  lineSpace?: number | string;
  charSpace?: number;
  wrap?: LabelWrapMode;
  /** Shrink the text until it fits the box instead of overflowing it. */
  autoShrink?: boolean;
}

export interface LabelQrCodeItem extends LabelItemBase {
  type: "qrcode";
  text: string;
  eccLevel?: number;
  version?: number;
}

export interface LabelBarcodeItem extends LabelItemBase {
  type: "barcode";
  text: string;
  /** A dz-canvas BarcodeType name such as "CODE128", or its numeric value. */
  barcodeType?: string | number;
  /** Height of the human-readable text under the bars; 0 hides it. */
  textHeight?: number;
  barPixels?: number;
}

export interface LabelLineItem {
  type: "line";
  x1?: number;
  y1?: number;
  x2?: number;
  y2?: number;
  lineWidth?: number;
  dashLens?: number[];
  rotation?: LabelRotation;
}

export interface LabelRectItem extends LabelItemBase {
  type: "rect";
  lineWidth?: number;
  fill?: boolean;
  cornerWidth?: number;
  cornerHeight?: number;
}

export interface LabelEllipseItem extends LabelItemBase {
  type: "ellipse";
  lineWidth?: number;
  fill?: boolean;
}

export interface LabelCircleItem {
  type: "circle";
  x?: number;
  y?: number;
  radius?: number;
  lineWidth?: number;
  fill?: boolean;
}

export interface LabelImageItem extends LabelItemBase {
  type: "image";
  /** A data: URI or a same-origin URL. */
  src: string;
  threshold?: number;
}

export type LabelItem =
  | LabelTextItem
  | LabelQrCodeItem
  | LabelBarcodeItem
  | LabelLineItem
  | LabelRectItem
  | LabelEllipseItem
  | LabelCircleItem
  | LabelImageItem;

export interface LabelSpec {
  /** Label width in millimetres. */
  width: number;
  /** Label height in millimetres. */
  height: number;
  rotation?: LabelRotation;
  /** Paper type: 0 continuous, 2 gap, 3 black mark, 4 transparent, 255 printer default. */
  gapType?: number;
  /** Physical gap between die-cut labels, in millimetres. */
  gapLength?: number;
  /** 1 (slowest) to 5 (fastest), 255 for the printer default. */
  printSpeed?: number;
  /** 1 (lightest) to 15 (darkest), 255 for the printer default. */
  printDarkness?: number;
  /** Greyscale cut-off used when rasterising, 0-255. */
  threshold?: number;
  copies?: number;
  items: LabelItem[];
}

function fail(message: string): never {
  throw new Error(`invalid label layout: ${message}`);
}

function record(value: unknown, where: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    fail(`${where} must be an object`);
  }
  return value as Record<string, unknown>;
}

function optionalNumber(value: unknown, where: string): number | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "number" || !Number.isFinite(value)) {
    fail(`${where} must be a number`);
  }
  return value;
}

function edge(value: unknown, where: string): number {
  const parsed = optionalNumber(value, where);
  if (parsed === undefined || parsed <= 0 || parsed > MAX_EDGE_MM) {
    fail(`${where} must be between 0 and ${MAX_EDGE_MM} millimetres`);
  }
  return parsed;
}

function optionalBoolean(value: unknown, where: string): boolean | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "boolean") {
    fail(`${where} must be true or false`);
  }
  return value;
}

function text(value: unknown, where: string): string {
  if (typeof value !== "string") {
    fail(`${where} must be a string`);
  }
  return value;
}

function optionalString(value: unknown, where: string): string | undefined {
  return value === undefined || value === null ? undefined : text(value, where);
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[], where: string): T | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value !== "string" || !allowed.includes(value as T)) {
    fail(`${where} must be one of ${allowed.join(", ")}`);
  }
  return value as T;
}

function rotation(value: unknown, where: string): LabelRotation | undefined {
  const parsed = optionalNumber(value, where);
  if (parsed === undefined) {
    return undefined;
  }
  if (parsed !== 0 && parsed !== 90 && parsed !== 180 && parsed !== 270) {
    fail(`${where} must be 0, 90, 180 or 270`);
  }
  return parsed;
}

function numbers(value: unknown, where: string): number[] | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (!Array.isArray(value)) {
    fail(`${where} must be an array of numbers`);
  }
  return value.map((entry, i) => optionalNumber(entry, `${where}[${i}]`) ?? fail(`${where}[${i}] must be a number`));
}

/**
 * Images are rasterised through a canvas, and a cross-origin image would taint
 * it and fail the whole print, so only data: URIs and same-origin URLs pass.
 */
function imageSource(value: unknown, where: string): string {
  const src = text(value, where);
  if (src.startsWith("data:image/")) {
    return src;
  }

  const origin = globalThis.location?.origin;
  try {
    if (origin && new URL(src, origin).origin === origin) {
      return src;
    }
  } catch {
    fail(`${where} is not a valid URL`);
  }

  fail(`${where} must be a data: URI or a same-origin URL`);
}

const ALIGNMENTS = ["start", "center", "end", "stretch"] as const;
const WRAP_MODES = ["none", "char", "word"] as const;

function parseItemBase(raw: Record<string, unknown>, where: string): LabelItemBase {
  return {
    x: optionalNumber(raw.x, `${where}.x`),
    y: optionalNumber(raw.y, `${where}.y`),
    width: optionalNumber(raw.width, `${where}.width`),
    height: optionalNumber(raw.height, `${where}.height`),
    rotation: rotation(raw.rotation, `${where}.rotation`),
    align: oneOf(raw.align, ALIGNMENTS, `${where}.align`),
    valign: oneOf(raw.valign, ALIGNMENTS, `${where}.valign`),
  };
}

function parseItem(value: unknown, index: number): LabelItem {
  const where = `items[${index}]`;
  const raw = record(value, where);
  const base = parseItemBase(raw, where);

  switch (raw.type) {
    case "text":
      return {
        ...base,
        type: "text",
        text: text(raw.text, `${where}.text`),
        fontHeight: optionalNumber(raw.fontHeight, `${where}.fontHeight`),
        fontName: optionalString(raw.fontName, `${where}.fontName`),
        bold: optionalBoolean(raw.bold, `${where}.bold`),
        italic: optionalBoolean(raw.italic, `${where}.italic`),
        underline: optionalBoolean(raw.underline, `${where}.underline`),
        lineSpace:
          typeof raw.lineSpace === "string" ? raw.lineSpace : optionalNumber(raw.lineSpace, `${where}.lineSpace`),
        charSpace: optionalNumber(raw.charSpace, `${where}.charSpace`),
        wrap: oneOf(raw.wrap, WRAP_MODES, `${where}.wrap`),
        autoShrink: optionalBoolean(raw.autoShrink, `${where}.autoShrink`),
      };

    case "qrcode":
      return {
        ...base,
        type: "qrcode",
        text: text(raw.text, `${where}.text`),
        eccLevel: optionalNumber(raw.eccLevel, `${where}.eccLevel`),
        version: optionalNumber(raw.version, `${where}.version`),
      };

    case "barcode":
      return {
        ...base,
        type: "barcode",
        text: text(raw.text, `${where}.text`),
        barcodeType:
          typeof raw.barcodeType === "string"
            ? raw.barcodeType
            : optionalNumber(raw.barcodeType, `${where}.barcodeType`),
        textHeight: optionalNumber(raw.textHeight, `${where}.textHeight`),
        barPixels: optionalNumber(raw.barPixels, `${where}.barPixels`),
      };

    case "line":
      return {
        type: "line",
        x1: optionalNumber(raw.x1, `${where}.x1`),
        y1: optionalNumber(raw.y1, `${where}.y1`),
        x2: optionalNumber(raw.x2, `${where}.x2`),
        y2: optionalNumber(raw.y2, `${where}.y2`),
        lineWidth: optionalNumber(raw.lineWidth, `${where}.lineWidth`),
        dashLens: numbers(raw.dashLens, `${where}.dashLens`),
        rotation: base.rotation,
      };

    case "rect":
      return {
        ...base,
        type: "rect",
        lineWidth: optionalNumber(raw.lineWidth, `${where}.lineWidth`),
        fill: optionalBoolean(raw.fill, `${where}.fill`),
        cornerWidth: optionalNumber(raw.cornerWidth, `${where}.cornerWidth`),
        cornerHeight: optionalNumber(raw.cornerHeight, `${where}.cornerHeight`),
      };

    case "ellipse":
      return {
        ...base,
        type: "ellipse",
        lineWidth: optionalNumber(raw.lineWidth, `${where}.lineWidth`),
        fill: optionalBoolean(raw.fill, `${where}.fill`),
      };

    case "circle":
      return {
        type: "circle",
        x: base.x,
        y: base.y,
        radius: optionalNumber(raw.radius, `${where}.radius`),
        lineWidth: optionalNumber(raw.lineWidth, `${where}.lineWidth`),
        fill: optionalBoolean(raw.fill, `${where}.fill`),
      };

    case "image":
      return {
        ...base,
        type: "image",
        src: imageSource(raw.src, `${where}.src`),
        threshold: optionalNumber(raw.threshold, `${where}.threshold`),
      };

    default:
      fail(`${where}.type "${String(raw.type)}" is not supported`);
  }
}

export function parseLabelSpec(value: unknown): LabelSpec {
  const raw = record(value, "layout");

  if (!Array.isArray(raw.items)) {
    fail("items must be an array");
  }
  if (raw.items.length > MAX_ITEMS) {
    fail(`items must hold at most ${MAX_ITEMS} entries`);
  }

  const copies = optionalNumber(raw.copies, "copies");

  return {
    width: edge(raw.width, "width"),
    height: edge(raw.height, "height"),
    rotation: rotation(raw.rotation, "rotation"),
    gapType: optionalNumber(raw.gapType, "gapType"),
    gapLength: optionalNumber(raw.gapLength, "gapLength"),
    printSpeed: optionalNumber(raw.printSpeed, "printSpeed"),
    printDarkness: optionalNumber(raw.printDarkness, "printDarkness"),
    threshold: optionalNumber(raw.threshold, "threshold"),
    copies: copies === undefined ? undefined : Math.min(Math.max(Math.round(copies), 1), MAX_COPIES),
    items: raw.items.map(parseItem),
  };
}

function decodeBase64Utf8(value: string): string {
  const bytes = Uint8Array.from(atob(value), char => char.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

/**
 * Extracts the label layout embedded in a rendered label PNG. Returns
 * undefined when the image carries no layout — the caller then falls back to
 * printing the bitmap itself.
 */
export async function readLabelSpecFromPng(buffer: ArrayBuffer): Promise<LabelSpec | undefined> {
  const chunks = await readPngTextChunks(buffer);
  const chunk = chunks.find(candidate => candidate.keyword === LABEL_SPEC_KEYWORD);
  if (!chunk) {
    return undefined;
  }

  const payload = chunk.text.trimStart();
  // A plain tEXt chunk can only hold Latin-1, so services that need to stay
  // within it may base64-encode the JSON instead.
  const json = payload.startsWith("{") ? payload : decodeBase64Utf8(payload);

  return parseLabelSpec(JSON.parse(json));
}
