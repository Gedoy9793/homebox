export class WeChatScanCancelledError extends Error {
  constructor() {
    super("wechat scan cancelled");
    this.name = "WeChatScanCancelledError";
  }
}

export function isWeChatScanCancelled(error: unknown): boolean {
  return error instanceof WeChatScanCancelledError;
}

export function isWeChatUserAgent(ua: string): boolean {
  return /MicroMessenger/i.test(ua);
}

export function isWeChatBrowser(): boolean {
  return typeof navigator !== "undefined" && isWeChatUserAgent(navigator.userAgent);
}

/**
 * WeChat barcode scans come back as "EAN_13,6901234567890". QR codes are the
 * raw payload (usually a URL).
 */
export function normalizeWeChatScanResult(resultStr: string): string {
  const trimmed = resultStr.trim();
  const typed = /^(EAN_8|EAN_13|UPC_A|UPC_E|CODE_128|CODE_39|QR_CODE),(.+)$/i.exec(trimmed);
  return typed ? typed[2]!.trim() : trimmed;
}

type WeChatScanResponse = {
  err_msg?: string;
  errMsg?: string;
  resultStr?: string;
};

type WeixinBridge = {
  invoke: (method: string, params: Record<string, unknown>, callback: (res: WeChatScanResponse) => void) => void;
};

function weixinBridge(): WeixinBridge | undefined {
  return (globalThis as { WeixinJSBridge?: WeixinBridge }).WeixinJSBridge;
}

function waitForWeixinBridge(timeoutMs: number): Promise<WeixinBridge> {
  const existing = weixinBridge();
  if (existing) {
    return Promise.resolve(existing);
  }

  if (typeof document === "undefined") {
    return Promise.reject(new Error("WeixinJSBridge is not available"));
  }

  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      document.removeEventListener("WeixinJSBridgeReady", onReady);
      reject(new Error("WeixinJSBridge timed out"));
    }, timeoutMs);

    function onReady() {
      clearTimeout(timer);
      const bridge = weixinBridge();
      if (bridge) {
        resolve(bridge);
        return;
      }
      reject(new Error("WeixinJSBridge is not available"));
    }

    document.addEventListener("WeixinJSBridgeReady", onReady, { once: true });
  });
}

function finishScan(res: WeChatScanResponse, resolve: (value: string) => void, reject: (error: Error) => void): void {
  const msg = res.err_msg ?? res.errMsg ?? "";
  if (/cancel/i.test(msg)) {
    reject(new WeChatScanCancelledError());
    return;
  }
  if (msg && !/:ok$/i.test(msg) && !/:ok\b/i.test(msg)) {
    reject(new Error(msg));
    return;
  }

  const text = (res.resultStr ?? "").trim();
  if (!text) {
    reject(new Error("empty WeChat scan result"));
    return;
  }
  resolve(text);
}

/**
 * Opens WeChat's built-in scanner and returns the payload. Callers should only
 * use this in a WeChat WebView; it relies on the injected WeixinJSBridge.
 */
export async function scanQRCodeInWeChat(timeoutMs = 3000): Promise<string> {
  const bridge = await waitForWeixinBridge(timeoutMs);

  return new Promise((resolve, reject) => {
    bridge.invoke(
      "scanQRCode",
      {
        needResult: 1,
        scanType: ["qrCode", "barCode"],
      },
      res => finishScan(res, resolve, reject)
    );
  });
}
