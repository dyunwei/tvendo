<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import QRCode from "qrcode";
import { api as baseAPI, date, money, session } from "../api";
import { useEscapeClose } from "../useEscapeClose";
import { X, RefreshCw, Receipt } from "lucide-vue-next";
import PaginatedTable from "./PaginatedTable.vue";
// randomUUID requires a secure context; getRandomValues also works for HTTP LAN development.
function newPaymentRequestKey() {
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join(
    "",
  );
}
async function api<T = any>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 30000);
  try {
    return await baseAPI<T>(path, { ...options, signal: controller.signal });
  } catch (e) {
    if (controller.signal.aborted)
      throw new Error("请求超时，请先刷新余额和订单确认结果，避免重复充值。");
    throw e;
  } finally {
    clearTimeout(timeout);
  }
}
const props = defineProps<{ admin?: boolean; embedded?: boolean }>();
const emit = defineEmits<{ paid: [] }>();
interface Order {
  id: string;
  email?: string;
  amount_cents: number;
  status: "pending" | "paid" | "closed";
  code_url?: string;
  transaction_id: string | null;
  created_at: string;
  expires_at: string;
}
const enabled = ref(false),
  amounts = ref<number[]>([]),
  selected = ref(1);
const orders = ref<Order[]>([]),
  active = ref<Order | null>(null);
const qr = ref(""),
  error = ref(""),
  pollError = ref(""),
  busy = ref(false);
const choosing = ref(false);
const ordersLoading = ref(true);
const method = ref("wechat");
const canCredit = computed(() => session.user?.role === "admin");
const users = ref<
  { id: number; name: string; email: string; balance: number }[]
>([]);
const targetID = ref<number | null>(null),
  manualAmount = ref(10),
  note = ref("");
const formError = ref(""),
  success = ref("");
