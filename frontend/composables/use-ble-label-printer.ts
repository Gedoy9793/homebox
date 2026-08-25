// Prints Homebox labels on a Bluetooth label printer straight from the browser,
// through the vendor SDK for DothanTech/NIIMBOT-style printers (lpapi-ble) on
// top of Web Bluetooth.
//
// Given a label layout (see lib/labels/label-spec.ts) the label is re-drawn at
// the printer's own resolution, which stays sharp where sending the low-DPI
// preview bitmap would not. Printers that only give us a picture can still be
// fed one through printBitmap.

import { ref } from "vue";
import type { BleLabelSpecOverrides } from "./use-ble-label-settings";
import { readLabelSpecFromPng, type LabelItem, type LabelSpec } from "~~/lib/labels/label-spec";

type LpapiModule = typeof import("lpapi-ble");
type Lpapi = InstanceType<LpapiModule["LPAPI"]>;
type PrinterInfo = import("lpapi-ble").IPrinterInfoExt;
type LpapiResponse = {
  statusCode: number;
  errMsg?: string;
  printable?: number;
};

/**
 * lpapi-ble reads the printer's gap register as hundredths of a millimetre,
 * while commitJob accepts a millimetre value and performs that conversion
 * itself. Keep the value exposed to the UI in the human-readable unit.
 */
export function printerGapLengthMm(value: number | undefined): number | undefined {
  return value === undefined || !Number.isFinite(value) ? undefined : value / 100;
}

function normalizePrinterInfo(info: PrinterInfo | undefined): PrinterInfo | undefined {
  if (!info || info.gapLength === undefined) {
    return info;
  }

  return { ...info, gapLength: printerGapLengthMm(info.gapLength) };
}

/** dz-canvas Alignment. */
const ALIGNMENT = { start: 0, center: 1, end: 2, stretch: 3 } as const;
/** dz-canvas WrapMode. */
const WRAP_MODE = { none: 0, char: 1, word: 2 } as const;
/** dz-canvas FontStyle flags. */
const FONT_BOLD = 1;
const FONT_ITALIC = 2;
const FONT_UNDERLINE = 4;

const RESULT_OK = 0;
/** lpapi-ble printable codes that mean the motor is still moving. */
const PRINTABLE_PRINTING = 1;
const PRINTABLE_MOTOR = 2;
const PRINTER_IDLE_TIMEOUT_MS = 8000;
const PRINTER_IDLE_POLL_MS = 80;

// The SDK is ~330 KB and only needed once someone actually prints.
let modulePromise: Promise<LpapiModule> | undefined;

function loadLpapi(): Promise<LpapiModule> {
  modulePromise ??= import("lpapi-ble");
  return modulePromise;
}

/**
 * Web Bluetooth is Chromium-only and, like every powerful web API, restricted
 * to secure contexts — a Homebox reachable over plain HTTP won't offer this.
 */
export function isWebBluetoothAvailable(): boolean {
  return typeof navigator !== "undefined" && "bluetooth" in navigator && globalThis.isSecureContext;
}

function describe(lpapi: LpapiModule, response: LpapiResponse): string {
  const details = [response.errMsg, lpapi.LPAPI.getResultMessage(response.statusCode)].filter(Boolean);
  if (response.printable) {
    details.push(lpapi.LPAPI.getPrintableMessage(response.printable));
  }
  return details.join(" - ") || `error ${response.statusCode}`;
}

function assertOk(lpapi: LpapiModule, response: LpapiResponse): void {
  if (response.statusCode !== RESULT_OK) {
    throw new Error(describe(lpapi, response));
  }
}

/** The SDK takes images as data URIs, so a fetched PNG has to be re-encoded. */
export function pngDataURL(buffer: ArrayBuffer): string {
  let binary = "";
  for (const byte of new Uint8Array(buffer)) {
    binary += String.fromCharCode(byte);
  }
  return `data:image/png;base64,${btoa(binary)}`;
}

function fontStyle(item: { bold?: boolean; italic?: boolean; underline?: boolean }): number {
  return (item.bold ? FONT_BOLD : 0) | (item.italic ? FONT_ITALIC : 0) | (item.underline ? FONT_UNDERLINE : 0);
}

