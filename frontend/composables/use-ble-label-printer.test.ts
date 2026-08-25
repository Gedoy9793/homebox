import { beforeEach, describe, expect, it, vi } from "vitest";
import { printerGapLengthMm, useBleLabelPrinter } from "./use-ble-label-printer";

const { api, context, getInstance } = vi.hoisted(() => {
  type PagePrintOptions = {
    onJobCreated?: () => Promise<boolean>;
    onPagePrintComplete?: (info: {
      statusCode: number;
      copyIndex?: number;
      printCopies?: number;
      pageIndex?: number;
      printPages?: number;
    }) => void;
  };

  function completePage(options?: PagePrintOptions) {
    options?.onPagePrintComplete?.({
      statusCode: 0,
      copyIndex: 0,
      printCopies: 1,
      pageIndex: 0,
      printPages: 1,
    });
  }

  const context = { setOffset: vi.fn() };
  const api = {
    isPrinterOpened: vi.fn(() => true),
    getPrinterInfo: vi.fn(() => ({
      name: "Test printer",
      printerDPI: 203,
      printerWidth: 384,
      paperWidth: 25,
      gapType: 2,
      // The printer register is in 0.01mm; the composable exposes 6mm.
      gapLength: 600,
    })),
    getContext: vi.fn(() => context),
    startJob: vi.fn(() => ({})),
    commitJob: vi.fn(async (options?: PagePrintOptions) => {
      completePage(options);
      return { statusCode: 0 };
    }),
    printImage: vi.fn(async (options: PagePrintOptions) => {
      await options.onJobCreated?.();
      completePage(options);
      return { statusCode: 0 };
    }),
    abortJob: vi.fn(),
  };

  return { api, context, getInstance: vi.fn(() => api) };
});

vi.mock("lpapi-ble", () => ({
  BarcodeType: {},
  LPAPI: {
    getInstance,
    getResultMessage: vi.fn(() => ""),
    getPrintableMessage: vi.fn(() => ""),
  },
}));

