<script setup lang="ts">
import { ref, computed } from "vue";
import {
  ArrowRight,
  Copy,
  Check,
  Search,
  Play,
  CreditCard,
  ChevronRight,
  Plus,
  Minus,
  Terminal,
  Layers,
  Zap,
} from "lucide-vue-next";
const copied = ref(false),
  open = ref(-1);
const command = computed(
  () => `请读取 ${window.location.origin}/SKILL.md，帮我配置 Tvendo 工具接入。`,
);
async function copy() {
  try {
    await navigator.clipboard.writeText(command.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 2000);
  } catch {
    copied.value = false;
  }
}
const providers = [
  ["✳", "Apollo", "sales intelligence", "#252525"],
  ["ϟ", "Seedance", "video generation", "#2768ef"],
  ["🐟", "TinyFish", "search & extraction", "#ff8b35"],
  ["a", "Ahrefs", "SEO data", "#554fff"],
  ["▧", "Exa", "web search", "#5d52ed"],
  ["●", "Reddit", "social data", "#ff6314"],
  ["♪", "TikTok", "social media", "#181818"],
  ["◎", "Instagram", "social media", "#e5428c"],
  ["▶", "YouTube", "video data", "#ef2638"],
  ["a", "Amazon", "commerce data", "#111"],
  ["f", "Facebook", "social data", "#2675ef"],
  ["➤", "Telegram", "messaging", "#28a5cf"],
  ["G", "Google Reviews", "local business", "#4285f4"],
  ["B", "Browserbase", "web automation", "#ff652d"],
  ["▥", "MiniMax", "AI generation", "#e64f81"],
  ["◈", "DeFiLlama", "DeFi analytics", "#13203a"],
  ["♨", "Firecrawl", "web scraping", "#ff762b"],
  ["✉", "AgentMail", "agent email", "#171717"],
  ["♧", "People Data Labs", "people search", "#9174d1"],
  ["↔", "context.dev", "brand data", "#4f65e9"],
];
const faqs = [
  [
    "每个工具都需要单独申请 API Key 吗？",
    "不需要。管理员配置上游服务后，你只需一个 T vendo API Key 即可调用平台中已启用的工具。",
  ],
  [
    "费用如何计算？",
    "按接口单次售价计费。每次调用前会预留余额，成功后结算，实际价格可在工具详情中查看。",
  ],
  [
    "上游调用失败还会扣费吗？",
    "平台判断上游请求失败后会退回预留余额。你可以在用量页面查看调用状态。",
  ],
  [
    "如何为账户充值？",
    "第一版使用管理员人工充值。注册后联系平台管理员，充值完成后可在钱包中查看入账记录。",
  ],
  [
    "支持哪些 Agent？",
    "任何能发送 HTTP 请求的 Agent 均可使用 REST API。也可以让 Agent 读取平台的 SKILL.md 获取接入说明。",
  ],
];
</script>
<template>
  <main class="landing">
    <section class="hero">
      <div class="binary binary-left" aria-hidden="true">
        {{ "01001011010100100110010110100101 ".repeat(120) }}
      </div>
      <div class="binary binary-right" aria-hidden="true">
        {{ "11010100101011001010110100010101 ".repeat(120) }}
      </div>
      <div class="hero-inner">
        <span class="live-pill"><i /> BUILT FOR AGENTS · READY FOR MORE</span>
        <h1>连接你的 Agent，<br />打开<span>每一种可能。</span></h1>
        <p class="hero-sub">所有工具，一个入口。让想法直接变成行动。</p>
        <div class="agent-row">
          <span>交给你的 Agent</span><span class="agent-symbol">✳</span
          ><span class="agent-symbol">◉</span><span class="agent-symbol">❋</span
          ><span class="agent-symbol">✧</span>
        </div>
        <div class="command">
          <span class="dollar">$</span><code>{{ command }}</code
          ><button
            @click="copy"
            :aria-label="copied ? '已复制' : '复制安装命令'"
          >
            <Check v-if="copied" :size="17" /><Copy v-else :size="17" />
          </button>
        </div>
        <p class="let-it">接下来的，交给它。</p>
        <div class="connection">
          <div class="pixel-bot"><span /><span /></div>
          <div class="connection-line" />
          <span class="hub">T vendo ✳</span>
          <div class="branches"><i /><i /><i /><i /><i /></div>
        </div>
      </div>
      <div class="provider-grid">
        <RouterLink
          v-for="(p, i) in providers"
          :key="p[1]"
          to="/app/tools"
          class="provider"
          ><span class="provider-logo" :style="{ color: p[3] }">{{
            p[0]
          }}</span>
          <div>
            <strong>{{ p[1] }}</strong><small>{{ p[2] }}</small>
          </div>
          <span
            v-if="i === 2"
            class="example-dot"
            title="提供配置示例" /></RouterLink
        ><RouterLink to="/app/tools" class="provider provider-cta"
          >发现你的下一个工具 <ArrowRight :size="18"
        /></RouterLink>
      </div>
      <p class="provider-note">
        生态工具展示 · 可调用的服务以控制台中已启用的配置为准
      </p>
    </section>
    <section class="landing-section pale">
      <div class="section-inner">
        <span class="eyebrow blue">FROM INTENT TO ACTION</span>
        <h2>你说出目标，<br />Agent 找到实现它的工具。</h2>
        <div class="steps">
          <div>
            <span class="step-icon"><Search /></span>
            <h3>发现并比较</h3>
            <code>GET /v1/tools</code>
            <p>浏览可用接口<br />比较能力与价格</p>
          </div>
          <span class="step-line" />
          <div>
            <span class="step-icon"><Play /></span>
            <h3>调用工具</h3>
            <code>POST /v1/run</code>
            <p>统一鉴权，直接调用<br />无需逐个集成</p>
          </div>
          <span class="step-line" />
          <div>
            <span class="step-icon"><CreditCard /></span>
            <h3>按次付费</h3>
            <code>one balance</code>
            <p>成功调用后结算<br />从同一余额扣除</p>
          </div>
        </div>
      </div>
    </section>
    <section class="landing-section">
      <div class="section-inner">
        <span class="eyebrow blue">LESS FRICTION. MORE BUILDING.</span>
        <h2>告别繁琐集成。<br />专注你真正想做的事。</h2>
        <p class="section-sub">一份配置，一个密钥，一个统一的工具账户。</p>
        <div class="stats">
          <div>
            <strong>1<span> API</span></strong>
            <p>统一接入所有已启用工具</p>
          </div>
          <div>
            <strong>on-demand</strong>
            <p>为实际调用付费</p>
          </div>
          <div>
            <strong>1<span> balance</span></strong>
            <p>所有工具共享账户余额</p>
          </div>
        </div>
        <div class="terminal">
          <div class="terminal-bar">
            <span class="terminal-dots">● ● ●</span
            ><span>~/agents/research-bot</span
            ><span class="terminal-label">EXAMPLE SESSION</span>
          </div>
          <div class="terminal-content">
            <p><span class="dim">$</span> 为我寻找关于 AI Agent 的最新信息</p>
            <p class="dim">› GET /v1/tools</p>
            <p>↳ TinyFish · search · $0.001 / call</p>
            <p class="dim">› POST /v1/run</p>
            <p class="dim">
              &nbsp; { tool: "tinyfish", endpoint: "search", parameters: { ... }
              }
            </p>
            <p class="terminal-green">✓ 获得结构化结果</p>
            <p class="terminal-green">✓ 用量已记录 · 余额已更新</p>
            <p><span class="cursor">▌</span></p>
          </div>
        </div>
      </div>
    </section>
    <section class="landing-section pale">
      <div class="section-inner left">
        <span class="eyebrow blue">START WITH A SINGLE CONNECTION</span>
        <h2>以你的方式接入。</h2>
        <p class="section-sub">同一套工具，同一个账户。</p>
        <div class="connect-grid">
          <RouterLink to="/docs?tab=skill"
            ><Zap /><ChevronRight class="card-arrow" :size="18" />
            <h3>Skill</h3>
            <p>一句话，让 Agent 学会使用工具。</p></RouterLink
          ><RouterLink to="/docs?tab=api"
            ><Layers /><ChevronRight class="card-arrow" :size="18" />
            <h3>REST API</h3>
            <p>用熟悉的 HTTP 接入你的应用。</p></RouterLink
          ><RouterLink to="/docs?tab=quickstart"
            ><Terminal /><ChevronRight class="card-arrow" :size="18" />
            <h3>cURL</h3>
            <p>从终端开始你的第一次调用。</p></RouterLink
          >
        </div>
      </div>
    </section>
    <section class="blue-cta">
      <span class="cta-asterisk">✳</span
      ><span class="eyebrow">ONE CONNECTION. ENDLESS POSSIBILITIES.</span>
      <h2>让你的 Agent，多一些超能力。</h2>
      <p>从一个简单的连接开始。</p>
      <RouterLink to="/register" class="btn white"
        >创建免费账户 <ArrowRight :size="18"
      /></RouterLink>
    </section>
    <section class="landing-section faq-section">
      <div class="section-inner">
        <span class="eyebrow blue">GOOD TO KNOW</span>
        <h2>你可能想知道。</h2>
        <div class="faq-list">
          <div v-for="(faq, i) in faqs" :key="i" class="faq">
            <button
              @click="open = open === i ? -1 : i"
              :aria-expanded="open === i"
            >
              {{ faq[0] }}<Minus v-if="open === i" :size="19" /><Plus
                v-else
                :size="19"
              />
            </button>
            <p v-if="open === i">{{ faq[1] }}</p>
          </div>
        </div>
      </div>
    </section>
  </main>
</template>