async function openTopup() {
  choosing.value = true;
  active.value = null;
  formError.value = "";
  success.value = "";
  method.value = canCredit.value && !enabled.value ? "admin" : "wechat";
  targetID.value = session.user?.id ?? null;
  if (canCredit.value) {
    try {
      users.value = await api("/api/admin/users");
    } catch (e) {
      formError.value = (e as Error).message;
    }
  }
}
defineExpose({ openTopup });
async function submitTopup() {
  if (method.value === "wechat") {
    await create();
    return;
  }
  if (!canCredit.value || busy.value) return;
  const amount = Math.round(Number(manualAmount.value) * 1e6);
  if (
    !targetID.value ||
    !Number.isSafeInteger(amount) ||
    amount <= 0 ||
    amount > 1e12
  ) {
    formError.value = "请选择用户并填写大于 0 且不超过 100 万元的金额";
    return;
  }
  busy.value = true;
  formError.value = "";
  try {
    await api("/api/admin/topup", {
      method: "POST",
      body: JSON.stringify({
        user_id: targetID.value,
        amount,
        note: note.value,
      }),
    });
    const target = users.value.find((u) => u.id === targetID.value);
    success.value = `已为 ${target?.email || "所选用户"} 入账 ${money(amount)}`;
    choosing.value = false;
    emit("paid");
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
const requestKey = ref("");
const now = ref(Date.now());
const expired = computed(
  () => active.value && now.value >= Date.parse(active.value.expires_at),
);
const labels = { pending: "待支付", paid: "已到账", closed: "已关闭" };
let timer: ReturnType<typeof setInterval> | undefined;
let clock: ReturnType<typeof setInterval> | undefined;
let polling = false,
  disposed = false,
  qrVersion = 0;
const endpoint = props.admin ? "/api/admin/payments" : "/api/payments/orders";
async function load() {
  ordersLoading.value = true;
  try {
    orders.value = await api<Order[]>(endpoint);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    ordersLoading.value = false;
  }
}
async function open(order: Order) {
  choosing.value = false;
  active.value = order;
  qr.value = "";
  pollError.value = "";
  const version = ++qrVersion;
  if (
    order.status === "pending" &&
    order.code_url &&
    Date.now() < Date.parse(order.expires_at)
  ) {
    try {
      const data = await QRCode.toDataURL(order.code_url, {
        width: 256,
        margin: 2,
      });
      if (!disposed && version === qrVersion) qr.value = data;
    } catch {
      error.value = "二维码生成失败，请刷新后重试";
    }
  }
}
function close() {
  if (busy.value) return;
  choosing.value = false;
  active.value = null;
  qr.value = "";
  ++qrVersion;
}
useEscapeClose(close);
async function create() {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  formError.value = "";
  // Retain the key on an uncertain network response, so retrying cannot create another order.
  try {
    if (!requestKey.value) requestKey.value = newPaymentRequestKey();
    const order = await api<Order>("/api/payments/orders", {
      method: "POST",
      body: JSON.stringify({
        amount_cents: selected.value,
        request_key: requestKey.value,
      }),
    });
    requestKey.value = "";
    await open(order);
    await load();
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function poll() {
  if (polling || props.admin || document.hidden) return;
  if (!orders.value.some((o) => o.status === "pending")) return;
  polling = true;
  try {
    const updated = await api<Order[]>(endpoint);
    if (disposed) return;
    const justPaid = updated.some(
      (o) =>
        o.status === "paid" &&
        orders.value.some((old) => old.id === o.id && old.status !== "paid"),
    );
    orders.value = updated;
    pollError.value = "";
    if (active.value) {
      const current = updated.find((o) => o.id === active.value!.id);
      if (current) {
        if (
          current.status !== active.value.status ||
          current.code_url !== active.value.code_url
        )
          await open(current);
        else active.value = current;
      }
    }
    if (justPaid) emit("paid");
  } catch {
    pollError.value = "暂时无法刷新支付状态，恢复连接后将自动重试。";
  } finally {
    polling = false;
  }
}
onMounted(async () => {
  if (!props.admin) {
    try {
      const config = await api<{ enabled: boolean; amounts_cents: number[] }>(
        "/api/payments/config",
      );
      enabled.value = config.enabled;
      amounts.value = config.amounts_cents;
    } catch (e) {
      error.value = (e as Error).message;
    }
  }
  await load();
  if (disposed) return;
  timer = setInterval(poll, 3000);
  clock = setInterval(() => {
    now.value = Date.now();
  }, 1000);
});
onUnmounted(() => {
  disposed = true;
  ++qrVersion;
  clearInterval(timer);
  clearInterval(clock);
});
</script>

<template>
  <p v-if="success" class="alert" role="status">{{ success }}</p>
  <p v-if="error" role="alert" class="recharge-error">{{ error }}</p>
  <p v-if="pollError && !active" role="status">{{ pollError }}</p>
  <div :class="embedded ? 'wallet-records-toolbar' : 'section-heading'">
    <span v-if="embedded">最近 100 条订单 · 待支付订单可继续付款</span>
    <h3 v-else>{{ admin ? "在线充值订单" : "微信支付订单" }}</h3>
    <button class="btn outline small" :disabled="ordersLoading" @click="load">
      <RefreshCw :size="14" />刷新
    </button>
  </div>
  <div
    v-if="orders.length || !embedded"
    class="panel table-wrap"
    :class="{ 'wallet-orders': embedded }"
  >
    <PaginatedTable
      :items="orders"
      label="充值订单分页"
      :note="
        embedded
          ? undefined
          : admin
            ? '最近 200 条订单'
            : '最近 100 条订单，可继续未完成的支付'
      "
      v-slot="{ rows }"
    >
      <table>
        <thead>
          <tr>
            <th v-if="admin">用户</th>
            <th>订单</th>
            <th :class="{ 'wallet-money': embedded }">金额</th>
            <th>状态</th>
            <th>创建时间</th>
            <th v-if="!admin"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="order in rows" :key="order.id">
            <td v-if="admin">{{ order.email }}</td>
            <td>
              <span class="mono">{{ order.id }}</span><small
                v-if="admin && order.transaction_id"
                class="cell-sub"
                >微信交易号 {{ order.transaction_id }}</small
              >
            </td>
            <td :class="{ 'wallet-money': embedded }">
              {{ money(order.amount_cents * 10000) }}
            </td>
            <td>
              <span
                :class="
                  embedded ? ['wallet-order-status', order.status] : undefined
                "
                >{{ labels[order.status]
                }}{{
                  order.status === "pending" &&
                  Date.now() >= Date.parse(order.expires_at)
                    ? " · 确认中"
                    : ""
                }}</span
              >
            </td>
            <td :class="{ 'wallet-secondary': embedded }">
              {{ date(order.created_at) }}
            </td>
            <td v-if="!admin">
              <button class="btn outline small" @click="open(order)">
                {{ order.status === "pending" ? "查看支付" : "查看订单" }}
              </button>
            </td>
          </tr>
          <tr v-if="!orders.length">
            <td colspan="5" class="empty-cell">暂无微信支付订单</td>
          </tr>
        </tbody>
      </table>
    </PaginatedTable>
  </div>
  <div
    v-else
    class="panel wallet-empty"
    :role="ordersLoading ? 'status' : undefined"
  >
    <RefreshCw v-if="ordersLoading" :size="24" class="wallet-refreshing" />
    <Receipt v-else :size="26" />
    <div>
      <h3>{{ ordersLoading ? "正在加载充值订单" : "还没有充值订单" }}</h3>
      <p>
        {{
          ordersLoading ? "请稍候。" : "充值后可在这里查看支付状态和订单详情。"
        }}
      </p>
    </div>
  </div>
  <Teleport to="body">
    <div v-if="choosing" class="modal-backdrop" @click.self="close">
      <form
        class="modal topup-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="topup-title"
        @submit.prevent="submitTopup"
      >
        <button
          class="icon-btn modal-close"
          type="button"
          aria-label="关闭充值窗口"
          :disabled="busy"
          @click="close"
        >
          <X :size="20" />
        </button>
        <h2 id="topup-title">充值</h2>
        <p>选择充值方式和金额。</p>
        <label
          >充值方式<select v-model="method" :disabled="busy">
            <option value="wechat">微信支付</option>
            <option v-if="canCredit" value="admin">管理员直接充值</option>
          </select></label
        >
        <template v-if="method === 'wechat'">
          <template v-if="enabled">
            <div class="recharge-amounts">
              <button
                v-for="amount in amounts"
                :key="amount"
                type="button"
                class="btn outline"
                :class="{ chosen: selected === amount }"
                :aria-pressed="selected === amount"
                :disabled="busy || !!requestKey"
                @click="selected = amount"
              >
                {{ money(amount * 10000) }}
              </button>
            </div>
            <p>充值到当前账户。使用手机微信扫码支付，完成后自动入账。</p>
            <small>充值金额等额计入余额，暂不支持手机同机支付。</small>
          </template>
          <p v-else>微信支付暂未开通，请联系管理员充值。</p>
        </template>
        <template v-else-if="canCredit">
          <label
            >充值用户<select v-model="targetID" :disabled="busy" required>
              <option :value="null" disabled>请选择用户</option>
              <option v-for="user in users" :key="user.id" :value="user.id">
                {{ user.name }} · {{ user.email }}
              </option>
            </select></label
          >
          <label
            >入账金额（元）<input
              v-model.number="manualAmount"
              type="number"
              min="0.000001"
              max="1000000"
              step="0.000001"
              required
              :disabled="busy"
          /></label>
          <label
            >充值备注<textarea
              v-model="note"
              maxlength="500"
              rows="3"
              placeholder="例如：人工收款，订单号…"
              :disabled="busy"
            />
          </label>
          <div class="alert">
            确认后立即为所选用户入账，并保存充值流水和管理员操作记录。
          </div>
        </template>
        <p v-if="formError" class="alert error" role="alert">{{ formError }}</p>
        <button
          v-if="method === 'admin' || enabled"
          class="btn dark full"
          :disabled="busy || (method === 'admin' && !users.length)"
        >
          {{
            busy
              ? "处理中…"
              : method === "admin"
                ? "确认直接充值入账"
                : requestKey
                  ? "重试创建订单"
                  : "微信支付 " + money(selected * 10000)
          }}
        </button>
      </form>
    </div>
    <div v-if="active" class="modal-backdrop" @click.self="close">
      <section
        class="modal recharge-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="recharge-title"
      >
        <button
          class="icon-btn recharge-close"
          aria-label="关闭支付窗口"
          @click="close"
        >
          <X :size="20" />
        </button>
        <h2 id="recharge-title">
          {{
            active.status === "paid"
              ? "充值已到账"
              : active.status === "closed"
                ? "订单已关闭"
                : "微信扫码支付"
          }}
        </h2>
        <strong class="recharge-total">{{
          money(active.amount_cents * 10000)
        }}</strong>
        <template v-if="active.status === 'pending'">
          <template v-if="!expired && qr"
            ><img :src="qr" alt="微信支付二维码" width="256" height="256" />
            <p>请使用手机微信扫一扫</p></template
          >
          <p v-else-if="expired">
            二维码已过期，正在确认最终支付结果。未付款订单关闭后可重新充值。
          </p>
          <p v-else>
            正在确认订单是否创建成功，请稍后查看。请勿对同一笔充值重复付款。
          </p>
          <small
            >有效期至
            {{ date(active.expires_at) }}。关闭窗口后仍可从订单列表查看。</small
          >
        </template>
        <p v-else-if="active.status === 'paid'">
          已计入账户余额，可以继续使用工具。
        </p>
        <p v-else>未发生充值入账，可重新选择金额充值。</p>
        <small class="mono">订单号 {{ active.id }}</small>
        <p v-if="pollError" role="status">{{ pollError }}</p>
        <button class="btn outline" @click="close">
          {{ active.status === "paid" ? "完成" : "关闭" }}
        </button>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.topup-modal {
  position: relative;
}
.topup-modal select {
  width: 100%;
  margin-top: 8px;
}
.topup-modal label {
  display: block;
  margin-top: 16px;
}
.topup-modal .full {
  margin-top: 20px;
}
.recharge-amounts {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin: 20px 0;
}
.recharge-amounts .chosen {
  border-color: #19835b;
  background: #edf7f1;
  color: #176341;
}
.recharge-error {
  color: #ba3434;
  margin: 16px 0;
}
.recharge-modal {
  position: relative;
  max-width: 440px;
  padding: 32px;
  text-align: center;
  display: flex;
  align-items: center;
  flex-direction: column;
  gap: 16px;
}
.recharge-close {
  position: absolute;
  top: 12px;
  right: 12px;
}
.recharge-total {
  font-size: 32px;
}
.recharge-modal small {
  overflow-wrap: anywhere;
  max-width: 100%;
  color: #68736d;
}
.recharge-modal img {
  max-width: 100%;
  height: auto;
  border-radius: 8px;
}
</style>
