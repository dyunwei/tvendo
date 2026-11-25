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
  <form class="history-filters" @submit.prevent="$emit('apply')">
    <label>
      {{ label }}
      <select v-model="filter.value">
        <option value="">全部</option>
        <slot />
      </select>
    </label>
    <label>开始日期 <input v-model="filter.from" type="date" /></label>
    <label>结束日期 <input v-model="filter.through" type="date" /></label>
    <button class="btn outline small" :disabled="loading">筛选</button>
    <a class="btn outline small" :href="exportUrl" download>导出 CSV</a>
  </form>
</template>

<style scoped>
.history-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: end;
  gap: 0.75rem;
  margin-bottom: 1rem;
}
.history-filters label {
  display: grid;
  gap: 0.25rem;
}
</style>