function barcodeType(lpapi: LpapiModule, value: string | number | undefined): number | undefined {
  if (value === undefined || typeof value === "number") {
    return value;
  }

  const types = lpapi.BarcodeType as unknown as Record<string, number | undefined>;
  const resolved = types[value] ?? types[value.toUpperCase()];
  if (resolved === undefined) {
    throw new Error(`unknown barcode type "${value}"`);
  }
  return resolved;
}

async function drawItem(lpapi: LpapiModule, api: Lpapi, item: LabelItem): Promise<void> {
  const placement =
    "type" in item && item.type !== "line" && item.type !== "circle"
      ? {
          x: item.x,
          y: item.y,
          width: item.width,
          height: item.height,
          orientation: item.rotation,
          horizontalAlignment: item.align === undefined ? undefined : ALIGNMENT[item.align],
          verticalAlignment: item.valign === undefined ? undefined : ALIGNMENT[item.valign],
        }
      : {};

  switch (item.type) {
    case "text":
      api.drawText({
        ...placement,
        text: item.text,
        fontHeight: item.fontHeight,
        fontName: item.fontName,
        fontStyle: fontStyle(item),
        lineSpace: item.lineSpace,
        charSpace: item.charSpace,
        autoReturn: item.wrap === undefined ? undefined : WRAP_MODE[item.wrap],
        autoShrink: item.autoShrink,
      });
      return;

    case "qrcode":
      api.drawQRCode({
        ...placement,
        text: item.text,
        eccLevel: item.eccLevel,
        version: item.version,
      });
      return;

    case "barcode":
      api.draw1DBarcode({
        ...placement,
        text: item.text,
        barcodeType: barcodeType(lpapi, item.barcodeType),
        textHeight: item.textHeight,
        barPixels: item.barPixels,
      });
      return;

    case "line":
      api.drawLine({
        x1: item.x1,
        y1: item.y1,
        x2: item.x2,
        y2: item.y2,
        lineWidth: item.lineWidth,
        dashLens: item.dashLens,
        orientation: item.rotation,
      });
      return;

    case "rect":
      api.drawRectangle({
        ...placement,
        lineWidth: item.lineWidth,
        fill: item.fill,
        cornerWidth: item.cornerWidth,
        cornerHeight: item.cornerHeight,
      });
      return;

    case "ellipse":
      api.drawEllipse({
        ...placement,
        lineWidth: item.lineWidth,
        fill: item.fill,
      });
      return;

    case "circle":
      api.drawCircle({
        x: item.x,
        y: item.y,
        radius: item.radius,
        lineWidth: item.lineWidth,
        fill: item.fill,
      });
      return;

    case "image": {
      const image = await api.loadImage(item.src);
      if (!image) {
        throw new Error("failed to load a label image");
      }
      api.drawImage({ ...placement, image, threshold: item.threshold });
    }
  }
}

// One printer, one connection, one set of state — shared by every component that
// asks for it. A Bluetooth connection belongs to the page, not to a dialog: the
// user connects once and then prints from wherever, and pairing again for each
// component would make printing while creating items unusable.
const available = ref(isWebBluetoothAvailable());
const connected = ref(false);
const printerName = ref("");
const printerInfo = ref<PrinterInfo>();
const busy = ref(false);

let instance: Lpapi | undefined;

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function setDrawingOffset(api: Lpapi, x: number, y: number): void {
  // Always apply. startJob does not reset DrawContext offsets, so a previous
  // rotated job can leave an axis-swapped leftover.
  api.getContext().setOffset(x, y);
}

function resetDrawingOffset(api: Lpapi): void {
  api.getContext().setOffset(0, 0);
}

type PagePrintProgress = LpapiResponse & {
  copyIndex?: number;
  printCopies?: number;
  pageIndex?: number;
  printPages?: number;
};

function pageIsLast(info: PagePrintProgress, copies: number): boolean {
  const copyIndex = info.copyIndex ?? 0;
  const printCopies = info.printCopies ?? copies;
  const pageIndex = info.pageIndex ?? 0;
  const printPages = info.printPages ?? 1;
  return pageIndex + 1 >= printPages && copyIndex + 1 >= printCopies;
}

