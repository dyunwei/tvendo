<script setup lang="ts">
import { ref } from "vue";
import { api, session } from "../api";
const current = ref("");
const password = ref("");
const busy = ref(false);
const error = ref("");
const changed = ref(false);

async function submit() {
  busy.value = true;
  error.value = "";
  changed.value = false;
  try {
    const result = await api("/api/auth/password", {
      method: "POST",
      body: JSON.stringify({
        current_password: current.value,
        new_password: password.value,
      }),
    });
    session.user = result.user;
    session.csrf = result.csrf;
    current.value = "";
    password.value = "";
    changed.value = true;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <form class="auth-form" @submit.prevent="submit">
    <h2>修改密码</h2>
    <p>修改后，其他浏览器需要重新登录。</p>
    <label
      >当前密码
      <input
        v-model="current"
        type="password"
        autocomplete="current-password"
        required
    /></label>
    <label
      >新密码
      <input
        v-model="password"
        type="password"
        autocomplete="new-password"
        minlength="10"
        maxlength="72"
        required
    /></label>
    <p v-if="error" class="alert error" role="alert">{{ error }}</p>
    <p v-if="changed" class="alert success" role="status">密码已修改</p>
    <button class="btn primary" :disabled="busy">
      {{ busy ? "保存中…" : "保存密码" }}
    </button>
  </form>
</template>
