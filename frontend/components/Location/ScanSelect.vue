<script setup lang="ts">
  // Picks a location by scanning its label instead of hunting for it in the list.
  //
  // The global scanner (App/ScannerModal) cannot be reused here: it navigates to
  // whatever it scans, and it is a single shared dialog, so it has no way to hand a
  // result back to whoever opened it. This one stays inline and reports the id.
  import { useI18n } from "vue-i18n";
  import { resolveLocationIdFromScanText } from "~~/lib/labels/label-url";
  import {
    isWeChatBrowser,
    isWeChatScanCancelled,
    normalizeWeChatScanResult,
    scanQRCodeInWeChat,
  } from "~~/lib/wechat-scan";
  import {
    createPreferredBarcodeDetector,
    listVideoInputDevices,
    openCameraStream,
    preferredCameraId,
    rememberCameraId,
    requestCameraPermission,
    runBarcodeDetectLoop,
    stopMediaStream,
  } from "~~/lib/camera-barcode-scanner";
  import { Button } from "~/components/ui/button";
  import { toast } from "@/components/ui/sonner";
  import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select";
  import MdiQrcodeScan from "~icons/mdi/qrcode-scan";

  const emit = defineEmits<{ scanned: [id: string] }>();

  const { t } = useI18n();
  const api = useUserApi();

  const scanning = ref(false);
  const video = ref<HTMLVideoElement>();
  const sources = ref<MediaDeviceInfo[]>([]);
  const selectedSource = ref<string | null>(null);
  const error = ref("");

  let mediaStream: MediaStream | null = null;
  let detectAbort: AbortController | null = null;
  let detectorPromise: ReturnType<typeof createPreferredBarcodeDetector> | null = null;

  async function lookupLocationFromScan(text: string): Promise<string | undefined> {
    return resolveLocationIdFromScanText(text, async assetId => {
      const { data } = await api.assets.get(assetId);
      if (!data || data.total !== 1) {
        return undefined;
      }

      const entity = data.items[0];
      if (!entity?.entityType?.isLocation) {
        return undefined;
      }

      return { id: entity.id, isLocation: true };
    });
  }

  async function handleScannedText(text: string, notifyWithToast = false): Promise<boolean> {
    const id = await lookupLocationFromScan(text);
    if (!id) {
      const message = t("components.location.selector.scan_not_a_location");
      if (notifyWithToast) {
        toast.error(message);
      } else {
        error.value = message;
      }
      return false;
    }

    stop();
    emit("scanned", id);
    return true;
  }

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

  async function start(): Promise<void> {
    error.value = "";

    if (isWeChatBrowser()) {
      try {
        const text = normalizeWeChatScanResult(await scanQRCodeInWeChat());
        await handleScannedText(text, true);
        return;
      } catch (err) {
        if (isWeChatScanCancelled(err)) {
          return;
        }
        console.warn("WeChat scan unavailable, falling back to the in-app camera", err);
      }
    }

    await startCamera();
  }

  async function startCamera(): Promise<void> {
    scanning.value = true;

    if (!navigator?.mediaDevices) {
      error.value = t("scanner.unsupported");
      return;
    }

    try {
      await requestCameraPermission();

      detectorPromise ??= createPreferredBarcodeDetector();
      await detectorPromise;

      sources.value = await listVideoInputDevices();
      if (sources.value.length === 0) {
        error.value = t("scanner.no_sources");
        return;
      }

      selectedSource.value = preferredCameraId(sources.value) ?? null;
    } catch (err) {
      if (err instanceof Error && err.name === "NotAllowedError") {
        error.value = t("scanner.permission_denied");
        return;
      }

      console.error("Scanner error:", err);
      error.value = t("scanner.error");
    }
  }

  function stop(): void {
    stopCamera();
    scanning.value = false;
    sources.value = [];
    selectedSource.value = null;
  }

  watch([selectedSource, video], async ([source, el]) => {
    if (!scanning.value || !source || !el) {
      return;
    }

    stopCamera();
    rememberCameraId(source);

    try {
      const { detector, engine } = await (detectorPromise ??= createPreferredBarcodeDetector());
      console.debug(`[scanner] using ${engine} BarcodeDetector`);

      mediaStream = await openCameraStream(source, el);
      detectAbort = new AbortController();
      runBarcodeDetectLoop(
        detector,
        el,
        barcode => {
          void handleScannedText(barcode.rawValue).catch(scanErr => {
            console.error(scanErr);
            error.value = t("scanner.error");
          });
        },
        detectAbort.signal
      );
    } catch (err) {
      console.error("Scanner error:", err);
      error.value = t("scanner.error");
    }
  });

  onUnmounted(stop);
</script>

<template>
  <!-- A popover rather than an inline panel, so dropping this button into a form
       row cannot disturb that row's layout. -->
  <Popover :open="scanning" @update:open="value => (value ? start() : stop())">
    <PopoverTrigger as-child>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        class="h-7 gap-1 px-2 text-xs"
        :aria-label="$t('components.location.selector.scan')"
      >
        <MdiQrcodeScan class="size-4" />
        {{ $t("components.location.selector.scan") }}
      </Button>
    </PopoverTrigger>

    <PopoverContent class="w-80 p-2">
      <div class="flex flex-col gap-2">
        <!-- eslint-disable-next-line tailwindcss/no-custom-classname -->
        <video ref="video" class="aspect-video w-full rounded bg-muted" poster="data:image/gif,AAAA" />

        <p class="text-xs text-muted-foreground">
          {{ $t("components.location.selector.scan_hint") }}
        </p>
        <p v-if="error" class="text-xs text-destructive">{{ error }}</p>

        <Select v-if="sources.length > 1" v-model="selectedSource">
          <SelectTrigger class="h-8 text-xs">
            <SelectValue :placeholder="$t('scanner.select_video_source')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="source in sources" :key="source.deviceId" :value="source.deviceId">
              {{ source.label }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
    </PopoverContent>
  </Popover>
</template>
