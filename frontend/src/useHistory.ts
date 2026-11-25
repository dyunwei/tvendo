import { computed, reactive, ref } from "vue";
import { api } from "./api";

export function useHistory<T>(path: string, filterName: "status" | "kind") {
  const items = ref<T[]>([]);
  const filter = reactive({ from: "", through: "", value: "" });
  const applied = ref(new URLSearchParams());
  const cursors = ref([""]);
  const next = ref("");
  const loading = ref(false);
  const error = ref("");
  const exportURL = computed(() => {
    const params = new URLSearchParams(applied.value);
    params.set("format", "csv");
    return path + "?" + params.toString();
  });

  async function load(pages = cursors.value, filters = applied.value) {
    if (loading.value) return;
    loading.value = true;
    error.value = "";
    try {
      const params = new URLSearchParams(filters);
      params.set("limit", "25");
      const cursor = pages.at(-1);
      if (cursor) params.set("cursor", cursor);
      const data = await api<{ items: T[]; next_cursor: string }>(
        path + "?" + params.toString(),
      );
      items.value = data.items;
      next.value = data.next_cursor;
      cursors.value = pages;
      applied.value = filters;
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      loading.value = false;
    }
  }

  function applyFilters() {
    if (filter.from && filter.through && filter.from > filter.through) {
      error.value = "结束日期必须不早于开始日期";
      return;
    }
    const params = new URLSearchParams();
    if (filter.value) params.set(filterName, filter.value);
    if (filter.from) {
      params.set("from", new Date(filter.from + "T00:00:00").toISOString());
    }
    if (filter.through) {
      const end = new Date(filter.through + "T00:00:00");
      end.setDate(end.getDate() + 1);
      params.set("before", end.toISOString());
    }
    return load([""], params);
  }

  return reactive({
    items,
    filter,
    loading,
    error,
    exportURL,
    page: computed(() => cursors.value.length),
    hasNext: computed(() => next.value !== ""),
    load: () => load(),
    applyFilters,
    previous: () => load(cursors.value.slice(0, -1)),
    forward: () => load([...cursors.value, next.value]),
  });
}