/**
 * commitJob resolves when the page has been sent. The motor is often still
 * feeding to the next die-cut at that point, and the next job then starts
 * short — later labels print high. Wait for the page-complete callback.
 */
async function waitForPagePrinted(
  send: (onPagePrintComplete: (info: PagePrintProgress) => void) => Promise<LpapiResponse>,
  copies = 1
): Promise<LpapiResponse> {
  let lastPage: PagePrintProgress | undefined;
  let notifyPrinted = () => {};
  const printed = new Promise<void>(resolve => {
    notifyPrinted = resolve;
  });

  const sent = await send(info => {
    lastPage = info;
    if (pageIsLast(info, copies)) {
      notifyPrinted();
    }
  });

  if (sent.statusCode !== RESULT_OK) {
    return sent;
  }

  await Promise.race([printed, sleep(PRINTER_IDLE_TIMEOUT_MS)]);
  return lastPage ?? sent;
}

async function waitUntilPrinterIdle(api: Lpapi, timeoutMs = PRINTER_IDLE_TIMEOUT_MS): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const printable = api.getPrinterInfo()?.printable;
    if (printable !== PRINTABLE_PRINTING && printable !== PRINTABLE_MOTOR) {
      return;
    }
    await sleep(PRINTER_IDLE_POLL_MS);
  }
}

