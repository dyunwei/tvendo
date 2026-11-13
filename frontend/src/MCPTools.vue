<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  RefreshCw,
  Search,
  CheckCircle2,
  AlertCircle,
  ChevronDown,
  ArrowLeft,
  ArrowRight,
} from "lucide-vue-next";
import { api, money, date } from "./api";
import type { Tool, Endpoint } from "./api";
const props = defineProps<{ provider: Tool; refresh: () => Promise<void> }>();
const syncing = ref(false),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  query = ref(""),
  filter = ref("all"),
  selected = ref<string[]>([]),
  page = ref(1),
  price = ref(0.001),
  detail = ref<Endpoint | null>(null),
  singlePrices = ref<Record<string, number>>({});
const pageSize = 30;
const matched = computed(() =>
  props.provider.endpoints.filter(
    (ep) =>
      (ep.name + " " + ep.mcp_name + " " + ep.description)
        .toLowerCase()
        .includes(query.value.toLowerCase()) &&
      (filter.value === "all" ||
        (filter.value === "enabled" && !ep.disabled && !ep.missing) ||
        (filter.value === "pending" && ep.disabled && !ep.missing) ||
        (filter.value === "missing" && ep.missing) ||
        (filter.value === "changed" && ep.schema_changed)),
  ),
);
const pages = computed(() =>
  Math.max(1, Math.ceil(matched.value.length / pageSize)),
);
const rows = computed(() =>
  matched.value.slice((page.value - 1) * pageSize, page.value * pageSize),
);
const allMatched = computed(
  () =>
    matched.value.length > 0 &&
    matched.value.every((ep) => selected.value.includes(ep.id)),
);
const locked = computed(() => syncing.value || busy.value);
watch([query, filter], () => {
  page.value = 1;
});
watch(
  () => props.provider.revision,
  () => {
    selected.value = [];
    singlePrices.value = {};
    page.value = Math.min(page.value, pages.value);
  },
);
function selectMatched() {
  selected.value = allMatched.value
    ? selected.value.filter((id) => !matched.value.some((ep) => ep.id === id))
    : [...new Set([...selected.value, ...matched.value.map((ep) => ep.id)])];
}
function effective(ep: Endpoint) {
  return ep.price_override
    ? ep.price_micros
    : props.provider.default_price_micros || 0;
}
async function sync() {
  syncing.value = true;
  error.value = "";
  notice.value = "";
  try {
    const d = await api(`/api/admin/tools/${props.provider.id}/sync`, {
      method: "POST",
    });
    await props.refresh();
    notice.value = `同步完成：新增 ${d.added}，更新 ${d.updated}，上游已删除 ${d.missing}，参数待复核 ${d.changed}。新增工具默认未启用。`;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    syncing.value = false;
  }
}
async function bulk(action: Record<string, unknown>, ids = selected.value) {
  if (!ids.length) return;
  busy.value = true;
  error.value = "";
  notice.value = "";
  try {
    await api(`/api/admin/tools/${props.provider.id}/bulk`, {
      method: "POST",
      body: JSON.stringify({
        revision: props.provider.revision,
        ids,
        ...action,
      }),
    });
    await props.refresh();
    notice.value = `已更新 ${ids.length} 个工具`;
    selected.value = [];
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
function setSinglePrice(ep: Endpoint) {
  const value = singlePrices.value[ep.id] ?? effective(ep) / 1e6;
  if (!Number.isFinite(value) || value < 0 || value > 1000) {
    error.value = "售价需在 0–1000 美元内";
    return;
  }
  bulk({ price_micros: Math.round(value * 1e6) }, [ep.id]);
}
function setPrice(ids = selected.value) {
  if (!Number.isFinite(price.value) || price.value < 0 || price.value > 1000) {
    error.value = "售价需在 0–1000 美元内";
    return;
  }
  bulk({ price_micros: Math.round(price.value * 1e6) }, ids);
}
</script>
<template>
  <section class="mcp-catalog">
    <div class="mcp-sync-bar">
      <div>
        <strong
          >MCP 工具清单
          <span class="badge">{{ provider.endpoints.length }}</span></strong
        ><small>{{
          provider.last_synced_at
            ? "最近同步：" + date(provider.last_synced_at)
            : "尚未同步，请先保存连接信息"
        }}</small>
      </div>
      <button
        type="button"
        class="btn primary small"
        @click="sync"
        :disabled="locked"
      >
        <RefreshCw :size="14" :class="{ spin: syncing }" />{{
          syncing ? "正在连接并同步…" : "测试连接并同步"
        }}
      </button>
    </div>
    <p class="mcp-hint">
      同步使用已保存的连接配置。默认价
      {{ money(provider.default_price_micros || 0) }} /
      次；批量启用前请确认售价。同步保留定价和上下架状态，不会自动获取上游价格。
    </p>
    <div v-if="error" class="alert error" role="alert">
      <AlertCircle :size="16" />{{ error }}
    </div>
    <div v-if="notice" class="alert success" role="status">
      <CheckCircle2 :size="16" />{{ notice }}
    </div>
    <div class="mcp-filters">
      <div class="search-box">
        <Search :size="15" /><input
          v-model="query"
          placeholder="搜索工具名称、标识或说明"
          aria-label="搜索 MCP 工具"
        />
      </div>
      <select v-model="filter" aria-label="筛选 MCP 工具">
        <option value="all">全部状态</option>
        <option value="enabled">已启用</option>
        <option value="pending">未启用 / 待审核</option>
        <option value="changed">参数待复核</option>
        <option value="missing">上游已删除</option>
      </select>
    </div>
    <div class="mcp-bulk">
      <label class="mcp-select"
        ><input
          type="checkbox"
          :checked="allMatched"
          @change="selectMatched"
          :disabled="locked || !matched.length"
        />选择全部筛选结果（{{ matched.length }}）</label
      ><span>已选 {{ selected.length }}</span
      ><button
        type="button"
        class="btn outline small"
        @click="bulk({ enabled: true })"
        :disabled="locked || !selected.length"
      >
        批量启用</button
      ><button
        type="button"
        class="btn outline small"
        @click="bulk({ enabled: false })"
        :disabled="locked || !selected.length"
      >
        批量停用
      </button>
    </div>
    <div class="mcp-price-bar">
      <label
        >单次售价（USD）<input
          v-model.number="price"
          type="number"
          min="0"
          max="1000"
          step="0.000001"
          aria-label="批量单次售价" /></label
      ><button
        type="button"
        class="btn outline small"
        @click="setPrice()"
        :disabled="locked || !selected.length"
      >
        应用到所选工具</button
      ><button
        type="button"
        class="btn outline small"
        @click="bulk({ use_default: true })"
        :disabled="locked || !selected.length"
      >
        所选恢复默认价
      </button>
    </div>
    <div class="panel table-wrap mcp-table">
      <table>
        <thead>
          <tr>
            <th />
            <th>工具 / 状态</th>
            <th>单次售价</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="ep in rows" :key="ep.id">
            <td>
              <input
                v-model="selected"
                :value="ep.id"
                type="checkbox"
                :aria-label="'选择 ' + ep.name"
                :disabled="locked"
              />
            </td>
            <td>
              <button
                type="button"
                class="mcp-name"
                @click="detail = detail?.id === ep.id ? null : ep"
              >
                {{ ep.name }} <ChevronDown :size="12" /></button
              ><small class="cell-sub mono">{{ ep.mcp_name }}</small
              ><span
                class="status"
                :class="
                  ep.missing ? 'failed' : ep.disabled ? 'paused' : 'success'
                "
                ><i />{{
                  ep.missing ? "上游已删除" : ep.disabled ? "未启用" : "已启用"
                }}</span
              ><span v-if="ep.schema_changed" class="badge">参数待复核</span>
              <p v-if="detail?.id === ep.id" class="mcp-description">
                {{ ep.description }}
              </p>
              <pre v-if="detail?.id === ep.id" class="mcp-schema">{{
                JSON.stringify(ep.input_schema, null, 2)
              }}</pre>
            </td>
            <td>
              {{ money(effective(ep))
              }}<input
                class="mcp-single-price"
                type="number"
                min="0"
                max="1000"
                step="0.000001"
                :value="singlePrices[ep.id] ?? effective(ep) / 1e6"
                @input="
                  singlePrices[ep.id] = Number(
                    ($event.target as HTMLInputElement).value,
                  )
                "
                :aria-label="'单独售价 ' + ep.name"
                :disabled="locked"
              /><small class="cell-sub">{{
                ep.price_override ? "单独售价" : "跟随默认价"
              }}</small>
            </td>
            <td>
              <button
                type="button"
                class="btn outline small"
                @click="bulk({ enabled: !!ep.disabled }, [ep.id])"
                :disabled="locked || ep.missing"
              >
                {{ ep.disabled ? "启用" : "停用" }}</button><button
                type="button"
                class="btn outline small"
                @click="setSinglePrice(ep)"
                :disabled="locked"
              >
                单独改价</button
              ><button
                v-if="ep.price_override"
                type="button"
                class="btn outline small"
                @click="bulk({ use_default: true }, [ep.id])"
                :disabled="locked"
              >
                恢复默认
              </button>
            </td>
          </tr>
          <tr v-if="!rows.length">
            <td colspan="4" class="empty-cell">
              {{
                provider.endpoints.length
                  ? "没有符合筛选条件的工具"
                  : "尚未同步工具，请点击「测试连接并同步」"
              }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="mcp-pagination">
      <span>第 {{ page }} / {{ pages }} 页 · {{ matched.length }} 个工具</span
      ><button
        type="button"
        class="btn outline small"
        @click="page--"
        :disabled="page <= 1"
      >
        <ArrowLeft :size="13" />上一页</button
      ><button
        type="button"
        class="btn outline small"
        @click="page++"
        :disabled="page >= pages"
      >
        下一页<ArrowRight :size="13" />
      </button>
    </div>
  </section>
</template>
