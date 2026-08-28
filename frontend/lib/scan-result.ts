/** Path to navigate to after scanning a Homebox label URL. */
export function homeboxPathFromScanText(text: string): string | undefined {
  const trimmed = text.trim();
  if (!trimmed) {
    return undefined;
  }

  let pathname: string;
  try {
    pathname = new URL(trimmed).pathname;
  } catch {
    if (!trimmed.startsWith("/")) {
      return undefined;
    }
    pathname = trimmed;
  }

  if (!pathname.startsWith("/")) {
    return undefined;
  }

  const sanitized = pathname.replace(/[^a-zA-Z0-9-_/]/g, "");
  return sanitized || undefined;
}

/** Asset label URLs use `/a/000-042` so the QR matches the printed number. */
export function assetIdFromScanText(text: string): string | undefined {
  const path = homeboxPathFromScanText(text);
  if (!path) {
    return undefined;
  }

  const match = /^\/a\/(\d{3}-\d{3})\/?$/i.exec(path);
  return match?.[1];
}

/** Product barcodes the import dialog can look up. */
export function productBarcodeFromScanText(text: string): string | undefined {
  const trimmed = text.trim();
  return /^\d{8,14}$/.test(trimmed) ? trimmed : undefined;
}
