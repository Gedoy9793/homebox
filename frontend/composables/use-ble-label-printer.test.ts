import { beforeEach, describe, expect, it, vi } from "vitest";
import { useBleLabelPrinter } from "./use-ble-label-printer";

const { api, getInstance } = vi.hoisted(() => {
  const api = {
    isPrinterOpened: vi.fn(() => true),
    getPrinterInfo: vi.fn(() => ({ name: "Test printer" })),
    startJob: vi.fn(() => ({})),
    commitJob: vi.fn(async () => ({ statusCode: 0 })),
    abortJob: vi.fn(),
  };

  return { api, getInstance: vi.fn(() => api) };
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

    expect(api.startJob).toHaveBeenCalledWith({
      width: 25,
      height: 15,
      orientation: 0,
    });
    expect(api.commitJob).toHaveBeenCalledWith({
      printCopies: 2,
      gapType: 2,
      gapLength: 6,
      printSpeed: undefined,
      printDarkness: undefined,
      threshold: undefined,
    });
  });
});
