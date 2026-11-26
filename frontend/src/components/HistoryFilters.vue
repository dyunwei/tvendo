<script setup lang="ts">
defineProps<{
  filter: { from: string; through: string; value: string };
  loading: boolean;
  exportUrl: string;
  label: string;
}>();
defineEmits<{ apply: [] }>();
</script>

<template>
  <form class="console-filters" @submit.prevent="$emit('apply')">
    <label>
      <span>{{ label }}</span>
      <select v-model="filter.value" class="console-control">
        <option value="">全部</option>
        <slot />
      </select>
    </label>
    <label>
      <span>开始日期</span>
      <input v-model="filter.from" class="console-control" type="date" />
    </label>
    <label>
      <span>结束日期</span>
      <input v-model="filter.through" class="console-control" type="date" />
    </label>
    <div class="console-actions">
      <button class="btn outline small console-control" :disabled="loading">
        筛选
      </button>
      <a class="btn outline small console-control" :href="exportUrl" download
        >导出 CSV</a
      >
    </div>
  </form>
</template>
