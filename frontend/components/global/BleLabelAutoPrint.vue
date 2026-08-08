<script setup lang="ts">
  // Prints a label right after something is created, so a batch of items can be
  // entered and labelled without opening each one's page to hit print.
  //
  // The printer connection is deliberately not established here: Web Bluetooth
  // only opens its device picker during a user gesture, and by the time an item
  // has been created that gesture is long gone. So the user connects up front
  // with the button below, and the connection — shared through the composable —
  // is then reused for every create.
  import { useI18n } from "vue-i18n";
  import { labelUrl, type LabelKind } from "~~/lib/labels/label-url";
  import { Button } from "@/components/ui/button";
  import { Checkbox } from "@/components/ui/checkbox";
  import { Label } from "@/components/ui/label";
  import { toast } from "@/components/ui/sonner";
  import MdiPrinterPos from "~icons/mdi/printer-pos";

  const { t } = useI18n();
  const { available, connected, printerName, busy, selectPrinter, printLabelUrl } = useBleLabelPrinter();
  const { selectedId } = useCollections();

  const settings = useLocalStorage("homebox/labels/auto-print", { enabled: false, copies: 1 }, { mergeDefaults: true });

  const copies = computed(() => Math.min(Math.max(Math.round(settings.value.copies) || 1, 1), 99));

  const checkboxId = useId();

  function reason(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  async function connect(): Promise<void> {
    try {
      await selectPrinter();
    } catch (err) {
      console.error("Failed to connect to the label printer:", err);
      toast.error(`${t("components.global.label_maker.bluetooth.connect_failed")}: ${reason(err)}`);
    }
  }

  /**
   * Prints the label for a freshly created record. Never throws: a label that
   * did not come out must not look like the record failed to save.
   */
  async function printFor(id: string, kind: LabelKind = "entity"): Promise<void> {
    if (!available.value || !settings.value.enabled) {
      return;
    }

    if (!connected.value) {
      toast.warning(t("components.global.label_maker.bluetooth.auto_print_not_connected"));
      return;
    }

    try {
      await printLabelUrl(labelUrl(kind, id, { tenant: selectedId.value ?? undefined }), { copies: copies.value });
      toast.success(t("components.global.label_maker.bluetooth.print_success"));
    } catch (err) {
      console.error("Bluetooth label printing failed:", err);
      toast.error(`${t("components.global.label_maker.bluetooth.print_failed")}: ${reason(err)}`);
    }
  }

  defineExpose({ printFor });
</script>

<template>
  <div v-if="available" class="flex flex-wrap items-center gap-2 text-sm">
    <Checkbox :id="checkboxId" v-model="settings.enabled" />
    <Label :for="checkboxId" class="flex cursor-pointer items-center gap-1 font-normal">
      <MdiPrinterPos class="size-4" />
      {{ $t("components.global.label_maker.bluetooth.auto_print") }}
    </Label>

    <template v-if="settings.enabled">
      <Button v-if="!connected" type="button" size="sm" variant="outline" :disabled="busy" @click="connect">
        {{ $t("components.global.label_maker.bluetooth.choose_printer") }}
      </Button>
      <span v-else class="text-muted-foreground">{{ printerName }}</span>
    </template>
  </div>
</template>
