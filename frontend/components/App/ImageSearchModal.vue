<template>
  <Dialog :dialog-id="DialogID.ImageSearch">
    <DialogScrollContent class="sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ t("items.image_search.title") }}</DialogTitle>
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

        <!-- Same live camera preview pattern as AppScannerModal -->
        <video ref="video" class="aspect-video w-full rounded-lg bg-muted shadow" poster="data:image/gif,AAAA" muted playsinline />

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

          <Button class="w-full" :disabled="loading || !cameraReady" @click="captureAndSearch">
            <MdiLoading v-if="loading" class="mr-2 animate-spin" />
            <MdiImageSearch v-else class="mr-2" />
            {{ loading ? t("items.image_search.searching") : t("items.image_search.capture_search") }}
          </Button>
        </div>

        <section v-if="searched" class="mt-4">
          <p v-if="!loading && results.length === 0" class="py-4 text-center text-sm text-muted-foreground">
            {{ $t("items.image_search.no_results") }}
          </p>
          <div v-else-if="results.length > 0" class="grid max-h-72 grid-cols-1 gap-2 overflow-y-auto sm:grid-cols-2">
            <div v-for="item in results" :key="item.id" @click="closeDialog(DialogID.ImageSearch)">
              <ItemCard :item="item" />
            </div>
          </div>
        </section>
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
  import { Button } from "@/components/ui/button";
  import { toast } from "@/components/ui/sonner";
  import { useDialog } from "@/components/ui/dialog-provider";
  import ItemCard from "~/components/Item/Card.vue";
  import type { EntitySummary } from "~~/lib/api/types/data-contracts";
  import MdiAlertCircleOutline from "~icons/mdi/alert-circle-outline";
  import MdiImageSearch from "~icons/mdi/image-search";
  import MdiLoading from "~icons/mdi/loading";

  const { t } = useI18n();
  const api = useUserApi();
  const { activeDialog, closeDialog } = useDialog();
  const open = computed(() => activeDialog && activeDialog.value === DialogID.ImageSearch);

  const sources = ref<MediaDeviceInfo[]>([]);
  const selectedSource = ref<string | null>(null);
  const video = ref<HTMLVideoElement>();
  const stream = ref<MediaStream | null>(null);
  const cameraReady = ref(false);
  const errorMessage = ref<string | null>(null);
  const loading = ref(false);
  const searched = ref(false);
  const results = ref<EntitySummary[]>([]);

  const LAST_USED_DEVICE_ID_KEY = "homebox:lastUsedDeviceId";

  const stopCamera = () => {
    stream.value?.getTracks().forEach(track => track.stop());
    stream.value = null;
    if (video.value) {
      video.value.srcObject = null;
    }
    cameraReady.value = false;
  };

  const startCamera = async (deviceId: string) => {
    stopCamera();
    errorMessage.value = null;
    try {
      const media = await navigator.mediaDevices.getUserMedia({
        video: { deviceId: { exact: deviceId } },
        audio: false,
      });
      stream.value = media;
      if (!video.value) {
        media.getTracks().forEach(track => track.stop());
        return;
      }
      video.value.srcObject = media;
      await video.value.play();
      cameraReady.value = true;
    } catch (err) {
      console.error("Image search camera error:", err);
      cameraReady.value = false;
      errorMessage.value = t("scanner.error");
    }
  };

  const listSources = async () => {
    errorMessage.value = null;
    searched.value = false;
    results.value = [];

    if (!(navigator && navigator.mediaDevices && "enumerateDevices" in navigator.mediaDevices)) {
      errorMessage.value = t("scanner.unsupported");
      return;
    }

    try {
      try {
        const permissionStream = await navigator.mediaDevices.getUserMedia({ video: true });
        permissionStream.getTracks().forEach(track => track.stop());
      } catch (err: unknown) {
        if (err instanceof Error && err.name === "NotAllowedError") {
          errorMessage.value = t("scanner.permission_denied");
          return;
        }
        throw err;
      }

      const devices = (await navigator.mediaDevices.enumerateDevices()).filter(d => d.kind === "videoinput");
      sources.value = devices;

      if (devices.length === 0) {
        errorMessage.value = t("scanner.no_sources");
        return;
      }

      let lastUsedDeviceId: string | null = null;
      try {
        lastUsedDeviceId = localStorage.getItem(LAST_USED_DEVICE_ID_KEY);
      } catch (e) {
        console.debug("failed to read selected camera", e);
      }

      selectedSource.value = devices[0]!.deviceId;
      for (const device of devices) {
        if (device.deviceId === lastUsedDeviceId) {
          selectedSource.value = device.deviceId;
          break;
        } else if (device.label.toLowerCase().includes("back")) {
          selectedSource.value = device.deviceId;
        }
      }
    } catch (err) {
      console.error("Image search camera error:", err);
      errorMessage.value = t("scanner.error");
    }
  };

  const captureAndSearch = async () => {
    const el = video.value;
    if (!el || !cameraReady.value || loading.value) return;
    if (el.videoWidth === 0 || el.videoHeight === 0) {
      toast.error(t("scanner.error"));
      return;
    }

    const canvas = document.createElement("canvas");
    canvas.width = el.videoWidth;
    canvas.height = el.videoHeight;
    const ctx = canvas.getContext("2d");
    if (!ctx) {
      toast.error(t("scanner.error"));
      return;
    }
    ctx.drawImage(el, 0, 0);

    const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, "image/jpeg", 0.92));
    if (!blob) {
      toast.error(t("items.image_search.toast.failed"));
      return;
    }

    loading.value = true;
    searched.value = true;
    results.value = [];

    const file = new File([blob], "capture.jpg", { type: "image/jpeg" });
    const { data, error, status } = await api.imageSearch.searchByImage(file, file.name);

    loading.value = false;
    if (error) {
      results.value = [];
      if (status === 503) {
        toast.error(t("items.image_search.toast.unavailable"));
      } else {
        toast.error(t("items.image_search.toast.failed"));
      }
      return;
    }
    results.value = data ?? [];
  };

  watch(open, async isOpen => {
    if (isOpen) {
      await listSources();
    } else {
      stopCamera();
      sources.value = [];
      selectedSource.value = null;
      searched.value = false;
      results.value = [];
      errorMessage.value = null;
    }
  });

  watch(selectedSource, async newSource => {
    if (!open.value || !newSource) return;
    try {
      localStorage.setItem(LAST_USED_DEVICE_ID_KEY, newSource);
    } catch (e) {
      console.warn("failed to persist selected camera", e);
    }
    await startCamera(newSource);
  });

  onUnmounted(() => {
    stopCamera();
  });
</script>
