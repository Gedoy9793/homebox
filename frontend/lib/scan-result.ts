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

/** Product barcodes the import dialog can look up. */
export function productBarcodeFromScanText(text: string): string | undefined {
  const trimmed = text.trim();
  return /^\d{8,14}$/.test(trimmed) ? trimmed : undefined;
}
