<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { useRoute } from "vue-router";
import {
  Plus,
  Settings2,
  X,
  ArrowUpRight,
  Search,
  Wallet,
  ShieldCheck,
  Save,
  Trash2,
  Copy,
  RefreshCw,
  LoaderCircle,
  AlertCircle,
  CheckCircle2,
} from "lucide-vue-next";
import { api, money, date, refreshSession } from "./api";
import type { Tool, Endpoint } from "./api";
const route = useRoute(),
  section = computed(() => String(route.params.section || "tools"));
const tools = ref<Tool[]>([]),
  users = ref<any[]>([]),
  audit = ref<any[]>([]),
  loading = ref(false),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  search = ref(""),
  editing = ref<Tool | null>(null),
  endpointText = ref(""),
  resourceText = ref(""),
  secret = ref(""),
  replaceSecret = ref(false),
  editorTab = ref("general"),
  topupUser = ref<any>(null),
  amount = ref(10),
  note = ref("");
const shownUsers = computed(() =>
  users.value.filter((u) =>
    (u.email + " " + u.name).toLowerCase().includes(search.value.toLowerCase()),
  ),
);
const shownTools = computed(() =>
  tools.value.filter((t) =>
    (t.name + " " + t.category)
      .toLowerCase()
      .includes(search.value.toLowerCase()),
  ),
);
async function load() {
  loading.value = true;
  error.value = "";
  try {
    if (section.value === "users") users.value = await api("/api/admin/users");
    else if (section.value === "audit")
      audit.value = await api("/api/admin/audit");
    else tools.value = await api("/api/admin/tools");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
watch(
  () => section.value,
  () => {
    search.value = "";
    notice.value = "";
    load();
  },
  { immediate: true },
);
function edit(t?: Tool) {
  editing.value = t
    ? JSON.parse(JSON.stringify(t))
    : {
        id: 0,
        slug: "",
        name: "",
        category: "搜索与 SEO",
        description: "",
        website: "",
        docs: "",
        icon: "✦",
        base_url: "",
        auth_header: "Authorization",
        auth_prefix: "Bearer ",
        enabled: false,
        endpoints: [],
        resources: [],
      };
  secret.value = "";
  replaceSecret.value = false;
  endpointText.value = JSON.stringify(editing.value!.endpoints, null, 2);
  resourceText.value = JSON.stringify(editing.value!.resources, null, 2);
  editorTab.value = "general";
  error.value = "";
}
function addEndpoint() {
  try {
    const eps = JSON.parse(endpointText.value) as Endpoint[];
    if (!Array.isArray(eps)) throw new Error();
    eps.push({
      id: "endpoint-" + (eps.length + 1),
      name: "新接口",
      path: "/search",
      method: "POST",
      description: "接口能力说明",
      price_micros: 1000,
      parameters: { query: "example" },
    });
    endpointText.value = JSON.stringify(eps, null, 2);
  } catch {
    error.value = "请先修正接口 JSON 格式";
  }
}
async function save() {
  if (!editing.value) return;
  busy.value = true;
  error.value = "";
  try {
    const t = {
      ...editing.value,
      endpoints: JSON.parse(endpointText.value),
      resources: JSON.parse(resourceText.value),
    };
    if (!Array.isArray(t.endpoints) || !Array.isArray(t.resources))
      throw new Error("接口和资源必须是 JSON 数组");
    if (replaceSecret.value || secret.value) t.secret = secret.value;
    await api("/api/admin/tools", { method: "POST", body: JSON.stringify(t) });
    editing.value = null;
    notice.value = "工具配置已保存";
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function topup() {
  if (!topupUser.value) return;
  busy.value = true;
  try {
    await api("/api/admin/topup", {
      method: "POST",
      body: JSON.stringify({
        user_id: topupUser.value.id,
        amount: Math.round(amount.value * 1e6),
        note: note.value,
      }),
    });
    topupUser.value = null;
    notice.value = "充值已入账，账目和管理员操作记录已保存";
    await load();
    await refreshSession();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
function downloadExample() {
  const content = JSON.stringify(
    {
      slug: "example",
      name: "Example Tool",
      category: "搜索与 SEO",
      icon: "✦",
      description: "按上游官方文档填写配置后启用",
      base_url: "https://api.example.com",
      auth_header: "Authorization",
      auth_prefix: "Bearer ",
      enabled: false,
      endpoints: [
        {
          id: "search",
          name: "Search",
          path: "/search",
          method: "POST",
          description: "搜索接口",
          price_micros: 1000,
          parameters: { query: "example" },
        },
      ],
      resources: [],
    },
    null,
    2,
  );
  const url = URL.createObjectURL(
    new Blob([content], { type: "application/json" }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = "tool-config-example.json";
  a.click();
  URL.revokeObjectURL(url);
}
</script>
<template>
  <div class="admin-area">
    <div v-if="error" class="alert error" role="alert">
      <AlertCircle :size="17" />{{ error
      }}<button class="icon-btn" @click="error = ''" aria-label="关闭错误">
        <X :size="14" />
      </button>
    </div>
    <div v-if="notice" class="alert success">
      <CheckCircle2 :size="17" />{{ notice }}
    </div>
    <div class="page-heading">
      <div>
        <span class="eyebrow blue">ADMINISTRATION</span>
        <h1>
          {{
            section === "users"
              ? "用户与充值"
              : section === "audit"
                ? "操作日志"
                : "上游工具配置"
          }}
        </h1>
        <p>
          {{
            section === "users"
              ? "为用户手动入账，每次操作均有记录。"
              : section === "audit"
                ? "查看工具配置和充值的管理员操作记录。"
                : "管理提供商、接口和定价，让工具真正连接起来。"
          }}
        </p>
      </div>
      <button
        v-if="section !== 'users' && section !== 'audit'"
        class="btn dark"
        @click="edit()"
      >
        <Plus :size="17" />添加上游工具</button
      ><button v-else class="btn outline" @click="load" :disabled="loading">
        <RefreshCw :size="15" />刷新
      </button>
    </div>
    <div v-if="loading" class="loading-line">
      <LoaderCircle class="spin" :size="15" />正在加载…
    </div>
    <template v-if="section === 'users'"
      ><div class="search-box">
        <Search :size="18" /><input
          v-model="search"
          placeholder="按邮箱或姓名查找用户"
          aria-label="搜索用户"
        />
      </div>
      <div class="panel table-wrap">
        <table>
          <thead>
            <tr>
              <th>用户</th>
              <th>角色</th>
              <th>余额</th>
              <th>注册时间</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in shownUsers" :key="u.id">
              <td>
                <strong class="text-dark">{{ u.name }}</strong><small
                  class="cell-sub"
                  >{{ u.email }}</small
                >
              </td>
              <td>
                <span
                  class="badge"
                  :class="{ 'blue-badge': u.role === 'admin' }"
                  >{{ u.role === "admin" ? "管理员" : "用户" }}</span
                >
              </td>
              <td class="text-dark mono">{{ money(u.balance) }}</td>
              <td>{{ date(u.created_at) }}</td>
              <td>
                <button
                  class="btn outline small"
                  @click="
                    topupUser = u;
                    amount = 10;
                    note = '';
                  "
                >
                  <Plus :size="14" />充值
                </button>
              </td>
            </tr>
            <tr v-if="!shownUsers.length">
              <td colspan="5" class="empty-cell">未找到用户</td>
            </tr>
          </tbody>
        </table>
        <div class="table-footer">
          显示最近 1000 位用户中的 {{ shownUsers.length }} 位<span
            >充值金额以美元计</span
          >
        </div>
      </div></template
    >
    <template v-else-if="section === 'audit'"
      ><div class="panel table-wrap">
        <table>
          <thead>
            <tr>
              <th>编号</th>
              <th>操作</th>
              <th>对象 ID</th>
              <th>操作人</th>
              <th>时间</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in audit" :key="row.id">
              <td>#{{ row.id }}</td>
              <td>
                {{
                  row.action === "save_tool"
                    ? "保存工具配置"
                    : row.action === "topup"
                      ? "充值入账"
                      : row.action
                }}
              </td>
              <td>{{ row.target_id }}</td>
              <td>{{ row.actor }}</td>
              <td>{{ date(row.created_at) }}</td>
            </tr>
            <tr v-if="!audit.length">
              <td colspan="5" class="empty-cell">暂无管理员操作记录</td>
            </tr>
          </tbody>
        </table>
        <div class="table-footer">最近 200 条操作</div>
      </div></template
    >
    <template v-else
      ><div class="admin-stats">
        <div class="panel">
          <span>已配置工具</span><strong>{{ tools.length }}</strong
          ><Settings2 :size="23" />
        </div>
        <div class="panel">
          <span>已启用</span
          ><strong>{{ tools.filter((t) => t.enabled).length }}</strong
          ><CheckCircle2 :size="23" class="status-ok-icon" />
        </div>
        <div class="panel">
          <span>接口总数</span
          ><strong>{{
            tools.reduce((n, t) => n + t.endpoints.length, 0)
          }}</strong
          ><ArrowUpRight :size="23" />
        </div>
      </div>
      <div class="admin-toolbar">
        <div class="search-box">
          <Search :size="18" /><input
            v-model="search"
            placeholder="搜索工具名称或分类…"
            aria-label="搜索配置"
          />
        </div>
        <button class="btn outline" @click="downloadExample">
          <Copy :size="15" />下载配置示例
        </button>
      </div>
      <div class="panel table-wrap">
        <table>
          <thead>
            <tr>
              <th>工具 / 标识</th>
              <th>分类</th>
              <th>接口</th>
              <th>鉴权</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in shownTools" :key="t.id">
              <td>
                <div class="admin-tool-name">
                  <span class="tool-icon">{{ t.icon }}</span><span
                    ><strong>{{ t.name }}</strong
                    ><small class="cell-sub mono">{{ t.slug }}</small></span
                  >
                </div>
              </td>
              <td>{{ t.category }}</td>
              <td>{{ t.endpoints.length }}</td>
              <td>
                <span class="badge">{{
                  t.has_secret ? "密钥已配置" : "未配置密钥"
                }}</span>
              </td>
              <td>
                <span class="status" :class="t.enabled ? 'success' : 'paused'"
                  ><i />{{ t.enabled ? "已启用" : "未启用" }}</span
                >
              </td>
              <td>
                <button class="btn outline small" @click="edit(t)">
                  <Settings2 :size="14" />配置
                </button>
              </td>
            </tr>
            <tr v-if="!shownTools.length">
              <td colspan="6" class="empty-cell">没有匹配的工具配置</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="tip">
        <ShieldCheck
          :size="17"
        />上游密钥在服务端加密存储。未启用的工具不会出现在用户目录中。
      </div></template
    >
    <Teleport to="body"
      ><div
        v-if="editing"
        class="modal-backdrop editor-backdrop"
        @click.self="!busy && (editing = null)"
      >
        <section
          class="modal tool-editor"
          role="dialog"
          aria-modal="true"
          aria-label="编辑上游工具"
        >
          <div class="editor-title">
            <div>
              <span class="eyebrow blue">UPSTREAM CONFIGURATION</span>
              <h2>
                {{ editing.id ? "配置 " + editing.name : "添加上游工具" }}
              </h2>
            </div>
            <button
              class="icon-btn"
              @click="editing = null"
              :disabled="busy"
              aria-label="关闭"
            >
              <X :size="20" />
            </button>
          </div>
          <div class="editor-tabs">
            <button
              v-for="t in [
                { id: 'general', name: '基本信息与鉴权' },
                { id: 'endpoints', name: '接口与定价' },
                { id: 'resources', name: '资源说明' },
              ]"
              :key="t.id"
              :class="{ active: editorTab === t.id }"
              @click="editorTab = t.id"
            >
              {{ t.name }}
            </button>
          </div>
          <form @submit.prevent="save">
            <div v-if="error" class="alert error" role="alert">{{ error }}</div>
            <div v-show="editorTab === 'general'" class="editor-body">
              <div class="form-grid">
                <label
                  >工具名称<input
                    v-model="editing.name"
                    placeholder="TinyFish"
                    maxlength="100" /></label
                ><label
                  >工具标识<input
                    v-model="editing.slug"
                    placeholder="tinyfish（小写字母、数字、-、_）"
                    maxlength="80" /></label
                ><label
                  >分类<input
                    v-model="editing.category"
                    list="category-options"
                  /><datalist id="category-options">
                    <option
                      v-for="c in [
                        '搜索与 SEO',
                        '社交媒体',
                        '数据抓取',
                        '生成式 AI',
                        '数据与金融',
                        '开发工具',
                        '电商',
                        '通信与邮件',
                      ]"
                      :key="c"
                    >
                      {{ c }}
                    </option>
                  </datalist></label
                ><label
                  >图标（字符 / Emoji）<input
                    v-model="editing.icon"
                    maxlength="8"
                    placeholder="🐟"
                /></label>
              </div>
              <label
                >工具介绍<textarea
                  v-model="editing.description"
                  rows="3"
                  placeholder="用户在工具详情页看到的介绍"
                />
              </label>
              <div class="form-grid">
                <label
                  >官方网站<input
                    v-model="editing.website"
                    placeholder="https://…" /></label
                ><label
                  >文档链接<input
                    v-model="editing.docs"
                    placeholder="https://…"
                /></label>
              </div>
              <div class="form-divider">上游连接</div>
              <label
                >上游 Base URL<input
                  v-model="editing.base_url"
                  placeholder="https://api.provider.com/v1"
                /><small
                  >与接口路径直接拼接；生产环境仅允许公网 HTTPS。</small
                ></label
              >
              <div class="form-grid">
                <label
                  >鉴权 Header<input
                    v-model="editing.auth_header"
                    placeholder="Authorization / X-API-Key" /></label
                ><label
                  >密钥前缀<input
                    v-model="editing.auth_prefix"
                    placeholder="Bearer （含尾部空格）或留空"
                  /><small>例如 Bearer 后需要保留一个空格。</small></label
                >
              </div>
              <label
                >上游密钥<input
                  v-model="secret"
                  type="password"
                  autocomplete="new-password"
                  :placeholder="
                    editing.has_secret
                      ? '已有密钥，留空保留原值'
                      : '输入上游 API Key（不含前缀）'
                  " /></label
              ><label v-if="editing.has_secret" class="checkbox-row"
                ><input
                  type="checkbox"
                  v-model="replaceSecret"
                />替换已有密钥（勾选后留空会清除）</label
              >
              <div class="editor-enable">
                <div>
                  <strong>向用户开放此工具</strong
                  ><small>启用前请核对上游文档、参数和售价。</small>
                </div>
                <input
                  type="checkbox"
                  class="toggle"
                  v-model="editing.enabled"
                  aria-label="启用工具"
                />
              </div>
            </div>
            <div v-show="editorTab === 'endpoints'" class="editor-body">
              <div class="alert">
                售价使用整数微美元：<strong>1000 = $0.001</strong
                >。每个接口单次计费，参数示例将显示给用户。
              </div>
              <div class="json-toolbar">
                <span>接口配置（JSON 数组）</span
                ><button
                  type="button"
                  class="btn outline small"
                  @click="addEndpoint"
                >
                  <Plus :size="14" />添加接口模板
                </button>
              </div>
              <textarea
                v-model="endpointText"
                class="json-editor mono"
                rows="20"
                spellcheck="false"
                aria-label="接口 JSON 配置"
              /><small
                >字段：id（唯一标识）、name、path、method、description、price_micros、parameters（参数示例）。支持
                GET / POST / PUT / PATCH /
                DELETE。此版本不支持参数映射、动态路径或流式返回。</small
              >
            </div>
            <div v-show="editorTab === 'resources'" class="editor-body">
              <div class="alert">
                资源仅展示说明，不会创建或租赁实际资源。不需要资源说明时填写
                []。
              </div>
              <textarea
                v-model="resourceText"
                class="json-editor mono"
                rows="14"
                spellcheck="false"
                aria-label="资源 JSON 配置"
              /><small
                >示例：[ { "name": "Cloud browser", "description":
                "云浏览器服务说明", "price": "$0.002 / 分钟" } ]</small
              >
            </div>
            <footer class="editor-footer">
              <span>密钥保存后不会返回明文</span
              ><button
                type="button"
                class="btn outline"
                @click="editing = null"
                :disabled="busy"
              >
                取消</button
              ><button class="btn primary" :disabled="busy">
                <Save :size="16" />{{ busy ? "保存中…" : "保存配置" }}
              </button>
            </footer>
          </form>
        </section>
      </div>
      <div
        v-if="topupUser"
        class="modal-backdrop"
        @click.self="!busy && (topupUser = null)"
      >
        <form
          class="modal"
          role="dialog"
          aria-modal="true"
          aria-label="用户充值"
          @submit.prevent="topup"
        >
          <button
            class="icon-btn modal-close"
            type="button"
            @click="topupUser = null"
            :disabled="busy"
            aria-label="关闭"
          >
            <X :size="20" /></button
          ><span class="modal-icon"><Wallet /></span>
          <h2>为用户充值</h2>
          <p>{{ topupUser.name }} · {{ topupUser.email }}</p>
          <div v-if="error" class="alert error">{{ error }}</div>
          <div class="topup-current">
            当前余额 <strong>{{ money(topupUser.balance) }}</strong>
          </div>
          <label
            >入账金额（USD）<input
              v-model.number="amount"
              type="number"
              min="0.000001"
              max="1000000"
              step="0.000001"
              required /></label
          ><label
            >充值备注<textarea
              v-model="note"
              maxlength="500"
              placeholder="例如：人工收款，订单号…"
              rows="3"
            />
          </label>
          <div class="alert">
            确认后将立即入账
            {{ money(Math.round(amount * 1e6)) }}，并写入充值流水和操作日志。
          </div>
          <button class="btn primary full" :disabled="busy">
            {{ busy ? "入账中…" : "确认充值入账" }}
          </button>
        </form>
      </div></Teleport
    >
  </div>
</template>
