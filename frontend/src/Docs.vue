<script setup lang="ts">
import { ref, computed } from "vue";
import { useRoute } from "vue-router";
import {
  BookOpen,
  Zap,
  Code2,
  KeyRound,
  Wallet,
  ArrowUpRight,
  Copy,
  Check,
} from "lucide-vue-next";
const route = useRoute(),
  tab = ref(String(route.query.tab || "quickstart")),
  copied = ref(false);
const base = window.location.origin;
const snippet = computed(
  () =>
    `curl ${base}/v1/run \\\n  -H "Authorization: Bearer $TVENDO_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '{"tool":"tinyfish","endpoint":"search",\n       "parameters":{"query":"AI agent tools"}}'`,
);
async function copy() {
  try {
    await navigator.clipboard.writeText(snippet.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 2000);
  } catch {
    copied.value = false;
  }
}
const items = [
  { id: "quickstart", name: "快速开始", icon: Zap },
  { id: "skill", name: "Agent Skill", icon: BookOpen },
  { id: "api", name: "API 参考", icon: Code2 },
  { id: "keys", name: "API 密钥", icon: KeyRound },
  { id: "billing", name: "余额与计费", icon: Wallet },
];
</script>
<template>
  <main class="docs-layout">
    <aside class="docs-sidebar">
      <span class="eyebrow">DOCUMENTATION</span
      ><button
        v-for="item in items"
        :key="item.id"
        :class="{ active: tab === item.id }"
        @click="tab = item.id"
      >
        <component :is="item.icon" :size="17" />{{ item.name }}</button
      ><RouterLink to="/app" class="docs-console"
        >进入控制台 <ArrowUpRight :size="15"
      /></RouterLink>
    </aside>
    <article class="docs-content">
      <div class="breadcrumb">
        文档 <span>/</span>
        {{ items.find((x) => x.id === tab)?.name || "快速开始" }}
      </div>
      <template v-if="tab === 'quickstart'"
        ><span class="eyebrow blue">GET STARTED</span>
        <h1>几分钟，完成第一次调用。</h1>
        <p class="doc-lead">一个 API Key，连接你需要的工具。</p>
        <h2><b class="number">1</b> 创建账户并充值</h2>
        <p>
          注册 T vendo
          账户，联系平台管理员充值。在「钱包」中可以查看可用余额和充值记录。
        </p>
        <RouterLink to="/register" class="btn primary"
          >创建账户 <ArrowUpRight :size="15"
        /></RouterLink>
        <h2><b class="number">2</b> 创建 API Key</h2>
        <p>
          进入控制台的「API 密钥」，为你的 Agent
          创建密钥。完整密钥只显示一次，请妥善保存到环境变量中。
        </p>
        <pre>export TVENDO_API_KEY="tv_live_..."</pre>
        <h2><b class="number">3</b> 选择工具并调用</h2>
        <p>
          在工具目录查看已启用的工具与接口，确认接口标识、参数和价格，再发送请求。以下是
          TinyFish 配置示例；实际可用接口以控制台为准。
        </p>
        <div class="doc-code">
          <button @click="copy" :aria-label="copied ? '已复制' : '复制示例'">
            <Check v-if="copied" :size="17" /><Copy v-else :size="17" />
          </button>
          <pre>{{ snippet }}</pre>
        </div></template
      >
      <template v-else-if="tab === 'skill'"
        ><span class="eyebrow blue">FOR YOUR AGENT</span>
        <h1>让 Agent 学会使用工具。</h1>
        <p class="doc-lead">
          把这条指令发给能读取网页并发送 HTTP 请求的 Agent。
        </p>
        <pre>请读取 {{ base }}/SKILL.md，帮我配置 Tvendo 工具接入。</pre>
        <h2>接入流程</h2>
        <p>
          Skill 描述了发现接口与执行调用的步骤。你的 Agent 读取文档后，会检查
          CLI 和接入配置。请在本机配置平台 API Key，并授权任务的费用范围。
        </p>
        <a href="/SKILL.md" target="_blank" rel="noopener" class="btn outline"
          >查看 SKILL.md <ArrowUpRight :size="15"
        /></a>
        <div class="alert">
          支持中文 Skill、REST API、Go CLI 及上游 MCP 工具。CLI 安装入口见
          /v1/info；Docker 镜像自带 npm 安装包与六种平台二进制，无需本机编译。
        </div></template
      >
      <template v-else-if="tab === 'api'"
        ><span class="eyebrow blue">API REFERENCE</span>
        <h1>简单、一致的 HTTP API。</h1>
        <p class="doc-lead">
          基础地址：<code>{{ base }}</code>
        </p>
        <h2>身份验证</h2>
        <pre>Authorization: Bearer &lt;TVENDO_API_KEY&gt;</pre>
        <h2><span class="method">GET</span> /v1/tools</h2>
        <p>
          返回已启用工具、接口
          ID、调用方法、参数示例和售价。金额按人民币（CNY）计价，使用整数微元：1
          元 = 1,000,000 微元。
        </p>
        <pre>
{ "slug": "tinyfish", "endpoints": [
  { "id": "search", "price_micros": 1000, ... }
] }</pre>
        <h2><span class="method post">POST</span> /v1/run</h2>
        <p>
          平台统一使用 POST 调用；平台根据管理员配置转发到上游的方法及路径。GET
          上游的参数转换为查询字符串，其余方法发送 JSON 请求体。
        </p>
        <pre>
{
  "tool": "tinyfish",
  "endpoint": "search",
  "parameters": { "query": "AI agent tools" }
}</pre>
        <h3>成功响应</h3>
        <pre>{ "id": 42, "cost_micros": 1000, "data": { ... } }</pre>
        <h3>错误状态</h3>
        <table>
          <thead>
            <tr>
              <th>状态码</th>
              <th>含义</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="x in [
                ['400', '参数或配置无效'],
                ['401', '密钥无效或已撤销'],
                ['402', '余额不足'],
                ['403', '超出预算'],
                ['404', '工具或接口不存在'],
                ['429', '请求频率过高'],
                ['502', '上游失败，已退还预留金额'],
                ['503', '结算暂未完成，请查询用量，避免自动重试'],
              ]"
              :key="x[0]"
            >
              <td>{{ x[0] }}</td>
              <td>{{ x[1] }}</td>
            </tr>
          </tbody>
        </table>
        <div class="alert">
          当前支持同步 HTTP / JSON 和远程 MCP 工具调用，超时 60 秒，最大响应 8
          MB。异步任务、流式响应、浏览器资源租赁暂未实现。
        </div></template
      >
      <template v-else-if="tab === 'keys'"
        ><span class="eyebrow blue">AUTHENTICATION</span>
        <h1>你的密钥，你的控制。</h1>
        <p class="doc-lead">在控制台创建和撤销工作空间的 API 密钥。</p>
        <h2>密钥只展示一次</h2>
        <p>
          完整密钥仅在创建时返回。平台保存密钥哈希与前缀，无法恢复原始密钥。遗失后请撤销并创建新的密钥。
        </p>
        <h2>权限范围</h2>
        <p>
          当前版本每个密钥拥有所属账户的工具调用权限；余额和预算在该账户的所有密钥之间共享。密钥不能访问管理员接口。
        </p>
        <h2>撤销即时生效</h2>
        <p>撤销后不能再发起新的调用。已在上游执行中的请求仍会完成并结算。</p>
        <RouterLink to="/app/keys" class="btn primary"
          >管理 API 密钥 <ArrowUpRight :size="15" /></RouterLink
      ></template>
      <template v-else
        ><span class="eyebrow blue">BILLING</span>
        <h1>每一次调用，清楚可见。</h1>
        <p class="doc-lead">按接口售价计费，所有工具共用一个余额。</p>
        <h2>如何充值？</h2>
        <p>
          联系平台管理员。管理员确认后为你的账户入账，并填写充值备注。平台不会在页面上收集银行卡信息。
        </p>
        <h2>预留与结算</h2>
        <p>
          请求执行前预留单次调用金额，成功后确认扣款；上游返回失败、超时或响应超过限制时退回预留款。进程意外中断遗留的预留金额会在五分钟后自动退回。
        </p>
        <h2>预算控制</h2>
        <p>
          可以启用单次调用上限和每日预算。每日预算按 UTC 00:00
          重置，对应北京时间 08:00。当天成功调用和执行中预留金额共同计入预算。
        </p>
        <h2>价格与记录</h2>
        <p>
          售价由管理员配置，在工具详情显示。钱包显示最近 200
          条账目，用量页显示最近 200 次调用。
        </p></template
      >
      <div class="doc-bottom">
        <span>还有下一步。</span
        ><RouterLink to="/app"
          >前往控制台 <ArrowUpRight :size="16"
        /></RouterLink>
      </div>
    </article>
  </main>
</template>
