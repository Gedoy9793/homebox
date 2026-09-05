import { BarcodeDetector as BarcodeDetectorPonyfill } from "barcode-detector/ponyfill";

/** Formats the main scanner needs: Homebox QR labels + product barcodes. */
export const SCAN_FORMATS = ["qr_code", "ean_13", "ean_8", "upc_a", "upc_e"] as const;

export type ScanFormat = (typeof SCAN_FORMATS)[number];

export type ScannedBarcode = {
  rawValue: string;
  format: string;
};

export type ScannerEngine = "native" | "wasm";

type Detector = {
  detect: (source: ImageBitmapSource) => Promise<Array<{ rawValue: string; format: string }>>;
};

const PRODUCT_FORMATS = new Set<string>(["ean_13", "ean_8", "upc_a", "upc_e"]);

export const LAST_USED_DEVICE_ID_KEY = "homebox:lastUsedDeviceId";

/** True only for a real browser BarcodeDetector, not a JS polyfill on globalThis. */
export function isNativeBarcodeDetectorAvailable(): boolean {
  try {
    return (
      typeof globalThis.BarcodeDetector === "function" &&
      Function.prototype.toString.call(globalThis.BarcodeDetector).includes("[native code]")
    );
  } catch {
    return false;
  }
}

export function isProductBarcodeFormat(format: string): boolean {
  return PRODUCT_FORMATS.has(format);
}

export function displayBarcodeFormat(format: string): string {
  return format.replaceAll("_", "-").toUpperCase();
}

export async function createPreferredBarcodeDetector(): Promise<{ detector: Detector; engine: ScannerEngine }> {
  if (isNativeBarcodeDetectorAvailable()) {
    try {
      const Native = globalThis.BarcodeDetector;
      const supported = await Native.getSupportedFormats();
      const formats = SCAN_FORMATS.filter(format => supported.includes(format));
      const detector = new Native({
        formats: formats.length > 0 ? [...formats] : ["qr_code"],
      });
      return { detector, engine: "native" };
    } catch (error) {
      console.warn("Native BarcodeDetector unavailable, falling back to WASM", error);
    }
  }

  const detector = new BarcodeDetectorPonyfill({ formats: [...SCAN_FORMATS] });
  return { detector, engine: "wasm" };
}

export async function listVideoInputDevices(): Promise<MediaDeviceInfo[]> {
  if (!navigator?.mediaDevices?.enumerateDevices) {
    return [];
  }

  const devices = await navigator.mediaDevices.enumerateDevices();
  return devices.filter(device => device.kind === "videoinput");
}

export function preferredCameraId(devices: MediaDeviceInfo[]): string | undefined {
  let remembered: string | null = null;
  try {
    remembered = localStorage.getItem(LAST_USED_DEVICE_ID_KEY);
  } catch (error) {
    console.debug("failed to read selected camera", error);
  }

  return (
    devices.find(device => device.deviceId === remembered)?.deviceId ??
    devices.find(device => device.label.toLowerCase().includes("back"))?.deviceId ??
    devices[0]?.deviceId
  );
}

export function rememberCameraId(deviceId: string): void {
  try {
    localStorage.setItem(LAST_USED_DEVICE_ID_KEY, deviceId);
  } catch (error) {
    console.debug("failed to persist selected camera", error);
  }
}

/** Ask for camera permission so device labels are populated. */
export async function requestCameraPermission(): Promise<void> {
  const stream = await navigator.mediaDevices.getUserMedia({
    video: { facingMode: { ideal: "environment" } },
  });
  stream.getTracks().forEach(track => track.stop());
}

export async function openCameraStream(deviceId: string, video: HTMLVideoElement): Promise<MediaStream> {
  const stream = await navigator.mediaDevices.getUserMedia({
    video: {
      deviceId: { exact: deviceId },
      facingMode: { ideal: "environment" },
      width: { ideal: 1920 },
      height: { ideal: 1080 },
    },
  });

  video.srcObject = stream;
  video.setAttribute("playsinline", "true");
  video.muted = true;
  await video.play();
  return stream;
}

export function stopMediaStream(stream: MediaStream | null | undefined): void {
  stream?.getTracks().forEach(track => track.stop());
}

const DETECT_INTERVAL_MS = 80;

/**
 * Continuously run BarcodeDetector against a live video element until aborted.
 * Skips overlapping detects so a slow native call cannot pile up.
 */
export function runBarcodeDetectLoop(
  detector: Detector,
  video: HTMLVideoElement,
  onDetect: (barcode: ScannedBarcode) => void,
  signal: AbortSignal
): void {
  let inFlight = false;

  const tick = async () => {
    if (signal.aborted) {
      return;
    }

    if (!inFlight && video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
      inFlight = true;
      try {
        const results = await detector.detect(video);
        if (!signal.aborted && results.length > 0) {
          const first = results[0]!;
          onDetect({ rawValue: first.rawValue, format: first.format });
        }
      } catch (error) {
        // Transient frame errors are normal while the camera is settling.
        if (!signal.aborted) {
          console.debug("barcode detect tick failed", error);
        }
      } finally {
        inFlight = false;
      }
    }

    if (!signal.aborted) {
      window.setTimeout(() => {
        void tick();
      }, DETECT_INTERVAL_MS);
    }
  };

  void tick();
}
