import { describe, expect, it } from "vitest";
import { displayBarcodeFormat, isNativeBarcodeDetectorAvailable, isProductBarcodeFormat } from "./camera-barcode-scanner";

describe("isProductBarcodeFormat", () => {
  it("accepts retail barcode formats", () => {
    expect(isProductBarcodeFormat("ean_13")).toBe(true);
    expect(isProductBarcodeFormat("ean_8")).toBe(true);
    expect(isProductBarcodeFormat("upc_a")).toBe(true);
    expect(isProductBarcodeFormat("upc_e")).toBe(true);
  });

  it("rejects QR and unknown formats", () => {
    expect(isProductBarcodeFormat("qr_code")).toBe(false);
    expect(isProductBarcodeFormat("code_128")).toBe(false);
  });
});

describe("displayBarcodeFormat", () => {
  it("formats detector format names for the UI", () => {
    expect(displayBarcodeFormat("ean_13")).toBe("EAN-13");
    expect(displayBarcodeFormat("qr_code")).toBe("QR-CODE");
  });
});

describe("isNativeBarcodeDetectorAvailable", () => {
  it("returns false when BarcodeDetector is missing", () => {
    const previous = globalThis.BarcodeDetector;
    // @ts-expect-error intentional cleanup for the test
    delete globalThis.BarcodeDetector;
    expect(isNativeBarcodeDetectorAvailable()).toBe(false);
    globalThis.BarcodeDetector = previous;
  });
});
