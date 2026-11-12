<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  Rocket,
  Blocks,
  KeyRound,
  ShieldCheck,
  Database,
  History,
  BookOpen,
  Wallet,
  ChevronsUpDown,
  ArrowUpRight,
  Plus,
  Search,
  SlidersHorizontal,
  RefreshCw,
  ArrowLeft,
  Copy,
  Check,
  X,
  LogOut,
  Settings2,
  Users,
  Menu,
  Play,
  Terminal,
  LoaderCircle,
  ChevronRight,
  Receipt,
  CheckCircle2,
  AlertCircle,
} from "lucide-vue-next";
import Admin from "./Admin.vue";
import { api, session, money, date, refreshSession } from "./api";
import type { Tool, Endpoint } from "./api";
const route = useRoute(),
  router = useRouter();
const page = computed(() =>
  route.path.startsWith("/admin")
    ? "admin"
    : route.params.slug
      ? "tools"
      : String(route.params.page || "start"),
);
const menu = [
  { id: "start", label: "开始使用", icon: Rocket },
  { id: "tools", label: "工具目录", icon: Blocks },
  { id: "keys", label: "API 密钥", icon: KeyRound },
  { id: "budgets", label: "预算管理", icon: ShieldCheck },
  { id: "resources", label: "资源", icon: Database },
  { id: "usage", label: "用量记录", icon: History },
];
const mobile = ref(false),
  profile = ref(false),
  loading = ref(false),
  error = ref(""),
  notice = ref(""),
  tools = ref<Tool[]>([]),
  keys = ref<any[]>([]),
  usage = ref<any[]>([]),
  transactions = ref<any[]>([]);
const budget = ref({
  daily_limit: 3000000,
  call_limit: 300000,
  daily_enabled: false,
  call_enabled: false,
  spent: 0,
  held: 0,
});
const dailyInput = ref(3),
  callInput = ref(0.3),
  search = ref(""),
  category = ref("全部"),
  keyTab = ref("active"),
  usageTab = ref("all"),
  creating = ref(false),
  keyLabel = ref(""),
  newKey = ref(""),
  busy = ref(false),
  revokeID = ref<number | null>(null),
  copied = ref(false),
  agent = ref("通用 Agent");
const categories = computed(() => [
  "全部",
  ...new Set(tools.value.map((t) => t.category)),
]);
const filtered = computed(() =>
  tools.value.filter(
    (t) =>
      (category.value === "全部" || t.category === category.value) &&
      (
        t.name +
        " " +
        t.description +
        " " +
        t.endpoints.map((e) => e.name).join(" ")
      )
        .toLowerCase()
        .includes(search.value.toLowerCase()),
  ),
);
const tool = computed(() =>
  tools.value.find((t) => t.slug === route.params.slug),
);
const visibleKeys = computed(() =>
  keys.value.filter((k) =>
    keyTab.value === "revoked" ? k.revoked : !k.revoked,
  ),
);
const visibleUsage = computed(() =>
  usageTab.value === "running"
    ? usage.value.filter((x) => x.status === "running")
    : usage.value,
);
const base = window.location.origin,
  command = `set up ${base}/SKILL.md`;
const invocation = ref<Endpoint | null>(null),
  params = ref("{}"),
  invokeKey = ref(""),
  invokeResult = ref(""),
  invoking = ref(false);
