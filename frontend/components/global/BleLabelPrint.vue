<script setup lang="ts">
  import { computed, onMounted, ref } from "vue";
  import { useI18n } from "vue-i18n";
  import { readLabelSpecFromPng, type LabelSpec } from "~~/lib/labels/label-spec";
  import { Button } from "@/components/ui/button";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { toast } from "@/components/ui/sonner";
  import MdiBluetooth from "~icons/mdi/bluetooth";
  import MdiLoading from "~icons/mdi/loading";

  const props = defineProps<{
    /** Label image endpoint; the layout embedded in it is what we print. */
    labelUrl: string;
  }>();

  const { t } = useI18n();
  const { available, connected, printerName, busy, selectPrinter, printSpec, printBitmap, disconnect } =
    useBleLabelPrinter();

  const settings = useLocalStorage(
    "homebox/labels/bluetooth",
    { copies: 1, width: 40, height: 30 },
    { mergeDefaults: true }
  );

  const spec = ref<LabelSpec>();
  const bitmap = ref("");
  const loading = ref(false);
  const layoutError = ref("");

  const copies = computed(() => Math.min(Math.max(Math.round(settings.value.copies) || 1, 1), 99));

  function reason(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  function toDataURL(buffer: ArrayBuffer): string {
    let binary = "";
    for (const byte of new Uint8Array(buffer)) {
      binary += String.fromCharCode(byte);
    }
    return `data:image/png;base64,${btoa(binary)}`;
  }

  /**
   * Reads the label layout embedded in the rendered label. Labels without one
   * are printed as the bitmap they are, which needs a paper size from the user.
   */
  async function loadLayout(): Promise<void> {
    loading.value = true;
    layoutError.value = "";
    try {
      const response = await fetch(props.labelUrl);
      if (!response.ok) {
        throw new Error(`label request failed with status ${response.status}`);
      }

      const buffer = await response.arrayBuffer();
      spec.value = await readLabelSpecFromPng(buffer);
      bitmap.value = spec.value ? "" : toDataURL(buffer);
    } catch (err) {
      console.error("Failed to read label layout:", err);
      spec.value = undefined;
      layoutError.value = reason(err);
    } finally {
      loading.value = false;
    }
  }

  async function print(): Promise<void> {
    try {
      if (!spec.value && !bitmap.value) {
        await loadLayout();
      }

      if (spec.value) {
        await printSpec(spec.value, copies.value);
      } else if (bitmap.value) {
        await printBitmap(bitmap.value, settings.value.width, settings.value.height, copies.value);
      } else {
        throw new Error(layoutError.value || "the label could not be loaded");
      }

      toast.success(t("components.global.label_maker.bluetooth.print_success"));
    } catch (err) {
      console.error("Bluetooth label printing failed:", err);
      toast.error(`${t("components.global.label_maker.bluetooth.print_failed")}: ${reason(err)}`);
    }
  }

  async function choosePrinter(): Promise<void> {
    try {
      await selectPrinter();
    } catch (err) {
      console.error("Failed to connect to the label printer:", err);
      toast.error(`${t("components.global.label_maker.bluetooth.connect_failed")}: ${reason(err)}`);
    }
  }

  async function disconnectPrinter(): Promise<void> {
    try {
      await disconnect();
    } catch (err) {
      console.error("Failed to disconnect the label printer:", err);
    }
  }

  onMounted(() => {
    if (available.value) {
      loadLayout();
    }
  });
</script>

<template>
  <div v-if="available" class="flex flex-col gap-2 rounded-md border p-3 text-sm">
    <div class="flex items-center justify-between gap-2">
      <span class="flex items-center gap-1 font-medium">
        <MdiBluetooth />
        {{ $t("components.global.label_maker.bluetooth.title") }}
      </span>
      <span class="text-muted-foreground">
        {{
          connected
            ? $t("components.global.label_maker.bluetooth.connected", { name: printerName })
            : $t("components.global.label_maker.bluetooth.not_connected")
        }}
      </span>
    </div>

    <p v-if="loading" class="text-muted-foreground">
      {{ $t("components.global.label_maker.bluetooth.reading_layout") }}
    </p>
    <p v-else-if="spec" class="text-muted-foreground">
      {{ $t("components.global.label_maker.bluetooth.layout_found", { width: spec.width, height: spec.height }) }}
    </p>
    <p v-else class="text-muted-foreground">
      {{
        layoutError
          ? $t("components.global.label_maker.bluetooth.layout_error", { error: layoutError })
          : $t("components.global.label_maker.bluetooth.layout_missing")
      }}
    </p>

    <div class="flex flex-wrap items-end gap-2">
      <div class="w-20">
        <Label for="ble-copies">{{ $t("components.global.label_maker.bluetooth.copies") }}</Label>
        <Input id="ble-copies" v-model.number="settings.copies" type="number" min="1" max="99" />
      </div>

      <template v-if="!spec">
        <div class="w-20">
          <Label for="ble-width">{{ $t("components.global.label_maker.bluetooth.width") }}</Label>
          <Input id="ble-width" v-model.number="settings.width" type="number" min="1" max="2000" />
        </div>
        <div class="w-20">
          <Label for="ble-height">{{ $t("components.global.label_maker.bluetooth.height") }}</Label>
          <Input id="ble-height" v-model.number="settings.height" type="number" min="1" max="2000" />
        </div>
      </template>
    </div>

    <div class="flex flex-wrap gap-2">
      <Button size="sm" :disabled="busy || loading" @click="print">
        <MdiLoading v-if="busy" class="animate-spin" />
        {{ $t("components.global.label_maker.bluetooth.print") }}
      </Button>
      <Button size="sm" variant="outline" :disabled="busy" @click="choosePrinter">
        {{
          connected
            ? $t("components.global.label_maker.bluetooth.change_printer")
            : $t("components.global.label_maker.bluetooth.choose_printer")
        }}
      </Button>
      <Button v-if="connected" size="sm" variant="outline" @click="disconnectPrinter">
        {{ $t("components.global.label_maker.bluetooth.disconnect") }}
      </Button>
    </div>
  </div>

  <p v-else class="text-sm text-muted-foreground">
    {{ $t("components.global.label_maker.bluetooth.unsupported") }}
  </p>
</template>
