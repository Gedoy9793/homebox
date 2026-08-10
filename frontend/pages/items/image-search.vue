<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { toast } from "@/components/ui/sonner";
  import { Button } from "@/components/ui/button";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import ItemCard from "~/components/Item/Card.vue";
  import type { EntitySummary } from "~~/lib/api/types/data-contracts";
  import MdiCamera from "~icons/mdi/camera";
  import MdiImage from "~icons/mdi/image";
  import MdiLoading from "~icons/mdi/loading";
  import MdiArrowLeft from "~icons/mdi/arrow-left";

  const { t } = useI18n();

  definePageMeta({
    middleware: ["auth"],
  });

  useHead({
    title: "HomeBox | " + t("items.image_search.title"),
  });

  const api = useUserApi();
  const loading = ref(false);
  const searched = ref(false);
  const previewUrl = ref<string | null>(null);
  const results = ref<EntitySummary[]>([]);

  const cameraInput = ref<HTMLInputElement | null>(null);
  const fileInput = ref<HTMLInputElement | null>(null);

  function openCamera() {
    cameraInput.value?.click();
  }

  function openFilePicker() {
    fileInput.value?.click();
  }

  function clearPreview() {
    if (previewUrl.value) {
      URL.revokeObjectURL(previewUrl.value);
      previewUrl.value = null;
    }
  }

  async function onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;

    clearPreview();
    previewUrl.value = URL.createObjectURL(file);
    await search(file);
  }

  async function search(file: File) {
    loading.value = true;
    searched.value = true;
    results.value = [];

    const { data, error, status } = await api.imageSearch.searchByImage(file, file.name || "query.jpg");

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
  }

  onBeforeUnmount(() => {
    clearPreview();
  });
</script>

<template>
  <BaseContainer>
    <div class="mb-4">
      <NuxtLink to="/items" class="inline-flex items-center gap-1 text-sm text-muted-foreground hover:underline">
        <MdiArrowLeft class="size-4" />
        {{ $t("global.items") }}
      </NuxtLink>
    </div>

    <BaseSectionHeader>
      {{ $t("items.image_search.title") }}
      <template #description>
        {{ $t("items.image_search.description") }}
      </template>
    </BaseSectionHeader>

    <div class="mb-6 flex flex-wrap gap-2">
      <Button class="h-12 grow sm:grow-0" :disabled="loading" @click="openCamera">
        <MdiLoading v-if="loading" class="animate-spin" />
        <MdiCamera v-else />
        {{ $t("items.image_search.take_photo") }}
      </Button>
      <Button class="h-12 grow sm:grow-0" variant="outline" :disabled="loading" @click="openFilePicker">
        <MdiImage />
        {{ $t("items.image_search.choose_image") }}
      </Button>
      <input
        ref="cameraInput"
        type="file"
        class="hidden"
        accept="image/*"
        capture="environment"
        @change="onFileSelected"
      />
      <input
        ref="fileInput"
        type="file"
        class="hidden"
        accept="image/png,image/jpeg,image/gif,image/avif,image/webp"
        @change="onFileSelected"
      />
    </div>

    <div v-if="previewUrl" class="mb-6 overflow-hidden rounded-lg border">
      <img :src="previewUrl" :alt="$t('items.image_search.query_preview')" class="max-h-64 w-full object-contain" />
    </div>

    <div v-if="loading" class="flex items-center justify-center gap-2 py-12 text-muted-foreground">
      <MdiLoading class="size-5 animate-spin" />
      {{ $t("items.image_search.searching") }}
    </div>

    <section v-else-if="searched">
      <p v-if="results.length === 0" class="py-8 text-center text-muted-foreground">
        {{ $t("items.image_search.no_results") }}
      </p>
      <template v-else>
        <p class="mb-3 text-sm text-muted-foreground">
          {{ $t("items.results", { total: results.length }) }}
        </p>
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          <ItemCard v-for="item in results" :key="item.id" :item="item" />
        </div>
      </template>
    </section>
  </BaseContainer>
</template>
