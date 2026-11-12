<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";
import { ArrowUpRight } from "lucide-vue-next";
import { session } from "./api";
const route = useRoute();
const publicPage = computed(
  () => !route.path.startsWith("/app") && !route.path.startsWith("/admin"),
);
</script>
<template>
  <header v-if="publicPage" class="public-header">
    <div class="nav-inner">
      <RouterLink to="/" class="brand"
        ><span class="brand-mark">t</span>T vendo<span class="asterisk"
          >✳</span
        ></RouterLink
      >
      <nav>
        <RouterLink to="/" :class="{ selected: route.path === '/' }"
          >Home</RouterLink
        ><RouterLink to="/docs" active-class="selected">Docs</RouterLink>
      </nav>
      <div class="nav-actions">
        <RouterLink v-if="!session.user" to="/login" class="login-link"
          >登录</RouterLink
        ><RouterLink :to="session.user ? '/app' : '/register'" class="btn soft"
          >{{ session.user ? "进入控制台" : "开始使用"
          }}<ArrowUpRight :size="16"
        /></RouterLink>
      </div>
    </div>
  </header>
  <RouterView />
  <footer
    v-if="publicPage && route.path != '/login' && route.path != '/register'"
    class="public-footer"
  >
    <div class="footer-top">
      <div>
        <RouterLink to="/" class="brand"
          >T vendo<span class="asterisk">✳</span></RouterLink
        >
        <p>一个 API，连接 Agent 所需的工具。<br />发现、调用、按需付费。</p>
        <small>© {{ new Date().getFullYear() }} T vendo</small>
      </div>
      <div>
        <span class="eyebrow">PRODUCT</span><RouterLink to="/">Home</RouterLink
        ><RouterLink to="/docs">Docs</RouterLink
        ><RouterLink to="/app">控制台 ↗</RouterLink>
      </div>
      <div>
        <span class="eyebrow">GET CONNECTED</span
        ><a href="/SKILL.md">Skill 文档 ↗</a
        ><RouterLink to="/docs#api">API 参考</RouterLink
        ><RouterLink to="/register">创建账户</RouterLink>
      </div>
    </div>
    <div class="footer-word">T vendo<span>✳</span></div>
  </footer>
</template>
