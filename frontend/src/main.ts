import { createApp } from "vue";
import { createRouter, createWebHistory } from "vue-router";
import App from "./App.vue";
import Landing from "./Landing.vue";
import Docs from "./Docs.vue";
import Auth from "./Auth.vue";
import Console from "./Console.vue";
import { session, refreshSession } from "./api";
import "./style.css";
const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: "/", component: Landing },
    { path: "/docs", component: Docs },
    { path: "/login", component: Auth },
    { path: "/register", component: Auth },
    { path: "/app/:page?", component: Console },
    { path: "/app/tools/:slug", component: Console },
    { path: "/admin/:section?", component: Console },
    { path: "/:pathMatch(.*)*", redirect: "/" },
  ],
});
router.beforeEach(async (to) => {
  if (!session.ready) await refreshSession();
  if (to.path.startsWith("/app") || to.path.startsWith("/admin")) {
    if (!session.user) return { path: "/login", query: { next: to.fullPath } };
    if (to.path.startsWith("/admin") && session.user.role !== "admin")
      return "/app";
  }
  if ((to.path === "/login" || to.path === "/register") && session.user)
    return "/app";
});
createApp(App).use(router).mount("#app");
