<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { api } from "./api";

const route = useRoute();
const verify = computed(() => route.path === "/verify-email");
const token = computed(() => route.hash.slice(1));
const email = ref(
  typeof route.query.email === "string" ? route.query.email : "",
);
const password = ref("");
const busy = ref(false);
const message = ref("");
const error = ref("");

watch(
  () => route.fullPath,
  () => {
    message.value = "";
    error.value = "";
    password.value = "";
  },
);

async function submit() {
  busy.value = true;
  error.value = "";
  try {
    const path = verify.value ? "/api/auth/verify" : "/api/auth/reset";
    await api(token.value ? path : path + "/request", {
      method: "POST",
      body: JSON.stringify(
        token.value
          ? { token: token.value, password: password.value }
          : { email: email.value },
      ),
    });
    message.value = token.value
      ? verify.value
        ? "邮箱已验证，可以登录。"
        : "密码已重置，请使用新密码登录。"
      : "如果账户符合条件，邮件将发送到该地址。";
    password.value = "";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <main class="auth-form-wrap" style="min-height: 80vh">
    <form class="auth-form" @submit.prevent="submit">
      <h2>{{ verify ? "验证邮箱" : token ? "设置新密码" : "找回密码" }}</h2>
      <p v-if="message" class="alert success" role="status">{{ message }}</p>
      <p v-if="error" class="alert error" role="alert">{{ error }}</p>
      <template v-if="!message">
        <label v-if="!token"
          >邮箱地址
          <input v-model="email" type="email" autocomplete="email" required
        /></label>
        <label v-else-if="!verify"
          >新密码
          <input
            v-model="password"
            type="password"
            autocomplete="new-password"
            minlength="10"
            maxlength="72"
            required
          />
        </label>
        <button class="btn primary full" :disabled="busy">
          {{ busy ? "处理中…" : token ? "确认" : "发送邮件" }}
        </button>
      </template>
      <RouterLink to="/login">返回登录</RouterLink>
    </form>
  </main>
</template>
