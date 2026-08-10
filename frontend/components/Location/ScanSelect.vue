<script setup lang="ts">
  // Picks a location by scanning its label instead of hunting for it in the list.
  //
  // The global scanner (App/ScannerModal) cannot be reused here: it navigates to
  // whatever it scans, and it is a single shared dialog, so it has no way to hand a
  // result back to whoever opened it. This one stays inline and reports the id.
  import { BrowserMultiFormatReader, NotFoundException } from "@zxing/library";
  import { useI18n } from "vue-i18n";
  import { locationIdFromUrl } from "~~/lib/labels/label-url";
  import { Button } from "~/components/ui/button";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select";
  import MdiQrcodeScan from "~icons/mdi/qrcode-scan";

  const emit = defineEmits<{ scanned: [id: string] }>();

  const { t } = useI18n();

  // Shared with the global scanner, so picking a camera once is enough.
  const LAST_USED_DEVICE_ID_KEY = "homebox:lastUsedDeviceId";

  const scanning = ref(false);
  const video = ref<HTMLVideoElement>();
  const sources = ref<MediaDeviceInfo[]>([]);
  const selectedSource = ref<string | null>(null);
  const error = ref("");

  let reader: BrowserMultiFormatReader | undefined;

  function preferredCamera(devices: MediaDeviceInfo[]): string | undefined {
    let remembered: string | null = null;
    try {
      remembered = localStorage.getItem(LAST_USED_DEVICE_ID_KEY);
    } catch (err) {
      console.debug("failed to read selected camera", err);
    }

    return (
      devices.find(device => device.deviceId === remembered)?.deviceId ??
      devices.find(device => device.label.toLowerCase().includes("back"))?.deviceId ??
      devices[0]?.deviceId
    );
  }

  async function start(): Promise<void> {
    error.value = "";
    scanning.value = true;

    if (!navigator?.mediaDevices) {
      error.value = t("scanner.unsupported");
      return;
    }

    reader ??= new BrowserMultiFormatReader();

    try {
      // Ask for permission before listing devices: without it the labels come
      // back empty, which makes the camera picker useless.
      const stream = await navigator.mediaDevices.getUserMedia({ video: true });
      stream.getTracks().forEach(track => track.stop());

      sources.value = await reader.listVideoInputDevices();
      if (sources.value.length === 0) {
        error.value = t("scanner.no_sources");
        return;
      }

      selectedSource.value = preferredCamera(sources.value) ?? null;
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
    reader?.reset();
    scanning.value = false;
    sources.value = [];
    selectedSource.value = null;
  }

  function toggle(): void {
    if (scanning.value) {
      stop();
    } else {
      start();
    }
  }

  watch(selectedSource, async source => {
    if (!scanning.value || !source || !video.value) {
      return;
    }

    reader?.reset();

    try {
      localStorage.setItem(LAST_USED_DEVICE_ID_KEY, source);
    } catch (err) {
      console.debug("failed to persist selected camera", err);
    }

    try {
      await reader?.decodeFromVideoDevice(source, video.value, (result, err) => {
        if (result) {
          const id = locationIdFromUrl(result.getText());
          if (!id) {
            // Keep the camera running: they may just have caught the wrong label.
            error.value = t("components.location.selector.scan_not_a_location");
            return;
          }

          stop();
          emit("scanned", id);
          return;
        }

        if (err && !(err instanceof NotFoundException)) {
          console.error(err);
          error.value = t("scanner.error");
        }
      });
    } catch (err) {
      console.error("Scanner error:", err);
      error.value = t("scanner.error");
    }
  });

  onUnmounted(stop);
</script>

<template>
  <div class="contents">
    <Button
      type="button"
      variant="outline"
      size="icon"
      :aria-label="$t('components.location.selector.scan')"
      :aria-pressed="scanning"
      @click="toggle"
    >
      <MdiQrcodeScan class="size-4" />
    </Button>

    <div v-if="scanning" class="col-span-full flex flex-col gap-2 rounded-md border p-2">
      <!-- eslint-disable-next-line tailwindcss/no-custom-classname -->
      <video ref="video" class="aspect-video w-full rounded bg-muted" poster="data:image/gif,AAAA" />

      <p class="text-xs text-muted-foreground">
        {{ $t("components.location.selector.scan_hint") }}
      </p>
      <p v-if="error" class="text-xs text-destructive">{{ error }}</p>

      <div class="flex gap-2">
        <Select v-if="sources.length > 1" v-model="selectedSource">
          <SelectTrigger class="h-8 flex-1 text-xs">
            <SelectValue :placeholder="$t('scanner.select_video_source')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="source in sources" :key="source.deviceId" :value="source.deviceId">
              {{ source.label }}
            </SelectItem>
          </SelectContent>
        </Select>
        <Button type="button" variant="ghost" size="sm" @click="stop">
          {{ $t("global.cancel") }}
        </Button>
      </div>
    </div>
  </div>
</template>
