import { useI18n } from "vue-i18n";
import { DialogID } from "@/components/ui/dialog-provider/utils";
import { useDialog } from "@/components/ui/dialog-provider";
import { toast } from "@/components/ui/sonner";
import { homeboxPathFromScanText, productBarcodeFromScanText } from "~~/lib/scan-result";
import {
  isWeChatBrowser,
  isWeChatScanCancelled,
  normalizeWeChatScanResult,
  scanQRCodeInWeChat,
} from "~~/lib/wechat-scan";

/** Opens WeChat's scanner when the page is inside WeChat, otherwise runs fallback. */
export function useAppScanner() {
  const { openDialog } = useDialog();
  const { t } = useI18n();

  function applyScannedText(raw: string): void {
    const text = normalizeWeChatScanResult(raw);
    const path = homeboxPathFromScanText(text);
    if (path) {
      navigateTo(path);
      return;
    }

    const barcode = productBarcodeFromScanText(text);
    if (barcode) {
      openDialog(DialogID.ProductImport, { params: { barcode } });
      return;
    }

    toast.error(t("scanner.invalid_url"));
  }

  async function scanWithWeChatOrFallback(fallback: () => void | Promise<void>): Promise<void> {
    if (isWeChatBrowser()) {
      try {
        applyScannedText(await scanQRCodeInWeChat());
        return;
      } catch (error) {
        if (isWeChatScanCancelled(error)) {
          return;
        }
        console.warn("WeChat scan unavailable, falling back to the in-app camera", error);
      }
    }

    await fallback();
  }

  return { applyScannedText, scanWithWeChatOrFallback };
}
