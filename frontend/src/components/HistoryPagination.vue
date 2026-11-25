<script setup lang="ts">
defineProps<{
  page: number;
  hasNext: boolean;
  loading: boolean;
  label: string;
}>();
defineEmits<{ previous: []; next: [] }>();
</script>

<template>
  <nav class="data-table-pagination" :aria-label="label">
    <button
      type="button"
      class="btn outline small"
      :disabled="loading || page === 1"
      @click="$emit('previous')"
    >
      上一页
    </button>
    <span>第 {{ page }} 页</span>
    <button
      type="button"
      class="btn outline small"
      :disabled="loading || !hasNext"
      @click="$emit('next')"
    >
      下一页
    </button>
  </nav>
</template>
