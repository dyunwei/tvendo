<script setup lang="ts">
defineProps<{ text: string | number | null | undefined }>();
</script>

<template>
  <span class="table-text" :title="String(text ?? '')">{{ text ?? "—" }}</span>
</template>
