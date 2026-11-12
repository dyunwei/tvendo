<script setup lang="ts">
import { ref, computed } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ArrowRight, Eye, EyeOff, LoaderCircle, Check } from "lucide-vue-next";
import { api, session } from "./api";
const route = useRoute(),
  router = useRouter(),
  register = computed(() => route.path === "/register");
const name = ref(""),
  email = ref(""),
  password = ref(""),
  show = ref(false),
  busy = ref(false),
  error = ref("");
async function submit() {
  busy.value = true;
  error.value = "";
  try {
    const d = await api(
      "/api/auth/" + (register.value ? "register" : "login"),
      {
        method: "POST",
        body: JSON.stringify({
          name: name.value,
          email: email.value,
          password: password.value,
        }),
      },
    );
    session.user = d.user;
    session.csrf = d.csrf;
    const next = String(route.query.next || "/app");
    await router.push(
      next.startsWith("/app") || next.startsWith("/admin") ? next : "/app",
    );
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <main class="auth-page">
    <div class="auth-story">
      <span class="eyebrow blue">THE TOOL LAYER FOR YOUR AGENT</span>
      <h1>少一点集成。<br />多一点<span>可能。</span></h1>
      <p>让每一个 Agent，都拥有把想法变成现实的工具。</p>
      <div class="auth-benefits">
        <p
          v-for="s in [
            '统一 API，无需重复接入',
            '按次调用，费用清晰可见',
            '一个账户，掌握所有工具',
          ]"
          :key="s"
        >
          <Check :size="17" />{{ s }}
        </p>
      </div>
      <div class="auth-art" aria-hidden="true">
        <div class="art-orbit" />
        <span>✳</span><i>tools</i><i>agents</i><i>ideas</i>
      </div>
      <small>BUILT FOR WHAT COMES NEXT.</small>
    </div>
    <div class="auth-form-wrap">
      <form @submit.prevent="submit" class="auth-form">
        <span class="mini-label">WELCOME TO T VENDO</span>
        <h2>{{ register ? "创建你的账户" : "欢迎回来。" }}</h2>
        <p>
          {{
            register ? "连接工具，从这里开始。" : "登录账户，继续你的下一步。"
          }}
        </p>
        <div v-if="error" class="alert error" role="alert">{{ error }}</div>
        <label v-if="register"
          >姓名<input
            v-model="name"
            required
            maxlength="40"
            autocomplete="name"
            placeholder="怎么称呼你？" /></label
        ><label
          >邮箱地址<input
            v-model="email"
            type="email"
            required
            autocomplete="email"
            placeholder="you@example.com" /></label
        ><label
          >密码
          <div class="password-input">
            <input
              v-model="password"
              :type="show ? 'text' : 'password'"
              required
              minlength="10"
              maxlength="72"
              :autocomplete="register ? 'new-password' : 'current-password'"
              placeholder="至少 10 位字符"
            /><button
              type="button"
              @click="show = !show"
              :aria-label="show ? '隐藏密码' : '显示密码'"
            >
              <EyeOff v-if="show" :size="18" /><Eye v-else :size="18" />
            </button></div></label
        ><button class="btn primary full" :disabled="busy">
          <LoaderCircle v-if="busy" class="spin" :size="17" />{{
            register ? "创建账户" : "登录"
          }}<ArrowRight v-if="!busy" :size="17" />
        </button>
        <p class="auth-switch">
          {{ register ? "已经有账户？" : "还没有账户？" }}
          <RouterLink :to="register ? '/login' : '/register'"
            >{{ register ? "登录" : "立即注册" }} <ArrowRight :size="13"
          /></RouterLink>
        </p>
        <div class="auth-help">账户问题或密码重置，请联系平台管理员。</div>
      </form>
    </div>
  </main>
</template>
