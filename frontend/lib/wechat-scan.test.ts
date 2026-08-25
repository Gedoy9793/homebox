import { afterEach, describe, expect, it } from "vitest";
import {
  isWeChatScanCancelled,
  isWeChatUserAgent,
  normalizeWeChatScanResult,
  scanQRCodeInWeChat,
  WeChatScanCancelledError,
} from "./wechat-scan";

describe("isWeChatUserAgent", () => {
  it("detects WeChat and ignores other browsers", () => {
    expect(isWeChatUserAgent("Mozilla/5.0 MicroMessenger/8.0.0")).toBe(true);
    expect(isWeChatUserAgent("Mozilla/5.0 (iPhone) AppleWebKit/605.1.15")).toBe(false);
  });
});

describe("normalizeWeChatScanResult", () => {
  it("strips WeChat barcode type prefixes", () => {
    expect(normalizeWeChatScanResult("EAN_13,6901234567890")).toBe("6901234567890");
    expect(normalizeWeChatScanResult("QR_CODE,https://homebox.example.com/item/1")).toBe(
      "https://homebox.example.com/item/1"
    );
    expect(normalizeWeChatScanResult("https://homebox.example.com/a/000-042")).toBe(
      "https://homebox.example.com/a/000-042"
    );
  });
});

describe("scanQRCodeInWeChat", () => {
  afterEach(() => {
    delete (globalThis as { WeixinJSBridge?: unknown }).WeixinJSBridge;
  });

  it("returns the scanned payload", async () => {
    (globalThis as { WeixinJSBridge: unknown }).WeixinJSBridge = {
      invoke: (_method: string, _params: unknown, callback: (res: { err_msg: string; resultStr: string }) => void) => {
        callback({ err_msg: "scanQRCode:ok", resultStr: "EAN_13,6901234567890" });
      },
    };

    await expect(scanQRCodeInWeChat()).resolves.toBe("EAN_13,6901234567890");
  });

  it("maps cancel responses to WeChatScanCancelledError", async () => {
    (globalThis as { WeixinJSBridge: unknown }).WeixinJSBridge = {
      invoke: (_method: string, _params: unknown, callback: (res: { err_msg: string }) => void) => {
        callback({ err_msg: "scanQRCode:cancel" });
      },
    };

    await expect(scanQRCodeInWeChat()).rejects.toBeInstanceOf(WeChatScanCancelledError);
    await expect(scanQRCodeInWeChat()).rejects.toSatisfy(isWeChatScanCancelled);
  });

  it("rejects when the bridge never appears", async () => {
    await expect(scanQRCodeInWeChat(50)).rejects.toThrow(/WeixinJSBridge/i);
  });
});
