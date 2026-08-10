<template>
  <div class="w-full">
    <div class="flex w-full flex-col gap-1.5">
      <Label for="photo-uploader" class="flex w-full px-1">
        {{ label }}
      </Label>

      <div class="flex w-full gap-2">
        <div class="relative min-w-0 grow">
          <Button type="button" variant="outline" class="w-full" aria-hidden="true" @click.prevent="openFilePicker">
            {{ buttonLabel }}
          </Button>
          <Input
            id="photo-uploader"
            ref="fileInput"
            class="absolute left-0 top-0 size-full cursor-pointer opacity-0"
            type="file"
            accept="image/png,image/jpeg,image/gif,image/avif,image/webp,android/force-camera-workaround"
            multiple
            @change="onFilesSelected"
          />
        </div>
        <Button type="button" variant="outline" class="shrink-0" @click.prevent="openCamera">
          <MdiCamera class="mr-2" />
          {{ t("components.form.camera_capture.take_photo") }}
        </Button>
      </div>
    </div>

    <CameraCaptureDialog v-model:open="cameraOpen" @captured="onCaptured" />
  </div>
</template>

<script setup lang="ts">
  import { computed, ref } from "vue";
  import { useI18n } from "vue-i18n";
  import { Label } from "~/components/ui/label";
  import { Input } from "~/components/ui/input";
  import { Button } from "~/components/ui/button";
  import { toast } from "@/components/ui/sonner";
  import { filesToPhotoPreviews, type PhotoPreview } from "./photo-uploader";
  import CameraCaptureDialog from "./CameraCaptureDialog.vue";
  import MdiCamera from "~icons/mdi/camera";

  const props = withDefaults(
    defineProps<{
      label?: string;
      buttonLabel?: string;
      existingCount?: number;
    }>(),
    {
      label: undefined,
      buttonLabel: undefined,
      existingCount: 0,
    }
  );

  const emit = defineEmits<{
    (e: "selected", photos: PhotoPreview[]): void;
  }>();

  const { t } = useI18n();
  const fileInput = ref<HTMLInputElement | null>(null);
  const cameraOpen = ref(false);

  const label = computed(() => props.label || t("components.entity.create_modal.item_photo"));
  const buttonLabel = computed(() => props.buttonLabel || t("components.entity.create_modal.upload_photos"));

  function openFilePicker() {
    fileInput.value?.click();
  }

  function openCamera() {
    if (!navigator?.mediaDevices?.getUserMedia) {
      toast.error(t("scanner.unsupported"));
      return;
    }
    cameraOpen.value = true;
  }

  async function onFilesSelected(event: Event) {
    const input = event.target as HTMLInputElement;
    if (!input.files || input.files.length === 0) return;

    const photos = await filesToPhotoPreviews(input.files, props.existingCount);

    emit("selected", photos);
    input.value = "";
  }

  async function onCaptured(files: File[]) {
    if (files.length === 0) return;
    const photos = await filesToPhotoPreviews(files, props.existingCount);
    emit("selected", photos);
  }
</script>
