// Prints Homebox labels on a Bluetooth label printer straight from the browser,
// through the vendor SDK for DothanTech/NIIMBOT-style printers (lpapi-ble) on
// top of Web Bluetooth.
//
// Given a label layout (see lib/labels/label-spec.ts) the label is re-drawn at
// the printer's own resolution, which stays sharp where sending the low-DPI
// preview bitmap would not. Printers that only give us a picture can still be
// fed one through printBitmap.

import { ref } from "vue";
import type { LabelItem, LabelSpec } from "~~/lib/labels/label-spec";

type LpapiModule = typeof import("lpapi-ble");
type Lpapi = InstanceType<LpapiModule["LPAPI"]>;
type LpapiResponse = { statusCode: number; errMsg?: string; printable?: number };

/** dz-canvas Alignment. */
const ALIGNMENT = { start: 0, center: 1, end: 2, stretch: 3 } as const;
/** dz-canvas WrapMode. */
const WRAP_MODE = { none: 0, char: 1, word: 2 } as const;
/** dz-canvas FontStyle flags. */
const FONT_BOLD = 1;
const FONT_ITALIC = 2;
const FONT_UNDERLINE = 4;

const RESULT_OK = 0;

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
      api.drawQRCode({ ...placement, text: item.text, eccLevel: item.eccLevel, version: item.version });
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
      api.drawEllipse({ ...placement, lineWidth: item.lineWidth, fill: item.fill });
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

export function useBleLabelPrinter() {
  const available = ref(isWebBluetoothAvailable());
  const connected = ref(false);
  const printerName = ref("");
  const busy = ref(false);

  let instance: Lpapi | undefined;

  async function getApi(): Promise<{ lpapi: LpapiModule; api: Lpapi }> {
    const lpapi = await loadLpapi();
    instance ??= lpapi.LPAPI.getInstance({ webBLE: true });
    return { lpapi, api: instance };
  }

  function syncConnection(api: Lpapi): void {
    connected.value = api.isPrinterOpened();
    printerName.value = connected.value ? (api.getPrinterInfo()?.name ?? "") : "";
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
      const job = api.startJob({
        width: spec.width,
        height: spec.height,
        orientation: spec.rotation ?? 0,
      });
      if (!job) {
        throw new Error("the printer rejected the print job");
      }

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
        await api.commitJob({
          printCopies: copies,
          gapType: spec.gapType,
          printSpeed: spec.printSpeed,
          printDarkness: spec.printDarkness,
          threshold: spec.threshold,
        })
      );
    });
  }

  /** Prints a ready-made label image, for labels that carry no layout. */
  function printBitmap(src: string, width: number, height: number, copies = 1): Promise<void> {
    return withPrinter(async (lpapi, api) => {
      assertOk(lpapi, await api.printImage({ src, width, height, copies }));
    });
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
    busy,
    selectPrinter,
    printSpec,
    printBitmap,
    disconnect,
  };
}
