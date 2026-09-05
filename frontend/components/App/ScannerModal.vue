<template>
  <Dialog :dialog-id="DialogID.Scanner">
    <DialogScrollContent>
      <DialogHeader>
        <DialogTitle>{{ t("scanner.title") }}</DialogTitle>
      </DialogHeader>
      <div>
        <div
          v-if="errorMessage"
          class="mb-5 flex items-center gap-2 rounded-md border border-destructive bg-destructive/10 p-4 text-destructive"
          role="alert"
        >
          <MdiAlertCircleOutline class="text-destructive" />
          <span class="text-sm font-medium">{{ errorMessage }}</span>
        </div>
        <div
          v-if="detectedBarcode"
          class="mb-5 flex flex-col items-center gap-2 rounded-md border border-accent-foreground bg-accent p-4 text-accent-foreground"
          role="alert"
        >
          <div class="flex">
            <MdiBarcode class="mr-2" />
            <span class="flex-1 text-center text-sm font-medium">
              {{ detectedBarcodeType }} {{ $t("scanner.barcode_detected_message") }}:
              <strong>{{ detectedBarcode }}</strong>
            </span>
          </div>

          <ButtonGroup>
            <Button :disabled="loading" type="submit" @click="handleButtonClick">
              {{ $t("scanner.barcode_fetch_data") }}
            </Button>
          </ButtonGroup>
        </div>
        <!-- eslint-disable-next-line tailwindcss/no-custom-classname -->
        <video ref="video" class="aspect-video w-full rounded-lg bg-muted shadow" poster="data:image/gif,AAAA" />
        <div class="mt-4 flex flex-col gap-3">
          <Select v-model="selectedSource">
            <SelectTrigger class="w-full">
              <SelectValue :placeholder="t('scanner.select_video_source')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="source in sources" :key="source.deviceId" :value="source.deviceId">
                {{ source.label }}
              </SelectItem>
            </SelectContent>
          </Select>
          <Button variant="outline" class="w-full" @click="openArMode">
            <MdiCameraOutline class="mr-2" />
            {{ t("scanner_ar.ar_mode") }}
          </Button>
        </div>
      </div>
    </DialogScrollContent>
  </Dialog>
</template>

<script setup lang="ts">
  import { computed, ref, watch } from "vue";
  import { useI18n } from "vue-i18n";
  import { DialogID } from "@/components/ui/dialog-provider/utils";
  import { Dialog, DialogHeader, DialogScrollContent, DialogTitle } from "@/components/ui/dialog";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import { Button, ButtonGroup } from "@/components/ui/button";
  import MdiBarcode from "~icons/mdi/barcode";
  import MdiAlertCircleOutline from "~icons/mdi/alert-circle-outline";
  import MdiCameraOutline from "~icons/mdi/camera-outline";
  import { useDialog } from "@/components/ui/dialog-provider";
  import { homeboxPathFromScanText } from "~~/lib/scan-result";
  import {
    createPreferredBarcodeDetector,
    displayBarcodeFormat,
    isProductBarcodeFormat,
    listVideoInputDevices,
    openCameraStream,
    preferredCameraId,
    rememberCameraId,
    requestCameraPermission,
    runBarcodeDetectLoop,
    stopMediaStream,
    type ScannedBarcode,
  } from "~~/lib/camera-barcode-scanner";

  const { t } = useI18n();
  const { activeDialog, openDialog, closeDialog } = useDialog();
  const open = computed(() => activeDialog && activeDialog.value === DialogID.Scanner);

  const sources = ref<MediaDeviceInfo[]>([]);
  const selectedSource = ref<string | null>(null);
  const loading = ref(false);
  const video = ref<HTMLVideoElement>();
  const errorMessage = ref<string | null>(null);
  const detectedBarcode = ref<string>("");
  const detectedBarcodeType = ref<string>("");

  let mediaStream: MediaStream | null = null;
  let detectAbort: AbortController | null = null;
  let detectorPromise: ReturnType<typeof createPreferredBarcodeDetector> | null = null;

  const handleError = (error: unknown) => {
    console.error("Scanner error:", error);
    errorMessage.value = t("scanner.error");
  };

  const handleButtonClick = () => {
    openDialog(DialogID.ProductImport, { params: { barcode: detectedBarcode.value } });
  };

  const openArMode = () => {
    closeDialog(DialogID.Scanner);
    navigateTo("/scanner-ar");
  };

  function stopDetectLoop() {
    detectAbort?.abort();
    detectAbort = null;
  }

  function stopCamera() {
    stopDetectLoop();
    stopMediaStream(mediaStream);
    mediaStream = null;
    if (video.value) {
      video.value.srcObject = null;
    }
  }

  const handleScanned = async (barcode: ScannedBarcode) => {
    if (loading.value) {
      return;
    }

    loading.value = true;
    const text = barcode.rawValue;
    const path = homeboxPathFromScanText(text);
    if (path) {
      closeDialog(DialogID.Scanner);
      navigateTo(path);
      return;
    }

    if (isProductBarcodeFormat(barcode.format) || /^\d{8,14}$/.test(text.trim())) {
      stopDetectLoop();
      detectedBarcode.value = text.trim();
      detectedBarcodeType.value = displayBarcodeFormat(barcode.format);
      loading.value = false;
      return;
    }

    if (!errorMessage.value) {
      handleError(new Error(t("scanner.invalid_url")));
    }
    loading.value = false;
  };

  const startScanner = async () => {
    errorMessage.value = null;
    if (!(navigator && navigator.mediaDevices && "enumerateDevices" in navigator.mediaDevices)) {
      errorMessage.value = t("scanner.unsupported");
      return;
    }

    try {
      try {
        await requestCameraPermission();
      } catch (err: unknown) {
        if (err instanceof Error && err.name === "NotAllowedError") {
          errorMessage.value = t("scanner.permission_denied");
          return;
        }
        throw err;
      }

      detectorPromise ??= createPreferredBarcodeDetector();
      await detectorPromise;

      const devices = await listVideoInputDevices();
      sources.value = devices;

      if (devices.length > 0) {
        selectedSource.value = preferredCameraId(devices) ?? null;
      } else {
        errorMessage.value = t("scanner.no_sources");
      }
    } catch (err) {
      handleError(err);
    }
  };

  const stopScanner = () => {
    stopCamera();
    sources.value = [];
    selectedSource.value = null;
    loading.value = false;
  };

  watch(open, async isOpen => {
    if (isOpen) {
      detectedBarcode.value = "";
      detectedBarcodeType.value = "";
      await startScanner();
    } else {
      stopScanner();
    }
  });

  watch([selectedSource, video], async ([source, el]) => {
    if (!open.value || !source || !el) {
      return;
    }

    stopCamera();
    rememberCameraId(source);

    try {
      const { detector, engine } = await (detectorPromise ??= createPreferredBarcodeDetector());
      console.debug(`[scanner] using ${engine} BarcodeDetector`);

      mediaStream = await openCameraStream(source, el);
      detectAbort = new AbortController();
      runBarcodeDetectLoop(detector, el, barcode => {
        void handleScanned(barcode);
      }, detectAbort.signal);
    } catch (err) {
      handleError(err);
    }
  });

  onUnmounted(() => {
    stopScanner();
  });
</script>