describe("useBleLabelPrinter", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("passes the physical stock gap to the print job", async () => {
    const { printSpec } = useBleLabelPrinter();

    await printSpec({ width: 25, height: 15, gapType: 2, gapLength: 6, items: [] }, 2);

    expect(api.startJob).toHaveBeenCalledTimes(2);
    expect(api.startJob).toHaveBeenCalledWith({
      width: 25,
      height: 15,
      orientation: 0,
      resetJob: true,
      autoAbort: true,
      gapType: 2,
      gapLength: 6,
      printSpeed: undefined,
      printDarkness: undefined,
      threshold: undefined,
    });
    expect(context.setOffset.mock.calls).toEqual([
      [0, 0],
      [0, 0],
      [0, 0],
      [0, 0],
    ]);
    expect(api.commitJob).toHaveBeenCalledTimes(2);
    expect(api.commitJob).toHaveBeenCalledWith({
      printCopies: 1,
      gapType: 2,
      gapLength: 6,
      printSpeed: undefined,
      printDarkness: undefined,
      threshold: undefined,
      onPagePrintComplete: expect.any(Function),
    });
  });

  it("passes calibration options and applies vector offsets", async () => {
    const { printSpec } = useBleLabelPrinter();

    await printSpec(
      {
        width: 25,
        height: 15,
        printAlignment: 1,
        horizontalOffset: -0.4,
        verticalOffset: 0.3,
        horizontalFlip: true,
        printSpeed: 4,
        printDarkness: 9,
        items: [],
      },
      1
    );

    expect(api.startJob).toHaveBeenCalledWith({
      width: 25,
      height: 15,
      orientation: 0,
      resetJob: true,
      autoAbort: true,
      gapType: undefined,
      gapLength: undefined,
      printSpeed: 4,
      printDarkness: 9,
      threshold: undefined,
    });
    expect(api.commitJob).toHaveBeenCalledWith({
      printCopies: 1,
      gapType: undefined,
      gapLength: undefined,
      printSpeed: 4,
      printDarkness: 9,
      threshold: undefined,
      printAlignment: 1,
      horizontalFlip: true,
      onPagePrintComplete: expect.any(Function),
    });
    expect(context.setOffset.mock.calls).toEqual([
      [-0.4, 0.3],
      [0, 0],
    ]);
  });

  it("resets vector offsets when a print job fails", async () => {
    api.commitJob.mockResolvedValueOnce({ statusCode: 7 });
    const { printSpec } = useBleLabelPrinter();

    await expect(
      printSpec({
        width: 25,
        height: 15,
        horizontalOffset: 0.5,
        verticalOffset: -0.2,
        items: [],
      })
    ).rejects.toThrow();

    expect(context.setOffset.mock.calls).toEqual([
      [0.5, -0.2],
      [0, 0],
    ]);
  });

  it("applies paper and offset settings to bitmap fallback jobs", async () => {
    const { printBitmap } = useBleLabelPrinter();

    await printBitmap("data:image/png;base64,AAAA", 25, 15, 2, {
      gapType: 2,
      gapLength: 2.75,
      horizontalOffset: -0.3,
      verticalOffset: 0.2,
      printSpeed: 3,
      printDarkness: 8,
    });

    expect(api.printImage).toHaveBeenCalledTimes(2);
    expect(api.printImage).toHaveBeenCalledWith(
      expect.objectContaining({
        src: "data:image/png;base64,AAAA",
        width: 25,
        height: 15,
        copies: 1,
        gapType: 2,
        gapLength: 2.75,
        printSpeed: 3,
        printDarkness: 8,
        onPagePrintComplete: expect.any(Function),
        onJobCreated: expect.any(Function),
      })
    );
    expect(context.setOffset.mock.calls).toEqual([
      [-0.3, 0.2],
      [0, 0],
      [-0.3, 0.2],
      [0, 0],
    ]);
  });

  it("exposes the connected printer capabilities", async () => {
    const { printerInfo, refreshPrinterInfo } = useBleLabelPrinter();
    await refreshPrinterInfo();

    expect(printerInfo.value).toMatchObject({
      printerDPI: 203,
      paperWidth: 25,
      gapType: 2,
      gapLength: 6,
    });
  });

  it("does not start the next copy until the previous page has printed", async () => {
    const order: string[] = [];
    api.startJob.mockImplementation(() => {
      order.push("start");
      return {};
    });
    api.commitJob.mockImplementation(async options => {
      order.push("commit");
      await new Promise(resolve => setTimeout(resolve, 40));
      order.push("page-complete");
      options.onPagePrintComplete?.({
        statusCode: 0,
        copyIndex: 0,
        printCopies: 1,
        pageIndex: 0,
        printPages: 1,
      });
      return { statusCode: 0 };
    });

    const { printSpec } = useBleLabelPrinter();
    await printSpec({ width: 25, height: 15, items: [] }, 2);

    expect(order).toEqual(["start", "commit", "page-complete", "start", "commit", "page-complete"]);
  });

  it("waits until the printer motor has stopped before finishing", async () => {
    let printable = 2;
    api.getPrinterInfo.mockImplementation(() => ({
      name: "Test printer",
      printerDPI: 203,
      printerWidth: 384,
      paperWidth: 25,
      gapType: 2,
      gapLength: 600,
      printable,
    }));
    api.commitJob.mockImplementationOnce(async options => {
      options.onPagePrintComplete?.({
        statusCode: 0,
        copyIndex: 0,
        printCopies: 1,
        pageIndex: 0,
        printPages: 1,
      });
      setTimeout(() => {
        printable = 0;
      }, 120);
      return { statusCode: 0 };
    });

    const { printSpec } = useBleLabelPrinter();
    await printSpec({ width: 25, height: 15, items: [] });

    expect(api.getPrinterInfo().printable).toBe(0);
  });

  it("converts the printer gap register from hundredths of a millimetre", () => {
    expect(printerGapLengthMm(600)).toBe(6);
    expect(printerGapLengthMm(0)).toBe(0);
    expect(printerGapLengthMm(undefined)).toBeUndefined();
    expect(printerGapLengthMm(Number.NaN)).toBeUndefined();
  });
});
