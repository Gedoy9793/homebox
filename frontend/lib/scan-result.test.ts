import { describe, expect, it } from "vitest";
import { homeboxPathFromScanText, productBarcodeFromScanText } from "./scan-result";

describe("homeboxPathFromScanText", () => {
  it("keeps Homebox label paths", () => {
    const uuid = "0198f0a1-0000-7000-8000-000000000001";
    expect(homeboxPathFromScanText(`https://homebox.example.com/item/${uuid}`)).toBe(`/item/${uuid}`);
    expect(homeboxPathFromScanText(`https://homebox.example.com/location/${uuid}/`)).toBe(`/location/${uuid}/`);
    expect(homeboxPathFromScanText("https://homebox.example.com/a/000-042")).toBe("/a/000-042");
    expect(homeboxPathFromScanText("/item/abc")).toBe("/item/abc");
  });

  it("rejects empty and non-path payloads", () => {
    expect(homeboxPathFromScanText("")).toBeUndefined();
    expect(homeboxPathFromScanText("8801073141735")).toBeUndefined();
    expect(homeboxPathFromScanText("not a url")).toBeUndefined();
  });
});

describe("productBarcodeFromScanText", () => {
  it("accepts numeric product barcodes", () => {
    expect(productBarcodeFromScanText("8801073141735")).toBe("8801073141735");
    expect(productBarcodeFromScanText(" 12345678 ")).toBe("12345678");
  });

  it("rejects URLs and short or non-numeric text", () => {
    expect(productBarcodeFromScanText("https://example.com/item/1")).toBeUndefined();
    expect(productBarcodeFromScanText("123")).toBeUndefined();
    expect(productBarcodeFromScanText("ABC123456789")).toBeUndefined();
  });
});