export function useBleLabelPrinter() {
  async function getApi(): Promise<{ lpapi: LpapiModule; api: Lpapi }> {
    const lpapi = await loadLpapi();
    instance ??= lpapi.LPAPI.getInstance({ webBLE: true });
    return { lpapi, api: instance };
  }

  function syncConnection(api: Lpapi): void {
    connected.value = api.isPrinterOpened();
    const info = connected.value ? normalizePrinterInfo(api.getPrinterInfo()) : undefined;
    printerInfo.value = info;
    printerName.value = info?.name ?? "";
  }

  /** Refreshes the printer capabilities and paper calibration values. */
  async function refreshPrinterInfo(): Promise<PrinterInfo | undefined> {
    const { api } = await getApi();
    syncConnection(api);
    return printerInfo.value;
  }

  /**
   * Opens the browser's Bluetooth device picker and connects to the chosen
   * printer. Must be called from a user gesture; Web Bluetooth refuses
   * otherwise.
   */
  async function selectPrinter(): Promise<void> {
    busy.value = true;
    try {
      const { lpapi, api } = await getApi();

      const found = await api.requestDevice({ webBLE: true });
      assertOk(lpapi, found);

      const device = found.resultInfo?.[0];
      if (!device) {
        throw new Error("no printer was selected");
      }

      const opened = await api.openPrinter({
        deviceId: device.deviceId,
        name: device.name,
        connectionStateChange: () => syncConnection(api),
      });
      assertOk(lpapi, opened);

      syncConnection(api);
    } finally {
      busy.value = false;
    }
  }

  async function withPrinter<T>(action: (lpapi: LpapiModule, api: Lpapi) => Promise<T>): Promise<T> {
    const { lpapi, api } = await getApi();
    if (!api.isPrinterOpened()) {
      await selectPrinter();
    }

    busy.value = true;
    try {
      return await action(lpapi, api);
    } finally {
      syncConnection(api);
      busy.value = false;
    }
  }

  /** Draws and prints a label layout at the printer's own resolution. */
  function printSpec(spec: LabelSpec, copies = 1): Promise<void> {
    return withPrinter(async (lpapi, api) => {
      const count = Math.min(Math.max(Math.round(copies) || 1, 1), 99);
      const paper = {
        gapType: spec.gapType,
        gapLength: spec.gapLength,
        printSpeed: spec.printSpeed,
        printDarkness: spec.printDarkness,
        threshold: spec.threshold,
      };
      const nextOffset = {
        x: spec.horizontalOffset ?? 0,
        y: spec.verticalOffset ?? 0,
      };

      // One page per job. The SDK will otherwise send every copy as soon as the
      // Bluetooth buffer is free, before the motor has found the next gap.
      for (let copy = 0; copy < count; copy++) {
        const job = api.startJob({
          width: spec.width,
          height: spec.height,
          orientation: spec.rotation ?? 0,
          // Both flags are required by this SDK to drop a leftover job.
          resetJob: true,
          autoAbort: true,
          ...paper,
        } as Parameters<Lpapi["startJob"]>[0]);
        if (!job) {
          throw new Error("the printer rejected the print job");
        }

        // The encoder's offset options are not consumed by this SDK version.
        // DrawContext applies the offset before conversion to printer dots and
        // also swaps the axes correctly for a rotated label.
        setDrawingOffset(api, nextOffset.x, nextOffset.y);

        try {
          try {
            for (const item of spec.items) {
              await drawItem(lpapi, api, item);
            }
          } catch (err) {
            api.abortJob();
            throw err;
          }

          assertOk(
            lpapi,
            await waitForPagePrinted(
              onPagePrintComplete =>
                api.commitJob({
                  printCopies: 1,
                  ...paper,
                  ...(spec.printAlignment === undefined ? {} : { printAlignment: spec.printAlignment }),
                  ...(spec.antiColor === undefined ? {} : { antiColor: spec.antiColor }),
                  ...(spec.horizontalFlip === undefined ? {} : { horizontalFlip: spec.horizontalFlip }),
                  onPagePrintComplete,
                }),
              1
            )
          );
          await waitUntilPrinterIdle(api);
        } finally {
          resetDrawingOffset(api);
        }
      }
    });
  }

  /** Prints a ready-made label image, for labels that carry no layout. */
  function printBitmap(
    src: string,
    width: number,
    height: number,
    copies = 1,
    options: Partial<BleLabelSpecOverrides> = {}
  ): Promise<void> {
    return withPrinter(async (lpapi, api) => {
      const count = Math.min(Math.max(Math.round(copies) || 1, 1), 99);
      const x = options.horizontalOffset ?? 0;
      const y = options.verticalOffset ?? 0;

      for (let copy = 0; copy < count; copy++) {
        try {
          assertOk(
            lpapi,
            await waitForPagePrinted(
              onPagePrintComplete =>
                api.printImage({
                  src,
                  width,
                  height,
                  copies: 1,
                  gapType: options.gapType,
                  gapLength: options.gapLength,
                  printSpeed: options.printSpeed,
                  printDarkness: options.printDarkness,
                  onPagePrintComplete,
                  ...(x === 0 && y === 0
                    ? {}
                    : {
                        onJobCreated: async () => {
                          setDrawingOffset(api, x, y);
                          return true;
                        },
                      }),
                }),
              1
            )
          );
          await waitUntilPrinterIdle(api);
        } finally {
          resetDrawingOffset(api);
        }
      }
    });
  }

  /**
   * Prints the label served at url: the embedded layout when it has one, the
   * image itself otherwise. fallback gives the paper size to use for that
   * second case, where nothing tells us how big the label is.
   */
  async function printLabelUrl(
    url: string,
    options: {
      copies?: number;
      fallback?: { width: number; height: number };
      specOverrides?: Partial<BleLabelSpecOverrides>;
    } = {}
  ): Promise<void> {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error(`label request failed with status ${response.status}`);
    }

    const buffer = await response.arrayBuffer();
    const spec = await readLabelSpecFromPng(buffer);

    if (spec) {
      await printSpec({ ...spec, ...options.specOverrides }, options.copies);
      return;
    }

    if (!options.fallback) {
      throw new Error("the label carries no layout and no paper size was given");
    }

    await printBitmap(
      pngDataURL(buffer),
      options.fallback.width,
      options.fallback.height,
      options.copies,
      options.specOverrides
    );
  }

  async function disconnect(): Promise<void> {
    if (!instance) {
      return;
    }

    // Closing mid-job would cut the label off, so let the SDK flush first.
    await instance.closePrinter();
    syncConnection(instance);
  }

  return {
    available,
    connected,
    printerName,
    printerInfo,
    busy,
    selectPrinter,
    refreshPrinterInfo,
    printSpec,
    printBitmap,
    printLabelUrl,
    disconnect,
  };
}