const spentPct = computed(() =>
  budget.value.daily_limit > 0
    ? Math.min(
        100,
        (100 * (budget.value.spent + budget.value.held)) /
          budget.value.daily_limit,
      )
    : 0,
);
async function load() {
  loading.value = true;
  error.value = "";
  try {
    if (page.value === "admin") return;
    if (
      ![
        "start",
        "tools",
        "keys",
        "budgets",
        "resources",
        "usage",
        "wallet",
      ].includes(page.value)
    ) {
      await router.replace("/app");
      return;
    }
    const jobs: Promise<void>[] = [];
    if (["start", "tools", "resources"].includes(page.value))
      jobs.push(
        api<Tool[]>("/api/tools").then((d) => {
          tools.value = d;
        }),
      );
    if (["start", "keys"].includes(page.value))
      jobs.push(
        api("/api/keys").then((d) => {
          keys.value = d;
        }),
      );
    if (page.value === "usage")
      jobs.push(
        api("/api/usage").then((d) => {
          usage.value = d;
        }),
      );
    if (page.value === "wallet")
      jobs.push(
        api("/api/transactions").then((d) => {
          transactions.value = d;
        }),
        refreshSession(),
      );
    if (page.value === "budgets")
      jobs.push(
        api("/api/budgets").then((d) => {
          budget.value = d;
          dailyInput.value = d.daily_limit / 1e6;
          callInput.value = d.call_limit / 1e6;
        }),
      );
    await Promise.all(jobs);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
watch(
  () => route.fullPath,
  () => {
    mobile.value = false;
    profile.value = false;
    notice.value = "";
    load();
  },
  { immediate: true },
);
async function logout() {
  try {
    await api("/api/auth/logout", { method: "POST" });
    session.user = null;
    session.csrf = "";
    await router.push("/");
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function copy(v: string) {
  try {
    await navigator.clipboard.writeText(v);
    copied.value = true;
    setTimeout(() => (copied.value = false), 2000);
  } catch {
    error.value = "无法访问剪贴板，请手动选择复制";
  }
}
async function createKey() {
  busy.value = true;
  error.value = "";
  try {
    const d = await api("/api/keys", {
      method: "POST",
      body: JSON.stringify({ label: keyLabel.value }),
    });
    newKey.value = d.key;
    creating.value = false;
    keyLabel.value = "";
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function revoke() {
  if (revokeID.value === null) return;
  busy.value = true;
  try {
    await api("/api/keys/" + revokeID.value, { method: "DELETE" });
    revokeID.value = null;
    await load();
    notice.value = "密钥已撤销";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function saveBudget() {
  busy.value = true;
  try {
    await api("/api/budgets", {
      method: "PUT",
      body: JSON.stringify({
        ...budget.value,
        daily_limit: Math.round(dailyInput.value * 1e6),
        call_limit: Math.round(callInput.value * 1e6),
      }),
    });
    await load();
    notice.value = "预算设置已保存";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
function openInvoke(ep: Endpoint) {
  invocation.value = ep;
  params.value = JSON.stringify(ep.parameters || {}, null, 2);
  invokeKey.value = "";
  invokeResult.value = "";
}
async function invoke() {
  if (!invocation.value || !tool.value) return;
  invoking.value = true;
  invokeResult.value = "";
  try {
    const parameters = JSON.parse(params.value);
    if (
      !parameters ||
      typeof parameters !== "object" ||
      Array.isArray(parameters)
    )
      throw new Error("参数必须是 JSON 对象");
    const d = await api("/v1/run", {
      method: "POST",
      headers: { Authorization: "Bearer " + invokeKey.value },
      body: JSON.stringify({
        tool: tool.value.slug,
        endpoint: invocation.value.id,
        parameters,
      }),
    });
    invokeResult.value = JSON.stringify(d, null, 2);
    await refreshSession();
  } catch (e) {
    invokeResult.value = (e as Error).message;
  } finally {
    invoking.value = false;
  }
}
const statusLabel = (s: string) =>
  ({ success: "成功", failed: "失败 · 已退款", running: "执行中" })[s] || s;
</script>
<template>
  <div class="console-shell">
    <button class="mobile-menu" @click="mobile = !mobile" aria-label="切换导航">
      <Menu :size="22" />
    </button>
    <div v-if="mobile" class="sidebar-backdrop" @click="mobile = false" />
    <aside class="sidebar" :class="{ visible: mobile }">
      <RouterLink to="/" class="brand console-brand"
        ><span class="brand-mark">t</span>T vendo<span class="asterisk"
          >✳</span
        ></RouterLink
      >
      <div class="workspace">
        <span class="avatar violet">{{
          session.user?.name.slice(0, 1).toUpperCase()
        }}</span>
        <div>
          <strong>{{ session.user?.name }} 的空间</strong
          ><small>个人工作空间</small>
        </div>
        <ChevronsUpDown :size="15" />
      </div>
      <span class="nav-caption">WORKSPACE</span>
      <nav class="side-nav">
        <RouterLink
          v-for="item in menu"
          :key="item.id"
          :to="item.id === 'start' ? '/app' : '/app/' + item.id"
          :class="{ active: page === item.id }"
          ><component :is="item.icon" :size="18" />{{ item.label
          }}<span v-if="item.id === 'tools'" class="nav-new"
            >EXPLORE</span
          ></RouterLink
        >
      </nav>
      <template v-if="session.user?.role === 'admin'"
        ><span class="nav-caption admin-caption">ADMINISTRATION</span>
        <nav class="side-nav">
          <RouterLink
            to="/admin/tools"
            :class="{
              active:
                page === 'admin' &&
                route.params.section !== 'users' &&
                route.params.section !== 'audit',
            }"
            ><Settings2 :size="18" />上游工具配置</RouterLink
          ><RouterLink
            to="/admin/users"
            :class="{ active: route.params.section === 'users' }"
            ><Users :size="18" />用户与充值</RouterLink
          ><RouterLink
            to="/admin/audit"
            :class="{ active: route.params.section === 'audit' }"
            ><ShieldCheck :size="18" />操作日志</RouterLink
          >
        </nav></template
      >
      <div class="sidebar-bottom">
        <RouterLink to="/docs" class="sidebar-docs"
          ><BookOpen :size="17" />使用文档<ArrowUpRight
            :size="13" /></RouterLink
        ><RouterLink
          to="/app/wallet"
          class="balance-box"
          :class="{ chosen: page === 'wallet' }"
          ><Wallet :size="28" />
          <div>
            <small>AVAILABLE BALANCE</small
            ><strong>{{ money(session.user?.balance || 0) }}</strong>
          </div>
          <ChevronRight :size="16"
        /></RouterLink>
        <div class="profile-wrap">
          <button @click="profile = !profile" class="profile">
            <span class="avatar ocean">{{
              session.user?.name.slice(0, 1).toUpperCase()
            }}</span><span
              >{{ session.user?.name
              }}<small>{{
                session.user?.role === "admin" ? "管理员" : "个人账户"
              }}</small></span
            ><ChevronsUpDown :size="15" />
          </button>
          <div v-if="profile" class="profile-popup">
            <small>{{ session.user?.email }}</small><button @click="logout">
              <LogOut :size="15" />退出登录
            </button>
          </div>
        </div>
      </div>
    </aside>
    <main class="console-main">
      <div class="console-topbar">
        <span
          >工作空间 <ChevronRight :size="12" />
          {{
            page === "admin"
              ? "管理后台"
              : page === "wallet"
                ? "钱包"
                : menu.find((x) => x.id === page)?.label
          }}</span
        ><span class="environment"><i />所有服务由管理员统一配置</span>
      </div>
      <div
        class="console-content"
        :class="{ 'start-content': page === 'start' }"
      >
        <div v-if="error" class="alert error" role="alert">
          <AlertCircle :size="17" />{{ error
          }}<button class="icon-btn" @click="error = ''" aria-label="关闭错误">
            <X :size="15" />
          </button>
        </div>
        <div v-if="notice" class="alert success" role="status">
          <CheckCircle2 :size="17" />{{ notice }}
        </div>
        <div v-if="loading" class="loading-line" role="status">
          <LoaderCircle :size="15" class="spin" />正在加载…
        </div>
        <Admin v-if="page === 'admin'" />
        <template v-else-if="page === 'start'"
          ><div class="welcome">
            <span class="eyebrow blue">YOUR NEXT IDEA STARTS HERE</span>
            <h1>欢迎来到 T vendo<span>✳</span></h1>
            <p>一个连接。Agent 所需的每一种工具。</p>
          </div>
          <section class="setup-card">
            <header>
              <span class="number">1</span>
              <h3>连接你的 Agent</h3>
              <select v-model="agent" aria-label="选择 Agent">
                <option>通用 Agent</option>
                <option>OpenClaw</option>
                <option>Claude Code</option>
                <option>Codex</option></select
              ><RouterLink to="/docs?tab=skill"
                ><BookOpen :size="15" />接入文档<ArrowUpRight :size="13"
              /></RouterLink>
            </header>
            <div class="setup-body">
              <div class="setup-tab"><Rocket :size="16" />Skill</div>
              <p>配置 T vendo 与 {{ agent }}</p>
              <p class="text-dark">在你的 Agent 对话中发送：</p>
              <div class="code-copy">
                <code>{{ command }}</code><button
                  class="icon-btn"
                  @click="copy(command)"
                  :aria-label="copied ? '已复制' : '复制命令'"
                >
                  <Check v-if="copied" :size="16" /><Copy v-else :size="16" />
                </button>
              </div>
            </div>
          </section>
          <section class="setup-card">
            <header>
              <span class="number">2</span>
              <h3>给 Agent 一把 API 密钥</h3>
            </header>
            <div class="setup-body">
              <p>创建平台密钥，授权 Agent 发现和调用工具。</p>
              <div class="key-preview">
                <KeyRound :size="17" /><code>{{
                  keys.find((k) => !k.revoked)?.prefix
                    ? keys.find((k) => !k.revoked).prefix + "••••••••"
                    : "还没有 API 密钥"
                }}</code
                ><RouterLink to="/app/keys" class="btn outline small"
                  ><Settings2 :size="14" />{{
                    keys.some((k) => !k.revoked) ? "管理密钥" : "创建密钥"
                  }}</RouterLink
                >
              </div>
            </div>
          </section>
          <section class="setup-card">
            <header>
              <span class="number">3</span>
              <h3>准备好，开始探索</h3>
            </header>
            <div class="setup-body try-row">
              <div>
                <p class="text-dark">下一个出色的想法，值得一个好工具。</p>
                <p>浏览工具目录，查看接口和价格，然后开始调用。</p>
              </div>
              <RouterLink to="/app/tools" class="btn primary"
                >探索工具<ArrowUpRight :size="16"
              /></RouterLink>
            </div></section
        ></template>
        <template v-else-if="page === 'tools' && !route.params.slug"
          ><div class="page-heading">
            <div>
              <h1>
                工具目录<span class="heading-count">{{ tools.length }}</span>
              </h1>
              <p>发现适合你的下一个工具，所有接口即连即用。</p>
            </div>
            <span class="subtle-tag"><Blocks :size="13" />统一 API 接入</span>
          </div>
          <div class="search-box">
            <Search :size="20" /><input
              v-model="search"
              placeholder="搜索工具、功能或接口…"
              aria-label="搜索工具"
            /><kbd>/</kbd>
          </div>
          <div class="tools-layout">
            <nav class="categories">
              <span class="nav-caption">CATEGORIES</span
              ><button
                v-for="c in categories"
                :key="c"
                :class="{ active: category === c }"
                @click="category = c"
              >
                <Blocks v-if="c === '全部'" :size="15" /><Search
                  v-else
                  :size="15"
                /><span>{{ c }}</span
                ><small>{{
                  c === "全部"
                    ? tools.length
                    : tools.filter((t) => t.category === c).length
                }}</small>
              </button>
            </nav>
            <div v-if="filtered.length" class="tools-grid">
              <RouterLink
                v-for="t in filtered"
                :key="t.id"
                :to="'/app/tools/' + t.slug"
                class="tool-card"
                ><div class="tool-card-title">
                  <span class="tool-icon">{{ t.icon }}</span>
                  <h3>{{ t.name }}</h3>
                  <ArrowUpRight :size="16" />
                </div>
                <p>{{ t.description }}</p>
                <footer>
                  <span>{{ t.endpoints.length }} 个接口</span
                  ><span
                    v-if="
                      t.endpoints.length &&
                      t.endpoints.every((e) => e.price_micros === 0)
                    "
                    class="badge blue-badge"
                    >FREE</span
                  ><span v-else class="badge green-badge">按次计费</span>
                </footer></RouterLink
              >
            </div>
            <div v-else-if="!loading" class="empty-state panel">
              <Blocks :size="36" />
              <h3>{{ tools.length ? "没有匹配的工具" : "工具即将就绪" }}</h3>
              <p>
                {{
                  tools.length
                    ? "试试其他关键词或分类。"
                    : "管理员启用上游工具后，将会显示在这里。"
                }}
              </p>
              <RouterLink
                v-if="session.user?.role === 'admin' && !tools.length"
                to="/admin/tools"
                class="btn outline"
                >配置第一个工具<ArrowUpRight :size="15" /></RouterLink
              ><button
                v-else-if="tools.length"
                class="btn outline"
                @click="
                  search = '';
                  category = '全部';
                "
              >
                清空筛选
              </button>
            </div>
          </div></template
        >
        <template v-else-if="page === 'tools' && route.params.slug"
          ><template v-if="tool"
            ><RouterLink to="/app/tools" class="back-link"
              ><ArrowLeft :size="16" />返回工具目录</RouterLink
            >
            <div class="tool-detail-head">
              <span class="subtle-tag mono"
                >{{ tool.endpoints.length }} ENDPOINTS ·
                {{ tool.resources.length }} RESOURCES</span
              >
              <h1>
                <span class="tool-icon">{{ tool.icon }}</span>{{ tool.name
                }}<span class="badge green-badge">按次计费</span>
              </h1>
              <div class="tool-links">
                <a
                  v-if="tool.website"
                  :href="tool.website"
                  target="_blank"
                  rel="noopener"
                  >官方网站<ArrowUpRight :size="13" /></a
                ><a
                  v-if="tool.docs"
                  :href="tool.docs"
                  target="_blank"
                  rel="noopener"
                  >上游文档<ArrowUpRight :size="13"
                /></a>
              </div>
              <p>{{ tool.description }}</p>
            </div>
            <template v-if="tool.resources.length"
              ><h4 class="section-label">RESOURCES <span>资源说明</span></h4>
              <div class="resource-grid">
                <div
                  v-for="resource in tool.resources"
                  :key="resource.name"
                  class="endpoint-card"
                >
                  <span class="endpoint-provider"
                    >{{ tool.icon }} {{ tool.name
                    }}<span class="badge">资源说明</span></span
                  >
                  <h3>{{ resource.name }}</h3>
                  <p>{{ resource.description }}</p>
                  <strong>{{ resource.price }}</strong><small
                    class="resource-note"
                    >仅展示配置说明，当前版本不提供资源租赁。</small
                  >
                </div>
              </div></template
            ><template v-for="free in [true, false]" :key="String(free)"
              ><h4
                v-if="
                  tool.endpoints.some((e) => (e.price_micros === 0) === free)
                "
                class="section-label"
              >
                {{ free ? "FREE ENDPOINTS" : "ENDPOINTS" }}
                <span>{{ free ? "免费接口" : "按次调用接口" }}</span>
              </h4>
              <div class="endpoint-grid">
                <button
                  v-for="ep in tool.endpoints.filter(
                    (e) => (e.price_micros === 0) === free,
                  )"
                  :key="ep.id"
                  class="endpoint-card"
                  @click="openInvoke(ep)"
                >
                  <span class="endpoint-provider"
                    >{{ tool.icon }} {{ tool.name }} <ArrowUpRight :size="14"
                  /></span>
                  <h3>{{ ep.name }}</h3>
                  <code
                    ><span
                      class="method tiny"
                      :class="{ post: ep.method !== 'GET' }"
                      >{{ ep.method }}</span
                    >
                    {{ ep.path }}</code
                  >
                  <p>{{ ep.description }}</p>
                  <footer>
                    <strong>{{
                      ep.price_micros === 0 ? "Free" : money(ep.price_micros)
                    }}</strong><small>/ 次调用</small
                    ><span v-if="free" class="badge blue-badge">FREE</span
                    ><span v-else class="endpoint-call"
                      >试用<Play :size="12"
                    /></span>
                  </footer>
                </button></div></template
          ></template>
          <div v-else-if="!loading" class="empty-state panel">
            <Search :size="36" />
            <h3>工具不可用</h3>
            <p>该工具不存在或已被管理员停用。</p>
            <RouterLink to="/app/tools" class="btn outline"
              >返回工具目录</RouterLink
            >
          </div></template
        >
        <template v-else-if="page === 'keys'"
          ><div class="page-heading">
            <div>
              <h1>API 密钥</h1>
              <p>为你的 Agent 和应用管理访问凭证。</p>
            </div>
            <button class="btn dark" @click="creating = true">
              <Plus :size="17" />创建 API Key
            </button>
          </div>
          <div class="tabs">
            <button
              @click="keyTab = 'active'"
              :class="{ active: keyTab === 'active' }"
            >
              使用中
              <small>{{ keys.filter((k) => !k.revoked).length }}</small></button
            ><button
              @click="keyTab = 'revoked'"
              :class="{ active: keyTab === 'revoked' }"
            >
              已撤销
            </button>
          </div>
          <div class="panel table-wrap">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>密钥</th>
                  <th>权限</th>
                  <th>创建时间</th>
                  <th>最后使用</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                <tr v-for="key in visibleKeys" :key="key.id">
                  <td class="text-dark">{{ key.label }}</td>
                  <td class="mono">{{ key.prefix }}••••••••</td>
                  <td>
                    <span class="permission">tools:read</span>
                    <span class="permission">runs:create</span>
                  </td>
                  <td>{{ date(key.created_at) }}</td>
                  <td>{{ date(key.last_used) }}</td>
                  <td>
                    <button
                      v-if="!key.revoked"
                      class="danger-link"
                      @click="revokeID = key.id"
                    >
                      撤销</button
                    ><span v-else class="badge">已撤销</span>
                  </td>
                </tr>
                <tr v-if="!visibleKeys.length">
                  <td colspan="6" class="empty-cell">
                    {{
                      keyTab === "active"
                        ? "还没有 API Key，创建一个开始接入。"
                        : "暂无已撤销的密钥。"
                    }}
                  </td>
                </tr>
              </tbody>
            </table>
            <div class="table-footer">
              共 {{ visibleKeys.length }} 个密钥<span
                >完整密钥只在创建时显示一次</span
              >
            </div>
          </div>
          <div class="tip">
            <ShieldCheck
              :size="17"
            />密钥拥有当前账户的工具调用权限。请勿在浏览器代码或公开仓库中存储密钥。
          </div></template
        >
        <template v-else-if="page === 'budgets'"
          ><div class="page-heading">
            <div>
              <h1>预算管理</h1>
              <p>给每一次探索，设定一个安心的边界。</p>
            </div>
            <span class="subtle-tag"
              ><ShieldCheck :size="14" />账户级消费控制</span
            >
          </div>
          <form @submit.prevent="saveBudget">
            <div class="budget-grid">
              <section class="panel budget-card">
                <header>
                  <span><span class="sun-symbol">☼</span>每日预算</span
                  ><label class="toggle-label"
                    ><small>{{
                      budget.daily_enabled ? "已启用" : "未启用"
                    }}</small
                    ><input
                      type="checkbox"
                      class="toggle"
                      v-model="budget.daily_enabled"
                      aria-label="启用每日预算"
                  /></label>
                </header>
                <div class="budget-amount">
                  <span>$</span
                  ><input
                    v-model.number="dailyInput"
                    type="number"
                    min="0"
                    max="1000000"
                    step="0.000001"
                    required
                    aria-label="每日预算金额"
                  /><small>/ 天</small>
                </div>
                <p>
                  已用 {{ money(budget.spent) }} / 预留 {{ money(budget.held) }}
                  <span>{{ Math.round(spentPct) }}%</span>
                </p>
                <div class="progress">
                  <i :style="{ width: spentPct + '%' }" />
                </div>
                <small class="reset-time"
                  >每日 UTC 00:00（北京时间 08:00）重置</small
                >
              </section>
              <section class="panel budget-card">
                <header>
                  <span><ShieldCheck :size="19" />单次调用上限</span
                  ><label class="toggle-label"
                    ><small>{{
                      budget.call_enabled ? "已启用" : "未启用"
                    }}</small
                    ><input
                      type="checkbox"
                      class="toggle"
                      v-model="budget.call_enabled"
                      aria-label="启用单次调用限制"
                  /></label>
                </header>
                <div class="budget-amount">
                  <span>$</span
                  ><input
                    v-model.number="callInput"
                    type="number"
                    min="0"
                    max="1000000"
                    step="0.000001"
                    required
                    aria-label="单次调用上限"
                  /><small>/ 次</small>
                </div>
                <p>超过上限的接口将在调用前被拦截。</p>
              </section>
            </div>
            <div class="budget-save">
              <span>所有 API Key 共享这些预算限制。</span
              ><button class="btn dark" :disabled="busy">
                {{ busy ? "保存中…" : "保存设置" }}
              </button>
            </div>
          </form></template
        >
        <template v-else-if="page === 'resources'"
          ><div class="page-heading">
            <div>
              <h1>资源</h1>
              <p>查看各工具提供的资源说明。</p>
            </div>
          </div>
          <div class="alert">
            <Database
              :size="17"
            />第一版支持同步工具调用。云浏览器、持久化会话等资源仅展示说明，暂不支持创建和租赁。
          </div>
          <div
            v-if="tools.some((t) => t.resources.length)"
            class="resource-grid"
          >
            <template v-for="t in tools" :key="t.id"
              ><div
                v-for="r in t.resources"
                :key="r.name"
                class="endpoint-card"
              >
                <span class="endpoint-provider">{{ t.icon }} {{ t.name }}</span>
                <h3>{{ r.name }}</h3>
                <p>{{ r.description }}</p>
                <strong>{{ r.price }}</strong>
              </div></template
            >
          </div>
          <div v-else-if="!loading" class="panel empty-state">
            <Database :size="40" />
            <h3>暂无资源说明</h3>
            <p>工具配置中的资源说明将显示在这里。</p>
          </div></template
        >
        <template v-else-if="page === 'usage'"
          ><div class="page-heading">
            <div>
              <h1>用量记录</h1>
              <p>每一次调用，都有迹可循。</p>
            </div>
            <button class="btn outline" @click="load" :disabled="loading">
              <RefreshCw :size="15" :class="{ spin: loading }" />刷新
            </button>
          </div>
          <div class="tabs">
            <button
              :class="{ active: usageTab === 'all' }"
              @click="usageTab = 'all'"
            >
              全部</button
            ><button
              :class="{ active: usageTab === 'running' }"
              @click="usageTab = 'running'"
            >
              执行中
            </button>
          </div>
          <div v-if="visibleUsage.length" class="panel table-wrap">
            <table>
              <thead>
                <tr>
                  <th>调用编号</th>
                  <th>工具 / 接口</th>
                  <th>状态</th>
                  <th>费用</th>
                  <th>耗时</th>
                  <th>时间</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="run in visibleUsage" :key="run.id">
                  <td class="mono">#{{ run.id }}</td>
                  <td>
                    <strong class="text-dark">{{ run.tool_name }}</strong><small
                      class="cell-sub mono"
                      >{{ run.endpoint }}</small
                    >
                  </td>
                  <td>
                    <span class="status" :class="run.status"
                      ><i />{{ statusLabel(run.status) }}</span
                    >
                  </td>
                  <td>
                    {{ run.status === "failed" ? money(0) : money(run.cost)
                    }}<small v-if="run.status === 'running'" class="cell-sub"
                      >预留金额</small
                    >
                  </td>
                  <td>{{ run.duration_ms }} ms</td>
                  <td>{{ date(run.created_at) }}</td>
                </tr>
              </tbody>
            </table>
            <div class="table-footer">
              显示最近 200 次调用中的 {{ visibleUsage.length }} 条
            </div>
          </div>
          <div v-else-if="!loading" class="panel empty-state">
            <History :size="42" />
            <h3>
              {{
                usageTab === "running" ? "没有执行中的调用" : "还没有调用记录"
              }}
            </h3>
            <p>通过 API 执行工具后，调用状态和费用会显示在这里。</p>
            <RouterLink to="/app/tools" class="btn outline"
              >探索工具 <ArrowUpRight :size="14"
            /></RouterLink></div
        ></template>
        <template v-else-if="page === 'wallet'"
          ><div class="page-heading">
            <div>
              <h1>钱包</h1>
              <p>一个余额，连接所有工具。</p>
            </div>
          </div>
          <div class="panel wallet-card">
            <header>
              <span>可用余额</span
              ><button class="icon-btn" @click="load" aria-label="刷新余额">
                <RefreshCw :size="16" />
              </button>
            </header>
            <strong>{{ money(session.user?.balance || 0) }}</strong><button
              class="btn dark"
              @click="
                notice =
                  '请联系平台管理员，提供你的注册邮箱 ' +
                  session.user?.email +
                  '。管理员入账后，记录将显示在下方。'
              "
            >
              <Plus :size="16" />充值
            </button>
            <div class="wallet-bottom">
              <ShieldCheck :size="17" /><span
                >人工充值<small
                  >联系管理员为账户入账，支持填写充值备注。</small
                ></span
              >
            </div>
          </div>
          <div class="section-heading">
            <h3>充值记录</h3>
            <span>最近 200 条账目中的充值</span>
          </div>
          <div
            v-if="transactions.some((t) => t.kind === 'topup')"
            class="panel table-wrap"
          >
            <table>
              <thead>
                <tr>
                  <th>流水号</th>
                  <th>入账金额</th>
                  <th>备注</th>
                  <th>时间</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="t in transactions.filter((x) => x.kind === 'topup')"
                  :key="t.id"
                >
                  <td>#{{ t.id }}</td>
                  <td class="positive">+{{ money(t.amount) }}</td>
                  <td>{{ t.note || "管理员充值" }}</td>
                  <td>{{ date(t.created_at) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="panel empty-state compact">
            <Receipt :size="38" />
            <h3>还没有充值记录</h3>
            <p>管理员为你充值后，入账记录将显示在这里。</p>
          </div>
          <div class="section-heading">
            <h3>消费记录</h3>
            <RouterLink to="/app/usage"
              >查看用量 <ArrowUpRight :size="14"
            /></RouterLink>
          </div>
          <div
            v-if="transactions.some((t) => t.kind === 'usage')"
            class="panel table-wrap"
          >
            <table>
              <thead>
                <tr>
                  <th>流水号</th>
                  <th>消费金额</th>
                  <th>关联调用</th>
                  <th>时间</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="t in transactions.filter((x) => x.kind === 'usage')"
                  :key="t.id"
                >
                  <td>#{{ t.id }}</td>
                  <td>{{ money(t.amount) }}</td>
                  <td>{{ t.note }}</td>
                  <td>{{ date(t.created_at) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="panel empty-state compact">
            <History :size="38" />
            <h3>还没有消费记录</h3>
            <p>成功调用工具后的扣费将在这里显示。</p>
          </div></template
        >
      </div>
      <footer class="console-footer">
        <span>T vendo · The tool layer for your agent.</span
        ><RouterLink to="/docs"
          >需要帮助？查看文档 <ArrowUpRight :size="12"
        /></RouterLink>
      </footer>
    </main>
  </div>
  <Teleport to="body"
    ><div
      v-if="creating || newKey || revokeID !== null || invocation"
      class="modal-backdrop"
      @click.self="
        !busy &&
        !invoking &&
        ((creating = false),
        (newKey = ''),
        (revokeID = null),
        (invocation = null))
      "
    >
      <section
        class="modal"
        :class="{ wide: invocation }"
        role="dialog"
        aria-modal="true"
        aria-label="密钥或接口操作"
      >
        <button
          class="modal-close icon-btn"
          :disabled="busy || invoking"
          @click="
            creating = false;
            newKey = '';
            revokeID = null;
            invocation = null;
          "
          aria-label="关闭"
        >
          <X :size="20" />
        </button>
        <form v-if="creating" @submit.prevent="createKey">
          <span class="modal-icon"><KeyRound /></span>
          <h2>创建 API Key</h2>
          <p>给密钥起一个容易识别的名字。</p>
          <label
            >名称<input
              v-model="keyLabel"
              required
              maxlength="80"
              placeholder="例如：Research Agent"
              autofocus /></label
          ><button class="btn dark full" :disabled="busy">
            {{ busy ? "创建中…" : "创建密钥" }}
          </button>
        </form>
        <template v-else-if="newKey"
          ><span class="modal-icon success-icon"><CheckCircle2 /></span>
          <h2>密钥已创建</h2>
          <p>完整密钥仅展示这一次。请复制并保存到安全的地方。</p>
          <div class="secret-display">
            <code>{{ newKey }}</code>
          </div>
          <button class="btn primary full" @click="copy(newKey)">
            <Check v-if="copied" :size="16" /><Copy v-else :size="16" />{{
              copied ? "已复制" : "复制密钥"
            }}</button
          ><button class="btn outline full" @click="newKey = ''">
            我已妥善保存
          </button></template
        ><template v-else-if="revokeID !== null"
          ><span class="modal-icon danger"><ShieldCheck /></span>
          <h2>撤销这把密钥？</h2>
          <p>撤销会立即阻止新的调用，且无法恢复。正在执行的请求仍会结算。</p>
          <div class="modal-actions">
            <button class="btn outline" @click="revokeID = null">取消</button
            ><button class="btn danger-btn" @click="revoke" :disabled="busy">
              确认撤销
            </button>
          </div></template
        >
        <form v-else-if="invocation" @submit.prevent="invoke">
          <span class="eyebrow blue">API PLAYGROUND</span>
          <h2>{{ invocation.name }}</h2>
          <p>
            单次调用价格
            <strong>{{
              money(invocation.price_micros)
            }}</strong>。点击执行后将调用真实上游并结算费用。
          </p>
          <label
            >平台 API Key<input
              v-model="invokeKey"
              type="password"
              required
              autocomplete="off"
              placeholder="tv_live_…（仅在本次弹窗使用）" /></label
          ><label
            >请求参数（JSON）<textarea
              v-model="params"
              class="mono"
              rows="7"
              spellcheck="false"
              required
            />
          </label>
          <div class="modal-actions">
            <button
              type="button"
              class="btn outline"
              @click="
                copy(
                  `curl ${base}/v1/run -H 'Authorization: Bearer $TVENDO_API_KEY' -H 'Content-Type: application/json' -d '${JSON.stringify({ tool: tool?.slug, endpoint: invocation.id, parameters: invocation.parameters || {} }).replace(/'/g, `'\\''`)}'`,
                )
              "
            >
              <Copy :size="14" />复制 cURL</button
            ><button class="btn primary" :disabled="invoking">
              <LoaderCircle v-if="invoking" :size="15" class="spin" /><Play
                v-else
                :size="15"
              />{{ invoking ? "正在调用…" : "执行并按次付费" }}
            </button>
          </div>
          <pre v-if="invokeResult" class="result-code">{{ invokeResult }}</pre>
        </form>
      </section>
    </div></Teleport
  >
</template>
