import { onMounted, onUnmounted } from "vue";

export function useEscapeClose(close: () => void) {
  function onKeydown(event: KeyboardEvent) {
    if (event.key === "Escape" && !event.isComposing && !event.defaultPrevented) {
      close();
    }
  }

  onMounted(() => window.addEventListener("keydown", onKeydown));
  onUnmounted(() => window.removeEventListener("keydown", onKeydown));
}
