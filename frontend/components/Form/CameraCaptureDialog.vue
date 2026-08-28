<template>
  <AlertDialog :open="open" @update:open="onOpenChange">
    <AlertDialogContent class="flex max-h-[min(90dvh,90vh)] w-full max-w-lg flex-col gap-0 overflow-hidden p-0 sm:rounded-lg">
      <div class="shrink-0 space-y-4 px-6 pt-6 pb-4">
        <AlertDialogHeader>
          <AlertDialogTitle>{{ t("components.form.camera_capture.title") }}</AlertDialogTitle>
          <AlertDialogDescription>
            {{ t("components.form.camera_capture.description") }}
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div
          v-if="errorMessage"
          class="flex items-center gap-2 rounded-md border border-destructive bg-destructive/10 p-3 text-destructive"
          role="alert"
        >
          <MdiAlertCircleOutline class="shrink-0" />
          <span class="text-sm font-medium">{{ errorMessage }}</span>
        </div>
      </div>

      <div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 pb-4">
        <video
          ref="video"
          class="aspect-video max-h-[min(42dvh,42vh)] w-full rounded-lg bg-muted object-cover shadow"
          poster="data:image/gif,AAAA"
          muted
          playsinline
        />

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

        <div v-if="shots.length > 0" class="flex gap-2 overflow-x-auto pb-1">
          <div v-for="(shot, i) in shots" :key="i" class="relative shrink-0">
            <img :src="shot.previewUrl" class="h-16 w-16 rounded object-cover" alt="" />
            <Button
              type="button"
              size="icon"
              variant="destructive"
              class="absolute -right-1 -top-1 size-5"
              @click="removeShot(i)"
            >
              <MdiClose class="size-3" />
            </Button>
          </div>
        </div>
      </div>

      <AlertDialogFooter class="shrink-0 gap-2 border-t bg-background px-6 py-4 sm:justify-between">
        <Button type="button" variant="outline" :disabled="!cameraReady || capturing" @click="capture">
          <MdiCamera class="mr-2" />
          {{ t("components.form.camera_capture.capture") }}
        </Button>
        <div class="flex gap-2">
          <AlertDialogCancel type="button">{{ t("global.cancel") }}</AlertDialogCancel>
          <Button type="button" :disabled="shots.length === 0" @click="confirmShots">
            {{ t("components.form.camera_capture.add_photos", { count: shots.length }) }}
          </Button>
        </div>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>

<script setup lang="ts">
  import { onUnmounted, ref, watch } from "vue";
  import { useI18n } from "vue-i18n";
  import {
    AlertDialog,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
  } from "@/components/ui/alert-dialog";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import { Button } from "@/components/ui/button";
  import { toast } from "@/components/ui/sonner";
  import MdiAlertCircleOutline from "~icons/mdi/alert-circle-outline";
  import MdiCamera from "~icons/mdi/camera";
  import MdiClose from "~icons/mdi/close";

  type Shot = { file: File; previewUrl: string };

  const props = defineProps<{
    open: boolean;
  }>();

  const emit = defineEmits<{
    (e: "update:open", value: boolean): void;
    (e: "captured", files: File[]): void;
  }>();

  const { t } = useI18n();

  const sources = ref<MediaDeviceInfo[]>([]);
  const selectedSource = ref<string | null>(null);
  const video = ref<HTMLVideoElement>();
  const stream = ref<MediaStream | null>(null);
  const cameraReady = ref(false);
  const capturing = ref(false);
  const errorMessage = ref<string | null>(null);
  const shots = ref<Shot[]>([]);

  const LAST_USED_DEVICE_ID_KEY = "homebox:lastUsedDeviceId";

  function onOpenChange(value: boolean) {
    emit("update:open", value);
  }

  function clearShots() {
    for (const shot of shots.value) {
      URL.revokeObjectURL(shot.previewUrl);
    }
    shots.value = [];
  }

  function stopCamera() {
    stream.value?.getTracks().forEach(track => track.stop());
    stream.value = null;
    if (video.value) {
      video.value.srcObject = null;
    }
    cameraReady.value = false;
  }

  async function startCamera(deviceId: string) {
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
      console.error("Camera capture error:", err);
      cameraReady.value = false;
      errorMessage.value = t("scanner.error");
    }
  }

  async function listSources() {
    errorMessage.value = null;
    clearShots();

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
      console.error("Camera capture error:", err);
      errorMessage.value = t("scanner.error");
    }
  }

  async function capture() {
    const el = video.value;
    if (!el || !cameraReady.value || capturing.value) return;
    if (el.videoWidth === 0 || el.videoHeight === 0) {
      toast.error(t("scanner.error"));
      return;
    }

    capturing.value = true;
    try {
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
        toast.error(t("scanner.error"));
        return;
      }

      const file = new File([blob], `photo-${Date.now()}.jpg`, { type: "image/jpeg" });
      shots.value.push({ file, previewUrl: URL.createObjectURL(blob) });
    } finally {
      capturing.value = false;
    }
  }

  function removeShot(index: number) {
    const [removed] = shots.value.splice(index, 1);
    if (removed) {
      URL.revokeObjectURL(removed.previewUrl);
    }
  }

  function confirmShots() {
    if (shots.value.length === 0) return;
    const files = shots.value.map(s => s.file);
    clearShots();
    emit("captured", files);
    emit("update:open", false);
  }

  watch(
    () => props.open,
    async isOpen => {
      if (isOpen) {
        await listSources();
      } else {
        stopCamera();
        clearShots();
        sources.value = [];
        selectedSource.value = null;
        errorMessage.value = null;
      }
    }
  );

  watch(selectedSource, async newSource => {
    if (!props.open || !newSource) return;
    try {
      localStorage.setItem(LAST_USED_DEVICE_ID_KEY, newSource);
    } catch (e) {
      console.warn("failed to persist selected camera", e);
    }
    await startCamera(newSource);
  });

  onUnmounted(() => {
    stopCamera();
    clearShots();
  });
</script>
