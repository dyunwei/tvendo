<script setup lang="ts" generic="T">
import { computed, ref, watch } from "vue";

const props = defineProps<{
  items: readonly T[];
  resetKey?: unknown;
  note?: string;
  label?: string;
}>();
const pageSize = 10;
const page = ref(1);
const pages = computed(() =>
  Math.max(1, Math.ceil(props.items.length / pageSize)),
);
const rows = computed(() =>
  props.items.slice((page.value - 1) * pageSize, page.value * pageSize),
);
const start = computed(() =>
  props.items.length ? (page.value - 1) * pageSize + 1 : 0,
);
const end = computed(() => Math.min(page.value * pageSize, props.items.length));
watch(
  () => props.resetKey,
  () => {
    page.value = 1;
  },
  { flush: "sync" },
);
watch(
  pages,
  (count) => {
    page.value = Math.min(page.value, count);
  },
  { flush: "sync" },
);
</script>

<template>
  <div class="data-table">
    <div class="data-table-scroll">
      <slot :rows="rows" />
    </div>
    <nav class="data-table-pagination" :aria-label="label || '表格分页'">
      <div class="data-table-summary" aria-live="polite" aria-atomic="true">
        <span
          >共 {{ items.length }} 条 · 显示 {{ start }}–{{ end }} 条 · 每页
          {{ pageSize }} 条</span
        >
        <small v-if="note">{{ note }}</small>
      </div>
      <div class="data-table-controls">
        <button
          type="button"
          class="btn outline small"
          :disabled="page === 1"
          @click="page = 1"
        >
          首页
        </button>
        <button
          type="button"
          class="btn outline small"
          :disabled="page === 1"
          @click="page--"
        >
          上一页
        </button>
        <span class="data-table-page">第 {{ page }} / {{ pages }} 页</span>
        <button
          type="button"
          class="btn outline small"
          :disabled="page === pages"
          @click="page++"
        >
          下一页
        </button>
        <button
          type="button"
          class="btn outline small"
          :disabled="page === pages"
          @click="page = pages"
        >
          末页
        </button>
      </div>
    </nav>
  </div>
</template>
