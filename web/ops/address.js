(() => {
  const $ = (s, el = document) => el.querySelector(s);
  const esc = (s) => String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

  const state = {
    data: null,
    addr: "",
    activityFilter: "all",
    activityRows: [],
    activityTotal: 0,
    explorer: "https://browser.anubispace.org",
  };

  const METHOD_ZH = {
    PowerDaiReleased: "DAI 算力生效",
    PowerDaiQueued: "DAI 参与(排队)",
    TurbineActivated: "涡轮激活",
    TurbineUnlocked: "TurbineUnlocked",
    StaticClaimed: "领取静态排放",
    ClaimCredited: "领取记账",
    PowerRewardConsumed: "算力奖励消耗",
  };

  const MODULE_ZH = {
    power: "算力",
    emission: "排放",
    turbine: "涡轮",
    nft: "NFT",
    community: "社区",
    governance: "治理",
    ve: "治理",
    burn: "销毁",
  };

  function shortAddr(a) {
    if (!a) return "—";
    return a.slice(0, 6) + "…" + a.slice(-4);
  }

  function num(v, digits = 4) {
    if (v == null || v === "" || v === "—") return "—";
    const n = Number(v);
    if (!Number.isFinite(n)) return String(v);
    if (Math.abs(n) >= 1000) return n.toLocaleString("en-US", { maximumFractionDigits: 2 });
    return n.toLocaleString("en-US", { maximumFractionDigits: digits });
  }

  function fmtBeijing(isoOrSec) {
    if (isoOrSec == null || isoOrSec === "") return "—";
    let d;
    if (typeof isoOrSec === "number") d = new Date(isoOrSec * 1000);
    else d = new Date(isoOrSec);
    if (Number.isNaN(d.getTime())) return "—";
    const parts = new Intl.DateTimeFormat("en-GB", {
      timeZone: "Asia/Shanghai",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).formatToParts(d);
    const get = (t) => parts.find((p) => p.type === t)?.value || "";
    return `${Number(get("month"))}/${Number(get("day"))} ${get("hour")}:${get("minute")}:${get("second")}`;
  }

  function txLink(tx) {
    if (!tx) return "—";
    const href = `${state.explorer}/tx/${tx}`;
    return `<a href="${esc(href)}" target="_blank" rel="noreferrer">↗</a>`;
  }

  function addrLink(a) {
    if (!a) return "—";
    return `<button type="button" class="addr-link-btn" data-jump="${esc(a)}">${esc(shortAddr(a))}</button>`;
  }

  function metric(k, v, hint = "") {
    return `<div class="addr-metric"><span class="k">${esc(k)}</span><span class="v mono">${v}</span>${
      hint ? `<span class="h">${esc(hint)}</span>` : ""
    }</div>`;
  }

  function section(title, body, sub = "") {
    return `<section class="addr-section"><h2>${esc(title)}</h2>${
      sub ? `<p class="sub">${sub}</p>` : ""
    }${body}</section>`;
  }

  async function api(path) {
    const res = await fetch("/api" + path);
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  function renderBalances(b = {}, priceDai) {
    const ail = b.ailgns ?? "—";
    const priceN = Number(priceDai);
    const ailN = Number(ail);
    let ailHint = "";
    if (Number.isFinite(priceN) && Number.isFinite(ailN)) {
      ailHint = `≈ ${num(ailN * priceN, 4)} DAI`;
    } else if (Number.isFinite(priceN)) {
      ailHint = `1 AiLGNS ≈ ${num(priceN, 6)} DAI`;
    }
    const stake = b.longStake != null && b.longStake !== "" ? b.longStake : "—";
    return section("余额", `<div class="addr-metrics">
      ${metric("AiLGNS", esc(num(ail, 6)), ailHint)}
      ${metric("LGNS(A 链)", esc(num(b.lgns, 4)))}
      ${metric("sLGNS", esc(num(b.slgns, 4)))}
      ${metric("DAI(A 链原生/Gas)", esc(num(b.dai, 4)))}
      ${metric("长期质押 LGNS", esc(num(stake, 4)), "LgnsBurnGate.stakeOf")}
    </div>`);
  }

  function renderBurnBonds(bb) {
    if (!bb) return section("可领取", `<p class="sub">暂无数据</p>`);
    const fixed = bb.fixedPrice || {};
    const lgns = bb.lgnsBurn || {};
    const fixedHint = fixed.ok
      ? `${fixed.depositCount ?? 0} 笔债券`
      : (fixed.contract ? shortAddr(fixed.contract) : "");
    const lgnsHint = lgns.ok
      ? `${lgns.depositCount ?? 0} 笔债券`
      : (lgns.contract ? shortAddr(lgns.contract) : "");
    const claims = Array.isArray(bb.claims) ? bb.claims : [];
    const rows = claims.map((r) => `<tr>
      <td class="mono">${esc(fmtBeijing(r.at))}</td>
      <td>${esc(r.label || r.kind || "—")}</td>
      <td class="mono">${esc(num(r.amount, 6))} LGNS</td>
      <td>${txLink(r.tx)}</td>
    </tr>`).join("");
    return section("可领取", `
      <div class="addr-metrics">
        ${metric(fixed.label || "固定价销毁债券", esc(num(fixed.claimable, 4)), fixedHint)}
        ${metric(lgns.label || "LGNS 销毁债券", esc(num(lgns.claimable, 4)), lgnsHint)}
      </div>
      <h2 style="font-size:14px;margin:16px 0 8px">领取记录</h2>
      <div class="addr-table-wrap"><table class="addr-table">
        <thead><tr><th>时间(北京)</th><th>类型</th><th>数量</th><th>交易</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="4">无记录</td></tr>`}</tbody>
      </table></div>`);
  }

  async function copyAddress() {
    const btn = $("#btnAddrCopy");
    const input = $("#addrQueryInput");
    const v = (input?.value || "").trim();
    if (!/^0x[0-9a-fA-F]{40}$/.test(v)) {
      alert("请先输入有效地址");
      return;
    }
    let ok = false;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(v);
        ok = true;
      }
    } catch (_) { /* fallback below */ }
    if (!ok) {
      try {
        input.focus();
        input.select();
        ok = document.execCommand("copy");
        input.setSelectionRange(v.length, v.length);
      } catch (_) { ok = false; }
    }
    if (!ok) {
      alert("复制失败");
      return;
    }
    if (btn) {
      const prev = btn.textContent;
      btn.textContent = "已复制";
      btn.disabled = true;
      setTimeout(() => {
        btn.textContent = prev || "复制";
        btn.disabled = false;
      }, 1200);
    }
  }

  function powerStatus(s) {
    return ({ active: "生效", queued: "排队" })[s] || s || "—";
  }

  function powerShareHint(activePower, networkPower) {
    const a = Number(activePower);
    const n = Number(networkPower);
    if (!Number.isFinite(a) || !Number.isFinite(n) || n <= 0) return "Power";
    const pct = (a / n) * 100;
    const pctStr = pct < 0.000001 ? pct.toExponential(2) : pct.toFixed(6);
    return `占全网 ${pctStr}%`;
  }

  function renderPower(p, networkActivePower) {
    if (!p) return section("算力", `<p class="sub">暂无数据</p>`);
    const s = p.summary || {};
    const rows = (p.records || []).map((r) => `<tr>
      <td class="mono">${esc(fmtBeijing(r.ts))}</td>
      <td>${esc(METHOD_ZH[r.method] || r.method || "—")}</td>
      <td class="mono">${r.daiAmount != null ? esc(num(r.daiAmount, 2) + " DAI") : "—"}</td>
      <td class="mono">${esc(num(r.power, 2))}</td>
      <td><span class="addr-pill ${r.status === "active" ? "ok" : ""}">${esc(powerStatus(r.status))}</span></td>
      <td>${txLink(r.tx)}</td>
    </tr>`).join("");
    return section("算力", `
      <div class="addr-metrics">
        ${metric("生效算力", esc(num(s.activePower, 2)), powerShareHint(s.activePower, networkActivePower))}
        ${metric("待生效算力", esc(num(s.pendingPower, 2)), "Power")}
        ${metric("累计算力", esc(num(s.totalPower, 2)), `${s.records ?? 0} 条记录`)}
        ${metric("累计投入", esc(num(s.totalDai, 2)), "DAI")}
      </div>
      <div class="addr-table-wrap"><table class="addr-table">
        <thead><tr><th>时间(北京)</th><th>类型</th><th>数量</th><th>算力</th><th>状态</th><th>交易</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="6">无记录</td></tr>`}</tbody>
      </table></div>`);
  }

  function renderTeam(c, level) {
    if (!c && !level) return section("团队与等级", `<p class="sub">暂无数据</p>`);
    c = c || {};
    level = level || {};
    const lvl = level.level;
    const lvlLabel = lvl === 0 || lvl === "0" ? "无等级" : (lvl ?? "—");
    const hint = [
      level.veUsd != null ? `治理锁仓 $${num(level.veUsd, 2)}` : "",
      level.reason || "",
    ].filter(Boolean).join(" · ");
    const directs = (c.directs || []).map((d) => `<tr>
      <td>${addrLink(d.address)}</td>
      <td class="mono">${esc(num(d.activePower, 2))}</td>
      <td class="mono">${esc(fmtBeijing(d.boundAt))}</td>
    </tr>`).join("");
    const sub = `上级: ${c.referrer ? `<button type="button" class="addr-link-btn" data-jump="${esc(c.referrer)}">${esc(c.referrer)}</button>` : "—"} · 社区层级 ${esc(c.level ?? "—")}`;
    return section("团队与等级", `
      <div class="addr-metrics">
        ${metric("团队人数", esc(c.team ?? "—"), "人")}
        ${metric("直推人数", esc(c.direct ?? "—"), "人")}
        ${metric("团队有效算力", esc(num(c.teamActivePower, 2)), "Power")}
        ${metric("等级", esc(lvlLabel), hint)}
      </div>
      <details class="addr-directs" open>
        <summary>直推明细(${c.direct ?? 0} 人,按有效算力排序)</summary>
        <div class="addr-table-wrap"><table class="addr-table">
          <thead><tr><th>地址</th><th>有效算力</th><th>绑定时间</th></tr></thead>
          <tbody>${directs || `<tr><td colspan="3">无</td></tr>`}</tbody>
        </table></div>
      </details>`, sub);
  }

  function claimApprox(net, priceDai) {
    const n = Number(net);
    const p = Number(priceDai);
    if (!Number.isFinite(n) || !Number.isFinite(p)) return "—";
    return `≈ ${num(n * p, 4)} DAI`;
  }

  function renderEmission(em, claims, turbine, nft, community, priceDai) {
    const consumed = em?.accrued?.consumedTotal ?? claims?.totalConsumed ?? "—";
    const actN = turbine?.totals?.activations ?? 0;
    const daiSpent = turbine?.totals?.daiSpent;
    let nftCount = 0;
    if (Array.isArray(nft?.items)) nftCount = nft.items.length;
    else if (Array.isArray(nft?.records)) nftCount = nft.records.length;
    else if (typeof nft?.count === "number") nftCount = nft.count;
    const growth = community?.rewardsCum?.growth;
    const records = claims?.records || [];
    const rows = records.map((r) => `<tr>
        <td class="mono">${esc(fmtBeijing(r.ts))}</td>
        <td class="mono">${esc(num(r.net, 6))} AiLGNS</td>
        <td class="mono">${esc(num(r.net, 6))}</td>
        <td class="mono">${esc(claimApprox(r.net, priceDai))}</td>
        <td>${txLink(r.tx)}</td>
      </tr>`).join("");
    return section("排放与领取", `
      <div class="addr-metrics">
        ${metric("累计已领取(记账)", esc(num(consumed, 6)), "AiLGNS")}
        ${metric("涡轮激活次数", esc(actN), daiSpent != null ? `共花 ${num(daiSpent, 2)} DAI` : "")}
        ${metric("生产 NFT", esc(nftCount))}
        ${metric("社区池累计记账", esc(num(growth, 6)), "AiLGNS")}
      </div>
      <h2 style="font-size:14px;margin:16px 0 8px">领取记录</h2>
      <div class="addr-metrics" style="margin-bottom:8px">
        ${metric("领取次数", esc(claims?.count ?? records.length))}
        ${metric("累计领取", esc(num(claims?.totalNet ?? consumed, 6)), "AiLGNS")}
      </div>
      <div class="addr-table-wrap"><table class="addr-table">
        <thead><tr><th>时间(北京)</th><th>领取数量</th><th>领取后累计</th><th>约值</th><th>交易</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="5">无记录</td></tr>`}</tbody>
      </table></div>`);
  }

  function renderBurnGate(bg, balances = {}) {
    const item = (ok, title, meta) => {
      const mark = ok ? "✓" : "✗";
      const cls = ok ? "ok" : "bad";
      return `<li>
        <span class="mark ${cls}">${mark}</span>
        <div><div>${esc(title)}</div><div class="meta">${esc(meta)}</div></div>
        <span class="mono">${esc(ok ? "通过" : "未过")}</span>
      </li>`;
    };
    if (!bg) {
      return section("销毁 500 LGNS 资格",
        `<p class="addr-banner">链上读取失败</p>
         <ul class="addr-check-list">
           <li class="pending"><span class="mark pending">?</span>
             <div><div>资格清单不可用</div><div class="meta">burnGate eth_call 未返回</div></div>
             <span class="mono">失败</span></li>
         </ul>`);
    }
    const minStake = bg.minStake ?? "20000";
    const perBurn = bg.perBurn ?? "500";
    const allOk = bg.okStake && bg.okEpoch && bg.okBal && bg.okAllow && bg.okGas && bg.okQuota;
    const banner = allOk
      ? `<p class="addr-banner ok">✓ 符合销毁 ${esc(perBurn)} LGNS 的条件</p>`
      : `<p class="addr-banner">✗ 暂不符合销毁 ${esc(perBurn)} LGNS 的条件</p>`;
    const epochMeta = bg.burnEpoch != null && bg.epoch != null
      ? `burnEpoch=${bg.burnEpoch} · currentEpoch=${bg.epoch}`
      : "纪元烧毁状态";
    const lgnsBal = balances.lgns ?? "—";
    return section("销毁 500 LGNS 资格", `${banner}<ul class="addr-check-list">
      ${item(!!bg.okStake, `长期质押 ≥ ${num(minStake, 0)} LGNS`, `stakeOf=${num(bg.stake, 4)} · min=${num(minStake, 0)}`)}
      ${item(!!bg.okEpoch, "本纪元(7 天)还没烧过", epochMeta)}
      ${item(!!bg.okBal, `A 链 LGNS 余额 ≥ ${num(perBurn, 0)}`, `LGNS.balanceOf · ${lgnsBal} LGNS`)}
      ${item(!!bg.okAllow, "已授权算力账本使用 LGNS", `allowance=${num(bg.allowance, 4)} · 需 ≥ ${num(perBurn, 0)}`)}
      ${item(!!bg.okGas, "Gas(Anubis 链原生 DAI)", `建议 ≥ 0.02 DAI · ${num(bg.gasDai, 4)} DAI`)}
      ${item(!!bg.okQuota, "今日全网剩余额度", `剩余 ${num(bg.quotaLeft, 0)} / 日上限 ${num(bg.maxDaily, 0)} · 今日已烧 ${num(bg.burnedToday, 0)}`)}
    </ul>`);
  }

  function renderRewards(rw) {
    if (!rw) return section("手续费分红与激励", `<p class="sub">暂无数据</p>`);
    const feeN = Array.isArray(rw.feeClaims?.records) ? rw.feeClaims.records.length : 0;
    const voted = Array.isArray(rw.epochsVoted) ? rw.epochsVoted.length : 0;
    return section("手续费分红与激励", `<div class="addr-metrics">
      ${metric("手续费分红已领", esc(feeN), `${feeN} 次领取`)}
      ${metric("DAO 激励已领", esc(num(rw.daoIncentive?.claimed, 6)), "AiLGNS")}
      ${metric("LFEO 已领", esc(num(rw.lfeo?.claimed, 6)))}
      ${metric("参与投票纪元", esc(voted), voted ? "" : "没投过票")}
    </div>`);
  }

  function lockStatus(lock) {
    if (lock.unlocked) return { t: "已解锁", c: "ok" };
    const at = lock.unlockAt ? new Date(lock.unlockAt).getTime() : 0;
    if (at && at <= Date.now()) return { t: "可解锁", c: "warn" };
    return { t: "锁定中", c: "" };
  }

  function renderTurbine(t) {
    if (!t) return section("涡轮记录", `<p class="sub">暂无数据</p>`);
    const tot = t.totals || {};
    const rows = (t.locks || []).map((l) => {
      const st = lockStatus(l);
      return `<tr>
        <td class="mono">${esc(l.lockId)}</td>
        <td class="mono">${esc(num(l.amount, 4))}</td>
        <td class="mono">${esc(fmtBeijing(l.activatedAt))}</td>
        <td class="mono">${esc(fmtBeijing(l.unlockAt))}</td>
        <td><span class="addr-pill ${st.c}">${esc(st.t)}</span></td>
        <td>${txLink(l.tx)}</td>
      </tr>`;
    }).join("");
    return section("涡轮记录", `
      <div class="addr-metrics">
        ${metric("累计花费", esc(num(tot.daiSpent, 2)), `DAI · ${tot.activations ?? 0} 次激活`)}
        ${metric("累计激活", esc(num(tot.activated, 4)), "AiLGNS")}
        ${metric("当前锁定", esc(num(tot.locked, 4)))}
      </div>
      <div class="addr-table-wrap"><table class="addr-table">
        <thead><tr><th>#</th><th>数量 AiLGNS</th><th>激活时间</th><th>解锁时间</th><th>状态</th><th>交易</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="6">无记录</td></tr>`}</tbody>
      </table></div>`);
  }

  function renderVe(ve) {
    if (!ve) return section("治理锁仓仓位", `<p class="sub">暂无数据</p>`);
    const positions = ve.positions || [];
    let perm = 0;
    let term = 0;
    for (const p of positions) {
      const n = Number(p.principal) || 0;
      if (Number(p.kind) === 1) perm += n;
      else term += n;
    }
    const rows = positions.map((p) => `<tr>
      <td class="mono">${esc(p.positionId)}</td>
      <td>${Number(p.kind) === 1 ? "永久" : "定期"}</td>
      <td class="mono">${esc(num(p.principal, 4))}</td>
      <td class="mono">${esc(p.startEpoch ?? "—")}</td>
      <td class="mono">${esc(p.lockEpochs ?? "—")} 纪元</td>
      <td class="mono">${esc((Number(p.startEpoch) || 0) + (Number(p.lockEpochs) || 0))}</td>
      <td class="mono">${esc(fmtBeijing(p.createdAt))}</td>
      <td>${txLink(p.tx)}</td>
    </tr>`).join("");
    return section("治理锁仓仓位", `
      <p class="sub">kind 0 定期 · kind 1 永久;到期纪元 = 开始 + 锁定纪元数</p>
      <div class="addr-metrics">
        ${metric("治理身份", ve.hasIdentity ? "有" : "无", ve.hasIdentity ? "锁仓价值门槛" : "")}
        ${metric("永久锁仓", esc(num(perm, 4)), "AiLGNS")}
        ${metric("定期锁仓", esc(num(term, 4)), `${positions.length} 笔仓位`)}
      </div>
      <div class="addr-table-wrap"><table class="addr-table">
        <thead><tr><th>#</th><th>类型</th><th>本金</th><th>开始纪元</th><th>锁定</th><th>到期纪元</th><th>创建</th><th>交易</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="8">无仓位</td></tr>`}</tbody>
      </table></div>`);
  }

  function filteredActivity() {
    const f = state.activityFilter;
    if (f === "all") return state.activityRows;
    const map = {
      power: ["power"],
      emission: ["emission"],
      turbine: ["turbine"],
      nft: ["nft"],
      community: ["community"],
      governance: ["governance", "ve"],
      burn: ["burn"],
    };
    const mods = map[f] || [f];
    return state.activityRows.filter((r) => mods.includes(String(r.module || "").toLowerCase()));
  }

  function renderActivity() {
    const filters = [
      ["all", "全部"],
      ["power", "算力"],
      ["emission", "排放"],
      ["turbine", "涡轮"],
      ["nft", "NFT"],
      ["community", "社区"],
      ["governance", "治理"],
      ["burn", "销毁"],
    ];
    const rows = filteredActivity().map((r) => `<tr>
      <td class="mono">${esc(fmtBeijing(r.ts))}</td>
      <td>${esc(MODULE_ZH[r.module] || r.module || "—")}</td>
      <td>${esc(METHOD_ZH[r.method] || r.method || "—")}</td>
      <td class="mono">${esc(num(r.amount, 6))}</td>
      <td>${txLink(r.tx)}</td>
    </tr>`).join("");
    const shown = state.activityRows.length;
    const total = state.activityTotal || shown;
    const more = shown < total
      ? `<button type="button" class="btn secondary sm" id="addrLoadMore">加载更多(已显示 ${shown} / ${total})</button>`
      : `<span class="sub">已显示 ${shown} / ${total}</span>`;
    return section("链上活动", `
      <p class="sub">共 ${esc(total)} 条</p>
      <div class="addr-filters">${filters.map(([k, lab]) =>
        `<button type="button" data-filter="${k}" class="${state.activityFilter === k ? "active" : ""}">${lab}</button>`
      ).join("")}</div>
      <div style="display:flex;justify-content:space-between;align-items:center;gap:8px;margin-bottom:8px">${more}</div>
      <div class="addr-table-wrap"><table class="addr-table">
        <thead><tr><th>时间(北京)</th><th>模块</th><th>动作</th><th>数量</th><th>交易</th></tr></thead>
        <tbody>${rows || `<tr><td colspan="5">无记录</td></tr>`}</tbody>
      </table></div>`);
  }

  function renderAll(data) {
    const body = $("#addrDlgBody");
    if (!body) return;
    const errs = data.errors || [];
    const errHtml = errs.length
      ? `<div class="addr-errors">部分数据源失败: ${esc(errs.map((e) => e.source).join(", "))}</div>`
      : "";
    body.innerHTML = [
      errHtml,
      renderBalances(data.balances, data.ailgnsPriceDai),
      renderBurnBonds(data.burnBonds),
      renderPower(data.power, data.networkActivePower),
      renderTeam(data.community, data.level),
      renderEmission(data.emission, data.claims, data.turbine, data.nft, data.community, data.ailgnsPriceDai),
      renderBurnGate(data.burnGate, data.balances),
      renderRewards(data.rewards),
      renderTurbine(data.turbine),
      renderVe(data.ve),
      renderActivity(),
    ].join("");
  }

  function isPageMode() {
    return document.body.classList.contains("addr-page");
  }

  async function loadAddress(addr, { keepOpen } = {}) {
    const dlg = $("#addressDialog");
    const input = $("#addrQueryInput");
    const explorer = $("#addrExplorerLink");
    const body = $("#addrDlgBody");
    if (!body) return;
    if (!isPageMode() && !dlg) return;
    state.addr = addr;
    state.activityFilter = "all";
    if (input) input.value = addr;
    if (typeof window.state?.explorer === "string") state.explorer = window.state.explorer;
    if (isPageMode()) {
      document.title = `查地址 ${shortAddr(addr)} · anubisbot`;
      const url = new URL(location.href);
      url.searchParams.set("address", addr);
      history.replaceState(null, "", url);
    } else if (dlg && !dlg.open) {
      dlg.showModal();
    }
    body.innerHTML = `<div class="addr-loading">加载中…</div>`;
    try {
      if (!window.state?.explorer) {
        try {
          const h = await api("/health");
          if (h.explorer) state.explorer = h.explorer;
        } catch (_) { /* ignore */ }
      }
      if (explorer) {
        explorer.href = `${state.explorer}/address/${addr}`;
        explorer.hidden = false;
      }
      const data = await api(`/address/${addr}`);
      state.data = data;
      state.activityRows = data.activity?.rows || [];
      state.activityTotal = data.activity?.total || state.activityRows.length;
      renderAll(data);
    } catch (err) {
      body.innerHTML = `<div class="addr-errors">${esc(err.message || err)}</div>`;
    }
  }

  async function loadMoreActivity() {
    const addr = state.addr;
    if (!addr) return;
    const offset = state.activityRows.length;
    const data = await api(`/address/${addr}/activity?limit=40&offset=${offset}`);
    const rows = data.rows || [];
    state.activityRows = state.activityRows.concat(rows);
    if (typeof data.total === "number") state.activityTotal = data.total;
    if (state.data) {
      state.data.activity = { ...(state.data.activity || {}), rows: state.activityRows, total: state.activityTotal };
      renderAll(state.data);
    }
  }

  function bindUI() {
    const root = isPageMode() ? document.body : $("#addressDialog");
    if (!root || root.dataset.bound) return;
    root.dataset.bound = "1";
    const dlg = $("#addressDialog");
    if (dlg) {
      dlg.addEventListener("click", (e) => {
        if (e.target === dlg) dlg.close();
      });
    }
    $("#btnCloseAddress")?.addEventListener("click", () => dlg?.close());
    $("#btnAddrQuery")?.addEventListener("click", () => {
      const v = ($("#addrQueryInput")?.value || "").trim();
      if (/^0x[0-9a-fA-F]{40}$/.test(v)) loadAddress(v);
    });
    $("#btnAddrCopy")?.addEventListener("click", () => { copyAddress().catch(() => {}); });
    $("#addrQueryInput")?.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        $("#btnAddrQuery")?.click();
      }
    });
    $("#btnAddrRefresh")?.addEventListener("click", () => {
      if (state.addr) loadAddress(state.addr, { keepOpen: true });
    });
    $("#addrDlgBody")?.addEventListener("click", async (e) => {
      const jump = e.target.closest("[data-jump]");
      if (jump) {
        loadAddress(jump.dataset.jump);
        return;
      }
      const filterBtn = e.target.closest("[data-filter]");
      if (filterBtn) {
        state.activityFilter = filterBtn.dataset.filter;
        if (state.data) renderAll(state.data);
        return;
      }
      if (e.target.id === "addrLoadMore" || e.target.closest("#addrLoadMore")) {
        try {
          await loadMoreActivity();
        } catch (err) {
          alert(err.message || err);
        }
      }
    });
  }

  window.openAddressDetail = function openAddressDetail(addr) {
    if (!addr || !/^0x[0-9a-fA-F]{40}$/.test(addr)) return;
    bindUI();
    loadAddress(addr);
  };

  function boot() {
    bindUI();
    const bootAddr = new URLSearchParams(location.search).get("address");
    if (bootAddr && /^0x[0-9a-fA-F]{40}$/.test(bootAddr)) {
      loadAddress(bootAddr);
    } else if (isPageMode()) {
      $("#addrDlgBody").innerHTML = `<div class="addr-loading">粘贴地址后点「查询」</div>`;
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
