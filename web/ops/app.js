const state = {
  health: null,
  presets: {},
  tasks: [],
  wallets: [],
  balances: {},
  logs: [],
  rpcList: [],
  activeIds: [],
  rtts: {},
  leads: {},
  allowByTask: {},
  selectedTaskId: null,
  selectedPresetId: null,
  editingTaskId: null,
  taskFilter: "",
  explorer: "https://browser.anubispace.org",
  sig: { tasks: "", wallets: "", logs: "", rpc: "" },
};

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];

async function api(path, opts = {}) {
  const res = await fetch("/api" + path, {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function toast(msg, bad = false) {
  const box = $("#toasts");
  if (!box) return;
  const el = document.createElement("div");
  el.className = "toast" + (bad ? " bad" : "");
  el.textContent = msg;
  box.appendChild(el);
  // popover 进入顶层，避免被 <dialog showModal> 挡住
  if (typeof box.showPopover === "function") {
    try {
      if (!box.matches(":popover-open")) box.showPopover();
    } catch {
      /* ignore */
    }
  }
  setTimeout(() => {
    el.remove();
    if (typeof box.hidePopover === "function" && !box.childElementCount) {
      try { box.hidePopover(); } catch { /* ignore */ }
    }
  }, 4000);
}

function statusLabel(s) {
  return ({
    idle: "空闲",
    armed: "待执行",
    fired: "已提交",
    success: "成功",
    failed: "失败",
    skipped: "跳过",
  })[s] || s;
}

function fmtFireAt(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

function fireAtInputValue(iso) {
  if (!iso) return "";
  const s = fmtFireAt(iso);
  return s === "—" ? "" : s;
}

function parseFireAtText(text) {
  const raw = String(text || "").trim();
  if (!raw) return null;
  const m = raw.match(/^(\d{4})[/-](\d{1,2})[/-](\d{1,2})(?:[ T](\d{1,2}):(\d{1,2})(?::(\d{1,2}))?)?$/);
  if (m) {
    const d = new Date(
      Number(m[1]),
      Number(m[2]) - 1,
      Number(m[3]),
      Number(m[4] || 0),
      Number(m[5] || 0),
      Number(m[6] || 0),
    );
    return Number.isNaN(d.getTime()) ? null : d;
  }
  const d = new Date(raw);
  return Number.isNaN(d.getTime()) ? null : d;
}

function scheduleLabel(s) {
  return ({ hourly: "每整点", daily_1159_cst: "每日 11:59", once: "仅一次" })[s] || s;
}

function shortAddr(a) {
  if (!a) return "—";
  return a.slice(0, 6) + "…" + a.slice(-4);
}

const COPY_ICON_SVG = `<svg class="addr-copy-icon" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`;
const EDIT_ICON_SVG = `<svg class="addr-copy-icon" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>`;

function addrCell(address) {
  if (!address) return `<td class="addr-cell mono">—</td>`;
  const full = escapeHtml(address);
  return `<td class="addr-cell mono"><span class="addr-inline"><button type="button" class="addr-link-btn" data-act="open-addr" data-addr="${full}" data-full="${full}" aria-label="${full}">${escapeHtml(shortAddr(address))}</button><button type="button" class="addr-copy-btn" data-act="copy-addr" data-addr="${full}" title="复制" aria-label="复制">${COPY_ICON_SVG}</button></span></td>`;
}

let addrHoverHideTimer = 0;
let addrHoverAnchor = null;

function clearAddrHoverHideTimer() {
  if (addrHoverHideTimer) {
    clearTimeout(addrHoverHideTimer);
    addrHoverHideTimer = 0;
  }
}

function getAddrHoverTip() {
  let tip = document.getElementById("addrHoverTip");
  if (!tip) {
    tip = document.createElement("div");
    tip.id = "addrHoverTip";
    tip.hidden = true;
    document.body.appendChild(tip);
  }
  if (!tip.dataset.bound) {
    tip.dataset.bound = "1";
    tip.addEventListener("mouseenter", () => {
      clearAddrHoverHideTimer();
    });
    tip.addEventListener("mouseleave", (e) => {
      const to = e.relatedTarget;
      if (to && addrHoverAnchor && addrHoverAnchor.contains(to)) return;
      if (to && tip.contains(to)) return;
      scheduleHideAddrHoverTip(200);
    });
    tip.addEventListener("mousedown", (e) => e.stopPropagation());
    tip.addEventListener("click", (e) => e.stopPropagation());
  }
  return tip;
}

function hideAddrHoverTip() {
  clearAddrHoverHideTimer();
  addrHoverAnchor = null;
  const tip = document.getElementById("addrHoverTip");
  if (tip) {
    tip.hidden = true;
    tip.textContent = "";
    delete tip.dataset.anchor;
  }
}

function scheduleHideAddrHoverTip(ms) {
  clearAddrHoverHideTimer();
  addrHoverHideTimer = setTimeout(() => {
    addrHoverHideTimer = 0;
    hideAddrHoverTip();
  }, ms);
}

function placeAddrHoverTip(anchor) {
  const full = (anchor?.dataset?.full || "").trim();
  if (!full || !anchor) {
    hideAddrHoverTip();
    return;
  }
  clearAddrHoverHideTimer();
  addrHoverAnchor = anchor;
  const tip = getAddrHoverTip();
  tip.textContent = full;
  tip.hidden = false;
  tip.dataset.anchor = "1";
  const r = anchor.getBoundingClientRect();
  const pad = 6;
  const tw = tip.offsetWidth;
  const th = tip.offsetHeight;
  let left = r.left;
  let top = r.top - th - pad;
  if (top < 8) top = r.bottom + pad;
  if (left + tw > window.innerWidth - 8) {
    left = Math.max(8, window.innerWidth - tw - 8);
  }
  if (left < 8) left = 8;
  if (top + th > window.innerHeight - 8) {
    top = Math.max(8, window.innerHeight - th - 8);
  }
  if (top < 8) top = 8;
  tip.style.left = `${Math.round(left)}px`;
  tip.style.top = `${Math.round(top)}px`;
}

function bindAddrHoverTip() {
  if (document.documentElement.dataset.addrTipBound) return;
  document.documentElement.dataset.addrTipBound = "1";
  const match = (t) => t?.closest?.(".addr-link-btn[data-full]");
  const isTip = (t) => t?.closest?.("#addrHoverTip");
  document.addEventListener("mouseover", (e) => {
    const btn = match(e.target);
    if (!btn) return;
    placeAddrHoverTip(btn);
  });
  document.addEventListener("mouseout", (e) => {
    const btn = match(e.target);
    if (!btn) return;
    const to = e.relatedTarget;
    if (to && btn.contains(to)) return;
    if (to && isTip(to)) return;
    scheduleHideAddrHoverTip(200);
  });
  document.addEventListener("focusin", (e) => {
    const btn = match(e.target);
    if (btn) placeAddrHoverTip(btn);
  });
  document.addEventListener("focusout", (e) => {
    const btn = match(e.target);
    if (!btn) return;
    const to = e.relatedTarget;
    if (to && isTip(to)) return;
    scheduleHideAddrHoverTip(200);
  });
  window.addEventListener("scroll", hideAddrHoverTip, true);
  window.addEventListener("resize", hideAddrHoverTip);
}

async function copyAddrFromBtn(btn) {
  const addr = (btn?.dataset?.addr || "").trim();
  if (!/^0x[0-9a-fA-F]{40}$/.test(addr)) return;
  let ok = false;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(addr);
      ok = true;
    }
  } catch (_) { /* fallback */ }
  if (!ok) {
    try {
      const ta = document.createElement("textarea");
      ta.value = addr;
      ta.style.position = "fixed";
      ta.style.left = "-9999px";
      document.body.appendChild(ta);
      ta.select();
      ok = document.execCommand("copy");
      ta.remove();
    } catch (_) { ok = false; }
  }
  if (!ok) {
    toast("复制失败", true);
    return;
  }
  toast("已复制");
  const prevTitle = btn.getAttribute("title") || "复制";
  btn.setAttribute("title", "已复制");
  setTimeout(() => { btn.setAttribute("title", prevTitle); }, 1000);
}

function fmtCountdown(targetISO) {
  if (!targetISO) return "--:--";
  const t = new Date(targetISO).getTime() - Date.now();
  if (t <= 0) return "00:00";
  const s = Math.floor(t / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return h > 0 ? `${pad(h)}:${pad(m)}:${pad(sec)}` : `${pad(m)}:${pad(sec)}`;
}

/** Roll an expired absolute target forward by stepMs until it is in the future. */
function rollCountdownTarget(iso, stepMs) {
  if (!iso || !stepMs) return iso;
  let ms = new Date(iso).getTime();
  if (Number.isNaN(ms)) return iso;
  const now = Date.now();
  if (ms > now) return iso;
  while (ms <= now) ms += stepMs;
  return new Date(ms).toISOString();
}

function setTextQuiet(el, text) {
  if (el && el.textContent !== text) el.textContent = text;
}

function escapeHtml(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function fingerprint(v) {
  return JSON.stringify(v);
}

function presetFnName(p) {
  return (p?.function || p?.method || "").trim();
}

function parseAbiFunctions(abiText) {
  let arr;
  try {
    arr = JSON.parse(String(abiText || "").trim() || "[]");
  } catch {
    return { error: "ABI 不是合法 JSON" };
  }
  if (!Array.isArray(arr)) return { error: "ABI 须为 JSON 数组" };
  const fns = arr.filter((x) => x && x.type === "function" && x.name);
  if (!fns.length) return { error: "ABI 中未找到 function" };
  return { fns };
}

function resolveAbiFn(abiText, preferred) {
  const parsed = parseAbiFunctions(abiText);
  if (parsed.error) return parsed;
  const want = String(preferred || "").trim();
  if (want) {
    const hit = parsed.fns.find((f) => f.name === want);
    if (!hit) return { error: `函数 ${want} 不在 ABI 中` };
    return { fn: hit, fns: parsed.fns };
  }
  if (parsed.fns.length === 1) return { fn: parsed.fns[0], fns: parsed.fns };
  return { error: "ABI 含多个函数，请填写函数名", fns: parsed.fns };
}

function abiArgDefaults(fn, preset) {
  const inputs = fn?.inputs || [];
  const fromArgs = Array.isArray(preset?.defaultParams?.args) ? preset.defaultParams.args : [];
  return inputs.map((inp, i) => {
    if (fromArgs[i] != null && fromArgs[i] !== "") return String(fromArgs[i]);
    const name = presetFnName(preset);
    if (i === 0 && name === "claim" && preset?.defaultParams?.claimArg != null) {
      return String(preset.defaultParams.claimArg);
    }
    if (i === 0 && name === "burnForPower" && preset?.defaultParams?.burnAmount) {
      return String(preset.defaultParams.burnAmount);
    }
    if (inp.type?.startsWith("uint") || inp.type?.startsWith("int")) return "0";
    return "";
  });
}

function renderArgFields(container, fn, values, namePrefix) {
  if (!container) return;
  const inputs = fn?.inputs || [];
  if (!inputs.length) {
    container.innerHTML = `<p class="desc">该函数无参数</p>`;
    return;
  }
  container.innerHTML = inputs.map((inp, i) => {
    const label = `${inp.name || `arg${i}`} (${inp.type || "?"})`;
    const val = values[i] ?? "";
    return `<label>${escapeHtml(label)}<input name="${namePrefix}${i}" value="${escapeHtml(val)}" autocomplete="off" /></label>`;
  }).join("");
}

function collectArgFields(root, prefix, count) {
  const out = [];
  for (let i = 0; i < count; i++) {
    const el = root.querySelector(`[name="${prefix}${i}"]`);
    out.push(String(el?.value ?? "").trim());
  }
  return out;
}

function updatePresetAbiUI() {
  const abiText = $("#presetAbiInput")?.value || "";
  const fnName = $("#presetFunctionInput")?.value || "";
  const meta = $("#presetAbiMeta");
  const box = $("#presetArgsFields");
  const resolved = resolveAbiFn(abiText, fnName);
  if (resolved.error) {
    if (meta) meta.textContent = resolved.error;
    if (box) box.innerHTML = "";
    return null;
  }
  const sig = `${resolved.fn.name}(${(resolved.fn.inputs || []).map((x) => x.type).join(",")})`;
  if (meta) meta.textContent = `函数 ${sig}` + (resolved.fns.length > 1 ? ` · 共 ${resolved.fns.length} 个函数` : "");
  if (!$("#presetFunctionInput").value) $("#presetFunctionInput").value = resolved.fn.name;
  const preset = state.selectedPresetId ? state.presets[state.selectedPresetId] : null;
  const vals = abiArgDefaults(resolved.fn, {
    ...(preset || {}),
    function: $("#presetFunctionInput").value || resolved.fn.name,
    defaultParams: {
      ...(preset?.defaultParams || {}),
      args: collectExistingPresetArgDraft() || preset?.defaultParams?.args,
    },
  });
  renderArgFields(box, resolved.fn, vals, "arg");
  return resolved.fn;
}

function collectExistingPresetArgDraft() {
  const box = $("#presetArgsFields");
  if (!box) return null;
  const inputs = [...box.querySelectorAll("input[name^='arg']")];
  if (!inputs.length) return null;
  return inputs.map((el) => el.value);
}

function rttDotClass(rtt, green, yellow) {
  if (!rtt || rtt <= 0) return "";
  if (rtt < green) return "ok";
  if (rtt < yellow) return "warn";
  return "bad";
}

function hostFromUrl(url) {
  try { return new URL(url).host; } catch { return url || "RPC"; }
}

function renderHealthChips() {
  const h = state.health;
  if (!h) return;
  $("#modeToggle").checked = h.mode === "live";
  $("#modeLabel").textContent = (h.mode || "sim").toUpperCase();
  const ids = state.activeIds.length ? state.activeIds : (h.activeIds || []);
  const rtts = h.rpcsRtt || state.rtts || {};
  const parts = ids.map((id) => rtts[id] || 0).filter((n) => n > 0);
  setTextQuiet($("#leadText"), parts.length ? `${parts.join("/")} ms` : "— ms");
  const off = h.offsetMs ?? 0;
  $("#offsetText").textContent = `offset ${off >= 0 ? "+" : ""}${off}ms`;
  renderRpcMulti(false);
}

function renderRpcMulti(force = false) {
  const list = state.rpcList || [];
  const active = new Set(state.activeIds || []);
  const green = state.health?.rttGreen || 150;
  const yellow = state.health?.rttYellow || 400;
  const structSig = fingerprint(list.map((r) => r.id + "|" + r.url));
  const panel = $("#rpcMultiPanel");
  const needRebuild = force || structSig !== state.sig.rpc;
  if (needRebuild) {
    state.sig.rpc = structSig;
    panel.innerHTML = list.map((r) => {
      const checked = active.has(r.id) ? "checked" : "";
      return `<label class="rpc-row" data-id="${escapeHtml(r.id)}">
        <input type="checkbox" ${checked} />
        <span class="rpc-url mono" title="${escapeHtml(r.url)}">${escapeHtml(r.url || r.name)}</span>
        <i class="dot" data-dot></i>
        <span class="rpc-rtt mono" data-rtt>— ms</span>
      </label>`;
    }).join("") || `<div class="rpc-empty">暂无 RPC</div>`;
  }

  // update dots/rtt/checks without full rebuild when possible
  $$(".rpc-row", panel).forEach((row) => {
    const id = row.dataset.id;
    const rtt = state.rtts[id] || 0;
    const selected = active.has(id);
    const cb = row.querySelector("input");
    if (cb && cb.checked !== selected) cb.checked = selected;
    const dot = row.querySelector("[data-dot]");
    if (dot) {
      const cls = selected ? rttDotClass(rtt, green, yellow) : "";
      dot.className = "dot " + cls;
    }
    const rttEl = row.querySelector("[data-rtt]");
    if (rttEl) rttEl.textContent = selected && rtt > 0 ? `${rtt} ms` : "— ms";
  });

  const primary = list.find((r) => r.id === state.activeIds[0]) || list[0];
  const n = state.activeIds.length;
  $("#rpcMultiLabel").textContent = n
    ? `已选 ${n} · ${hostFromUrl(primary?.url || primary?.name || "")}`
    : "选择 RPC";
}

function filteredTasks() {
  const q = (state.taskFilter || "").trim().toLowerCase();
  if (!q) return state.tasks;
  return state.tasks.filter((t) => {
    const preset = state.presets[t.taskType] || {};
    const label = preset.label || t.taskType || "";
    const hay = [
      t.walletName,
      t.walletAddress,
      t.name,
      t.taskType,
      label,
    ].map((x) => String(x || "").toLowerCase()).join(" ");
    return hay.includes(q);
  });
}

function renderTasks(force = false) {
  const body = $("#taskBody");
  const empty = $("#taskEmpty");
  const list = filteredTasks();
  const sig = fingerprint({
    filter: state.taskFilter,
    rows: list.map((t) => [
      t.id, t.enabled, t.name, t.status, t.fireAt, t.schedule, t.taskType, t.walletName, t.walletAddress,
      unreadRecordOutcome(t.id),
    ]),
  });
  if (!force && sig === state.sig.tasks) return;
  state.sig.tasks = sig;

  if (!state.tasks.length) {
    body.innerHTML = "";
    empty.textContent = "还没有任务。点击「新建任务」开始。";
    empty.classList.add("show");
    return;
  }
  if (!list.length) {
    body.innerHTML = "";
    empty.textContent = "没有匹配的任务";
    empty.classList.add("show");
    return;
  }
  empty.classList.remove("show");
  body.innerHTML = list.map((t) => {
    const preset = state.presets[t.taskType] || {};
    const label = preset.label || t.taskType;
    const statusDot = t.status === "armed"
      ? '<i class="pulse"></i>'
      : t.status === "success"
        ? '<i class="dot ok"></i>'
        : t.status === "failed"
          ? '<i class="dot bad"></i>'
          : "";
    return `<tr data-id="${t.id}">
      <td class="col-enable"><button type="button" class="toggle ${t.enabled ? "on" : ""}" data-act="enable" aria-label="启用"><i></i></button></td>
      <td class="col-name" title="${escapeHtml(t.name || label)}">${escapeHtml(t.walletName || "—")}</td>
      ${addrCell(t.walletAddress)}
      <td class="col-type"><span class="badge ${escapeHtml(t.taskType)}">${escapeHtml(label)}</span></td>
      <td class="col-schedule">${scheduleLabel(t.schedule)}</td>
      <td class="mono col-fire">${
        t.schedule === "once"
          ? `<input type="text" class="fire-edit" value="${escapeHtml(fireAtInputValue(t.fireAt))}" placeholder="2026/11/05 20:00:00" title="仅一次可编辑下次执行时间" />`
          : (!t.enabled ? "—" : escapeHtml(fmtFireAt(t.fireAt)))
      }</td>
      <td class="col-status"><span class="status">${statusDot}${statusLabel(t.status)}</span></td>
      <td class="actions"><div class="actions-row">
        <button type="button" class="btn secondary sm btn-rel" data-act="logs">${
          (() => {
            const o = unreadRecordOutcome(t.id);
            return o ? `<i class="btn-dot ${o}"></i>` : "";
          })()
        }记录</button>
        <button type="button" class="btn secondary sm" data-act="run-once">执行一次</button>
        ${preset.needsLgnsApprove
          ? (() => {
              const as = state.allowByTask[t.id] || "unknown";
              const cls = as === "ok" ? " allow-ok" : as === "bad" ? " allow-bad" : "";
              const label = as === "ok" ? "已授权" : as === "bad" ? "未授权" : "授权检测";
              return `<button type="button" class="btn secondary sm${cls}" data-act="check-allow" data-allow-state="${as}">${label}</button>`;
            })()
          : ""}
        <button type="button" class="btn secondary sm" data-act="del-task">删除</button>
      </div></td>
    </tr>`;
  }).join("");
}

function logSeenKey(taskId) {
  return `anubisbot.logSeen.${taskId}`;
}

function isTaskRecordEvent(ev) {
  return ev === "broadcast" || ev === "result" || ev === "sent" || ev === "would_send";
}

function newestLogAt(taskId) {
  let newest = "";
  for (const l of state.logs || []) {
    if (l.taskId !== taskId || !l.at || !isTaskRecordEvent(l.event)) continue;
    if (!newest || new Date(l.at) > new Date(newest)) newest = l.at;
  }
  return newest;
}

function hasUnreadLogs(taskId) {
  return !!unreadRecordOutcome(taskId);
}

/** @returns {"ok"|"bad"|""} 未读成败：有成功→ok；仅失败→bad；无成败新消息→空 */
function unreadRecordOutcome(taskId) {
  const seen = localStorage.getItem(logSeenKey(taskId));
  let hasOk = false;
  let hasBad = false;
  for (const l of state.logs || []) {
    if (l.taskId !== taskId || !l.at) continue;
    if (seen && !(new Date(l.at) > new Date(seen))) continue;
    if (l.event === "result") {
      const ok = (l.extra || {}).ok !== false && !l.error;
      if (ok) hasOk = true;
      else hasBad = true;
    } else if (l.event === "failed" || l.event === "simulate_fail") {
      hasBad = true;
    }
  }
  if (hasOk) return "ok";
  if (hasBad) return "bad";
  return "";
}

function markLogsSeen(taskId, logs) {
  let newest = "";
  for (const l of logs || []) {
    if (!l.at) continue;
    if (!newest || new Date(l.at) > new Date(newest)) newest = l.at;
  }
  if (!newest) newest = newestLogAt(taskId);
  if (newest) localStorage.setItem(logSeenKey(taskId), newest);
}

function formatLGNSCommas(v) {
  const s = String(v ?? "").trim();
  if (!s) return "";
  const [intPart, frac] = s.split(".");
  const withComma = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return frac != null && frac !== "" ? `${withComma}.${frac}` : withComma;
}

function paintAllowBtn(btn, allowState, taskId) {
  if (!btn) return;
  const st = allowState === "ok" || allowState === "bad" ? allowState : "unknown";
  if (taskId) {
    if (st === "unknown") delete state.allowByTask[taskId];
    else state.allowByTask[taskId] = st;
  }
  btn.dataset.allowState = st;
  btn.classList.remove("allow-ok", "allow-bad");
  if (st === "ok") {
    btn.classList.add("allow-ok");
    btn.textContent = "已授权";
  } else if (st === "bad") {
    btn.classList.add("allow-bad");
    btn.textContent = "未授权";
  } else {
    btn.textContent = "授权检测";
  }
}

function formatTaskRecord(l) {
  const ex = l.extra || {};
  const amt = String(ex.amount || "").replace(/\s*·\s*[\d.,]+\s*LGNS/i, "").trim() || ex.amount || "";
  if (l.event === "broadcast" || l.event === "sent" || l.event === "would_send") {
    const rpc = l.rpcId || ex.rpcId || "";
    return {
      bad: false,
      ok: false,
      title: "广播",
      detail: [amt, rpc ? `RPC ${rpc}` : ""].filter(Boolean).join(" · "),
    };
  }
  if (l.event === "result") {
    const ok = ex.ok !== false && !l.error;
    let title = ok ? "成功" : "失败";
    if (ok) {
      if (ex.claimedLGNS) {
        title = `成功 => ${formatLGNSCommas(ex.claimedLGNS)} LGNS`;
      } else if (ex.claimedPending) {
        title = "成功 正在读取链上数据...";
      }
    }
    let detail = amt || "";
    if (ok && ex.blockNumber != null) {
      const idx = (ex.txIndex != null ? Number(ex.txIndex) + 1 : "—");
      const parts = [amt];
      if (ex.fireAtLabel && ex.hourBlock != null && ex.hourBlock !== "") {
        parts.push(`${ex.fireAtLabel} 第 ${ex.hourBlock} 块`);
      } else if (ex.hourBlock != null && ex.hourBlock !== "") {
        parts.push(`整点第 ${ex.hourBlock} 块`);
      }
      parts.push(`块内第 ${idx} 笔`, `高度 ${ex.blockNumber}`);
      detail = parts.filter(Boolean).join(" · ");
    } else if (!ok) {
      const err = l.error || "";
      // Prefer amount · LastError (already contains "tx reverted… · reason : …").
      // Avoid duplicating when message is already "claimAll · tx reverted…".
      if (err) {
        detail = [amt, err].filter(Boolean).join(" · ");
      } else if (l.message) {
        detail = String(l.message).replace(/^失败\s*·\s*/, "");
      } else {
        detail = amt || "失败";
      }
    }
    const includeMs = ex.includeMs != null && ex.includeMs !== "" ? ex.includeMs : null;
    // includeMs is rendered after the tx link; only fall back to elapsedMs in detail when no tx.
    if (includeMs == null && !l.txHash && ex.elapsedMs != null && ex.elapsedMs !== "") {
      detail = [detail, `${ex.elapsedMs}ms`].filter(Boolean).join(" · ");
    }
    return { bad: !ok, ok, title, detail, includeMs };
  }
  return { bad: !!l.error, ok: false, title: l.event, detail: l.message || "" };
}

function renderTaskLogRows(box, logs) {
  if (!logs.length) {
    box.innerHTML = `<div class="desc">暂无广播 / 成败记录</div>`;
    return;
  }
  box.innerHTML = logs.map((l) => {
    const row = formatTaskRecord(l);
    const tx = l.txHash
      ? (String(l.txHash).startsWith("sim-")
        ? `<span class="addr">${escapeHtml(l.txHash)}</span>`
        : `<a href="${state.explorer}/tx/${l.txHash}" target="_blank" rel="noreferrer">${escapeHtml(shortAddr(l.txHash))}</a>`)
      : "";
    const includeMs = row.includeMs != null && row.includeMs !== "" ? row.includeMs : null;
    let txLine = tx;
    if (tx && includeMs != null) {
      txLine = `${tx}<span> · 上链 ${escapeHtml(String(includeMs))}ms</span>`;
    } else if (!tx && includeMs != null) {
      txLine = `<span>上链 ${escapeHtml(String(includeMs))}ms</span>`;
    }
    const tone = row.bad ? "err" : row.ok ? "ok" : "";
    return `<div class="log-item ${tone}">
      <div class="t">${new Date(l.at).toLocaleString()}</div>
      <div>
        <strong>${escapeHtml(row.title)}</strong>
        ${row.detail ? `<div>${escapeHtml(row.detail)}</div>` : ""}
        ${txLine}
      </div>
    </div>`;
  }).join("");
}

function resultNeedsClaimPoll(logs) {
  return (logs || []).some((l) => {
    if (l.event !== "result") return false;
    const ex = l.extra || {};
    if (ex.ok === false || l.error) return false;
    if (ex.claimedLGNS) return false;
    return !!ex.claimedPending;
  });
}

async function openTaskLogs(taskId) {
  const t = state.tasks.find((x) => x.id === taskId);
  const dlg = $("#taskLogsDialog");
  const title = $("#taskLogsTitle");
  const box = $("#taskLogsList");
  if (!dlg || !box) return;
  const typeLabel = state.presets[t?.taskType]?.label || t?.taskType || "";
  const name = t?.name && t.name !== typeLabel ? t.name : "";
  if (title) title.textContent = name ? `任务记录 · ${typeLabel} · ${name}` : `任务记录 · ${typeLabel || taskId}`;
  box.innerHTML = `<div class="desc">加载中…</div>`;
  dlg.showModal();
  const gen = (state.taskLogsGen = (state.taskLogsGen || 0) + 1);
  const stillOpen = () => dlg.open && state.taskLogsGen === gen;

  const fetchAndRender = async () => {
    const data = await api(`/logs?taskId=${encodeURIComponent(taskId)}`);
    if (!stillOpen()) return null;
    const all = data.logs || [];
    const logs = all.filter((l) => isTaskRecordEvent(l.event));
    markLogsSeen(taskId, logs);
    state.sig.tasks = "";
    renderTasks(true);
    renderTaskLogRows(box, logs);
    return logs;
  };

  try {
    let logs = await fetchAndRender();
    if (!stillOpen() || !logs) return;
    // Poll while claimedPending until claimedLGNS appears (~15s).
    for (let i = 0; i < 8 && stillOpen(); i++) {
      if (!resultNeedsClaimPoll(logs)) break;
      await new Promise((r) => setTimeout(r, 2000));
      if (!stillOpen()) break;
      logs = await fetchAndRender();
      if (!logs) break;
    }
  } catch (err) {
    if (stillOpen()) box.innerHTML = `<div class="desc">${escapeHtml(err.message)}</div>`;
  }
}

function askDeleteTaskConfirm(name) {
  const dlg = $("#confirmDeleteTask");
  if (!dlg) return Promise.resolve(false);
  const msg = $("#confirmDeleteTaskMsg");
  if (msg) msg.textContent = `确定删除任务「${name || ""}」？此操作不可恢复。`;
  dlg.returnValue = "";
  dlg.showModal();
  return new Promise((resolve) => {
    dlg.addEventListener("close", () => resolve(dlg.returnValue === "ok"), { once: true });
  });
}

function sourceLabel(s) {
  return ({ privateKey: "私钥", mnemonic: "助记词", privateKeyEnv: "环境变量", watch: "只读" })[s] || s || "—";
}

function openAddrFromClick(e, addr) {
  if (!addr) return;
  if (e.ctrlKey || e.metaKey) {
    window.open(`/address.html?address=${encodeURIComponent(addr)}`, "_blank");
    return;
  }
  if (typeof window.openAddressDetail === "function") window.openAddressDetail(addr);
}

function renderWallets(force = false) {
  const body = $("#walletBody");
  const empty = $("#walletEmpty");
  const sig = fingerprint({ w: state.wallets, b: state.balances });
  if (!force && sig === state.sig.wallets) return;
  state.sig.wallets = sig;
  if (!state.wallets.length) {
    body.innerHTML = "";
    empty.classList.add("show");
    return;
  }
  empty.classList.remove("show");
  body.innerHTML = state.wallets.map((w) => {
    const bal = state.balances[w.id] || {};
    return `<tr data-id="${w.id}">
      <td class="wallet-name-cell"><span class="wallet-name-text">${escapeHtml(w.name)}</span><button type="button" class="addr-copy-btn" data-act="edit-wallet-name" title="改名" aria-label="改名">${EDIT_ICON_SVG}</button></td>
      ${addrCell(w.address)}
      <td>${escapeHtml(sourceLabel(w.source))}</td>
      <td class="mono">${escapeHtml(bal.dai ?? "—")}</td>
      <td class="mono">${escapeHtml(bal.lgns ?? "—")}</td>
      <td class="mono">${escapeHtml(bal.slgns ?? "—")}</td>
      <td class="mono">${escapeHtml(bal.ailgns ?? "—")}</td>
      <td class="actions"><div class="actions-row">
        <button type="button" class="btn secondary sm" data-act="del-wallet">删除</button>
      </div></td>
    </tr>`;
  }).join("");
}

function renderPresetManageList() {
  const box = $("#presetManageList");
  if (!box) return;
  const entries = Object.entries(state.presets || {});
  if (!entries.length) {
    box.innerHTML = `<div class="desc">暂无任务类型</div>`;
    return;
  }
  box.innerHTML = entries.map(([id, p]) => {
    const active = id === state.selectedPresetId ? "active" : "";
    return `<div class="task-manage-row ${active}" data-id="${escapeHtml(id)}">
      <div class="task-manage-meta">
        <strong>${escapeHtml(p.label || id)}</strong>
        <span class="badge ${escapeHtml(id)}">${escapeHtml(id)}</span>
        <span class="addr">${escapeHtml(scheduleLabel(p.defaultSchedule))}</span>
        <span class="addr mono">${escapeHtml(shortAddr(p.to))}</span>
      </div>
      <div class="task-manage-actions">
        <button type="button" class="btn secondary sm" data-act="edit-preset">编辑</button>
        <button type="button" class="btn secondary sm" data-act="del-preset">删除</button>
      </div>
    </div>`;
  }).join("");
}

function formatAbiText(abi) {
  if (!abi) return "";
  try {
    return JSON.stringify(typeof abi === "string" ? JSON.parse(abi) : abi, null, 2);
  } catch {
    return String(abi);
  }
}

function showPresetEditor(id) {
  const form = $("#presetForm");
  const dlg = $("#presetDialog");
  const title = $("#presetDialogTitle");
  if (!form || !dlg) return;
  form.reset();
  const idInput = $("#presetIdInput");
  if (id && state.presets[id]) {
    state.selectedPresetId = id;
    const p = state.presets[id];
    if (title) title.textContent = "编辑任务类型";
    idInput.value = id;
    idInput.readOnly = true;
    form.label.value = p.label || "";
    form.description.value = p.description || "";
    form.to.value = p.to || "";
    form.abi.value = formatAbiText(p.abi);
    form.function.value = presetFnName(p);
    form.defaultSchedule.value = p.defaultSchedule || "hourly";
    form.gasLimit.value = p.defaultParams?.gasLimit ?? "";
    form.needsLgnsApprove.checked = !!p.needsLgnsApprove;
  } else {
    state.selectedPresetId = null;
    if (title) title.textContent = "新建任务类型";
    idInput.value = "";
    idInput.readOnly = false;
    form.defaultSchedule.value = "hourly";
    form.abi.value = "";
    form.function.value = "";
  }
  updatePresetAbiUI();
  renderPresetManageList();
  if (!dlg.open) dlg.showModal();
}

function clearPresetEditor() {
  state.selectedPresetId = null;
  const form = $("#presetForm");
  const dlg = $("#presetDialog");
  if (form) {
    form.reset();
    const idInput = $("#presetIdInput");
    if (idInput) idInput.readOnly = false;
  }
  if (dlg?.open) dlg.close();
  renderPresetManageList();
}

function renderLogs(force = false) {
  const box = $("#logList");
  if (!box) return;
  const sig = fingerprint(state.logs);
  if (!force && sig === state.sig.logs) return;
  state.sig.logs = sig;
  if (!state.logs.length) {
    box.innerHTML = `<div class="empty show">暂无日志</div>`;
    return;
  }
  box.innerHTML = state.logs.map((l) => {
    const bad = !!(l.error || l.event === "failed" || l.event === "simulate_fail" || (l.event === "result" && l.extra?.ok === false));
    if (isTaskRecordEvent(l.event)) {
      const row = formatTaskRecord(l);
      const tx = l.txHash && !String(l.txHash).startsWith("sim-")
        ? `<a href="${state.explorer}/tx/${l.txHash}" target="_blank" rel="noreferrer">${escapeHtml(shortAddr(l.txHash))}</a>`
        : "";
      const tone = row.bad || bad ? "err" : row.ok ? "ok" : "";
      return `<div class="log-item ${tone}">
        <div class="t">${new Date(l.at).toLocaleString()}</div>
        <div>
          <strong>${escapeHtml(row.title)}</strong>
          ${row.detail ? `<div class="log-msg">${escapeHtml(row.detail)}</div>` : ""}
          ${tx}
          ${l.wallet || l.rpcId ? `<span class="log-meta">${escapeHtml([l.wallet, l.rpcId ? `rpc=${l.rpcId}` : ""].filter(Boolean).join(" · "))}</span>` : ""}
        </div>
      </div>`;
    }
    const meta = [l.taskId, l.wallet].filter(Boolean).map(escapeHtml).join(" · ");
    const tx = l.txHash && !String(l.txHash).startsWith("sim-")
      ? `<a href="${state.explorer}/tx/${l.txHash}" target="_blank" rel="noreferrer">${escapeHtml(shortAddr(l.txHash))}</a>`
      : "";
    return `<div class="log-item ${bad ? "err" : ""}">
      <div class="t">${new Date(l.at).toLocaleString()}</div>
      <div>
        <strong>${escapeHtml(l.event)}</strong>${meta ? ` · ${meta}` : ""}
        <div class="log-msg">${escapeHtml(l.message || l.error || "")}</div>
        ${tx}
        ${l.rpcId ? `<span class="log-meta">rpc=${escapeHtml(l.rpcId)} rtt=${l.rttMs || 0}ms</span>` : ""}
      </div>
    </div>`;
  }).join("");
}

function isTaskDialogOpen() {
  return !!$("#taskDialog")?.open;
}

function fillTypeSelect(opts = {}) {
  const sel = $("#typeSelect");
  if (!sel) return;
  const prev = sel.value;
  sel.innerHTML = Object.entries(state.presets).map(([k, p]) =>
    `<option value="${k}">${escapeHtml(p.label)}</option>`
  ).join("");
  if (prev && [...sel.options].some((o) => o.value === prev)) sel.value = prev;
  // Never clobber an open new/edit form via poll rebuild.
  if (!opts.skipDesc && !isTaskDialogOpen()) updateTypeDesc();
}

function fillWalletSelect() {
  $("#walletSelect").innerHTML = state.wallets.map((w) =>
    `<option value="${w.id}">${escapeHtml(w.name)} (${shortAddr(w.address)})</option>`
  ).join("") || `<option value="">请先添加钱包</option>`;
}

function fillSimTypeSelect() {
  const sel = $("#simTypeSelect");
  if (!sel) return;
  const prev = sel.value;
  const entries = Object.entries(state.presets || {});
  sel.innerHTML = entries.map(([k, p]) =>
    `<option value="${escapeHtml(k)}">${escapeHtml(p.label || k)}</option>`
  ).join("") || `<option value="">暂无任务类型</option>`;
  if (prev && [...sel.options].some((o) => o.value === prev)) sel.value = prev;
  updateSimTypeDesc();
}

function updateSimTypeDesc() {
  const el = $("#simTypeDesc");
  if (!el) return;
  const id = $("#simTypeSelect")?.value;
  const p = state.presets[id];
  if (!p) {
    el.textContent = "";
    return;
  }
  const sch = p.defaultSchedule ? `定时：${scheduleLabel(p.defaultSchedule)}` : "";
  el.textContent = [p.description || "", sch].filter(Boolean).join(" · ");
}

function fillSimWalletSelect() {
  const sel = $("#simWalletSelect");
  if (!sel) return;
  const prev = sel.value;
  const opts = [
    `<option value="">手填地址</option>`,
    ...state.wallets.map((w) =>
      `<option value="${escapeHtml(w.id)}" data-addr="${escapeHtml(w.address)}">${escapeHtml(w.name)} (${shortAddr(w.address)})</option>`
    ),
  ];
  sel.innerHTML = opts.join("");
  if (prev && [...sel.options].some((o) => o.value === prev)) sel.value = prev;
}

function syncSimFromAddress() {
  const input = $("#simFromInput");
  const walletSel = $("#simWalletSelect");
  if (!input) return;
  const wid = walletSel?.value;
  if (wid) {
    const w = state.wallets.find((x) => x.id === wid);
    if (w?.address) input.value = w.address;
  }
}

function explainSimResult(res) {
  if (!res || typeof res !== "object") return "";
  if (res.ok) return "结果：模拟成功（eth_call 未回滚，调用路径可通）";
  let raw = String(res.error || "未知错误");
  // 去掉后面 ABI hex，只留可读句
  raw = raw.split(/:\s*0x[0-9a-fA-F]{8,}/)[0].trim();
  raw = raw.replace(/^execution reverted:\s*/i, "").trim() || raw;
  const rules = [
    [/stake below minimum/i, "质押低于最低要求"],
    [/insufficient (allowance|approval)/i, "授权额度不足"],
    [/insufficient (balance|funds)/i, "余额不足"],
    [/transfer amount exceeds allowance/i, "超过授权额度"],
    [/transfer amount exceeds balance/i, "余额不足"],
    [/not (yet )?open|too early|not started/i, "尚未开放"],
    [/sold out|no (remaining )?quota|exceed/i, "额度已满或超限"],
  ];
  let zh = "";
  for (const [re, text] of rules) {
    if (re.test(raw)) {
      zh = text;
      break;
    }
  }
  return zh
    ? `结果：模拟失败 — ${zh}（${raw}）`
    : `结果：模拟失败 — ${raw}`;
}

function fillTaskSelects(preferId) {
  if (preferId) state.selectedTaskId = preferId;
  fillSimTypeSelect();
  fillSimWalletSelect();
  renderPresetManageList();
}

function updateTypeDesc() {
  const form = $("#taskForm");
  const editing = !!(state.editingTaskId || form?.taskId?.value);
  const gweiEl = form?.querySelector('[name="gasPriceGwei"]');
  const keepGwei = editing && gweiEl ? gweiEl.value : null;
  const t = $("#typeSelect").value;
  const p = state.presets[t];
  $("#typeDesc").textContent = p?.description || "";
  const schHint = $("#typeScheduleHint");
  if (schHint) {
    schHint.textContent = p?.defaultSchedule
      ? `定时：${scheduleLabel(p.defaultSchedule)}（随任务类型，不可单独改）`
      : "定时：随任务类型";
  }
  const gas = document.querySelector('#taskForm [name="gasLimit"]');
  if (p?.defaultParams?.gasLimit) gas.value = p.defaultParams.gasLimit;
  if (keepGwei != null && gweiEl) gweiEl.value = keepGwei;
  const box = $("#taskArgsFields");
  const resolved = resolveAbiFn(JSON.stringify(p?.abi || []), presetFnName(p));
  if (!resolved.fn) {
    if (box) box.innerHTML = `<p class="desc">${escapeHtml(resolved.error || "无法解析 ABI")}</p>`;
    return;
  }
  renderArgFields(box, resolved.fn, abiArgDefaults(resolved.fn, p), "taskArg");
}

function resetTaskDialogMode() {
  const form = $("#taskForm");
  if (!form) return;
  state.editingTaskId = null;
  form.taskId.value = "";
  form.walletIdMirror.value = "";
  const title = $("#taskFormTitle");
  if (title) title.textContent = "新建任务";
  const sel = $("#walletSelect");
  if (sel) {
    sel.disabled = false;
    sel.required = true;
  }
}

function taskNameForForm(task) {
  const raw = (task?.name || "").trim();
  if (!raw) return "";
  const label = (state.presets[task.taskType]?.label || "").trim();
  if (label && raw === label) return "";
  return raw;
}

function openNewTaskDialog() {
  const form = $("#taskForm");
  form.reset();
  resetTaskDialogMode();
  fillTypeSelect();
  fillWalletSelect();
  form.name.value = "";
  $("#taskDialog").showModal();
}

function openEditTaskDialog(task) {
  if (!task) return;
  const form = $("#taskForm");
  form.reset();
  fillTypeSelect({ skipDesc: true });
  fillWalletSelect();
  state.editingTaskId = task.id;
  form.taskId.value = task.id;
  form.walletIdMirror.value = task.walletId || "";
  form.name.value = taskNameForForm(task);
  const sel = $("#walletSelect");
  if (sel) {
    sel.value = task.walletId || "";
    sel.disabled = true;
    sel.required = false;
  }
  form.taskType.value = task.taskType || "";
  updateTypeDesc();
  const p = task.params || {};
  if (p.gasLimit) form.gasLimit.value = p.gasLimit;
  form.gasPriceGwei.value = p.gasPriceGwei != null && p.gasPriceGwei !== "" ? p.gasPriceGwei : "";
  const preset = state.presets[task.taskType];
  const box = $("#taskArgsFields");
  const resolved = resolveAbiFn(JSON.stringify(preset?.abi || []), presetFnName(preset));
  if (resolved.fn && box) {
    let vals = abiArgDefaults(resolved.fn, preset);
    if (Array.isArray(p.args) && p.args.length) {
      vals = p.args.map(String);
    } else if (p.burnAmount && resolved.fn.inputs?.length) {
      vals = resolved.fn.inputs.map((_, i) => (i === 0 ? String(p.burnAmount) : ""));
    }
    renderArgFields(box, resolved.fn, vals, "taskArg");
  }
  const title = $("#taskFormTitle");
  if (title) title.textContent = "编辑任务";
  $("#taskDialog").showModal();
}

function buildTaskParamsFromForm(fd, taskType) {
  const params = {
    gasLimit: Number(fd.get("gasLimit") || 0) || undefined,
  };
  const gwei = fd.get("gasPriceGwei");
  if (gwei !== "" && gwei != null) params.gasPriceGwei = Number(gwei);
  const p = state.presets[taskType];
  const resolved = resolveAbiFn(JSON.stringify(p?.abi || []), presetFnName(p));
  const n = resolved.fn?.inputs?.length || 0;
  if (n > 0) {
    params.args = collectArgFields($("#taskForm"), "taskArg", n);
  }
  return params;
}

function openSettings(tab = "general") {
  fillTaskSelects();
  $$("#settingsTabs .tab").forEach((b) => b.classList.toggle("active", b.dataset.tab === tab));
  $$(".tab-panel").forEach((p) => p.classList.toggle("active", p.id === "tab-" + tab));
  $("#settingsModeHint").textContent = (state.health?.mode || "sim").toUpperCase();
  api("/settings").then((st) => {
    const f = $("#settingsForm");
    f.signLeadMs.value = st.signLeadMs;
    f.earlyBroadcast.checked = st.earlyBroadcast !== false;
    f.rpcTimeoutMs.value = st.rpcTimeoutMs || 12000;
    f.batchConcurrency.value = st.batchConcurrency;
    f.gasMultiplier.value = st.gasMultiplier;
    f.ntpHost.value = st.ntpHost || "pool.ntp.org";
    f.ntpIntervalSec.value = st.ntpIntervalSec || 30;
  }).catch((e) => toast(e.message, true));
  if (tab === "params") renderPresetManageList();
  $("#settingsDialog").showModal();
}

async function refreshHealth() {
  const health = await api("/health");
  // Full replace calibrates nextHour/next1159; tickCountdowns owns the chip text.
  state.health = health;
  state.explorer = health.explorer || state.explorer;
  state.activeIds = health.activeIds || [];
  state.rtts = health.rpcsRtt || {};
  state.leads = health.leads || {};
  renderHealthChips();
  tickCountdowns();
}

/** Light log poll so record dots update without waiting for full refreshLists. */
async function refreshLogsLight() {
  const failedOnly = $("#failedOnly")?.checked;
  const logs = await api("/logs?failedOnly=" + (failedOnly ? "1" : "0"));
  state.logs = logs.logs || [];
  renderLogs();
  renderTasks();
}

async function refreshLists() {
  const failedOnly = $("#failedOnly").checked;
  // 余额由服务启动时缓存，打开网页不再打链上 RPC
  const [rpc, presets, tasks, wallets, logs] = await Promise.all([
    api("/rpc"),
    api("/presets"),
    api("/tasks"),
    api("/wallets"),
    api("/logs?failedOnly=" + (failedOnly ? "1" : "0")),
  ]);
  state.rpcList = rpc.rpcs;
  state.activeIds = rpc.activeIds || state.activeIds;
  state.rtts = rpc.rtts || state.rtts;
  state.leads = rpc.leads || state.leads;
  state.presets = presets;
  state.tasks = tasks.tasks || [];
  state.wallets = wallets.wallets || [];
  state.balances = wallets.balances || {};
  state.logs = logs.logs || [];
  renderRpcMulti(true);
  renderTasks();
  renderWallets();
  renderLogs();
  // Skip while task dialog is open — rebuild would reset type to presets[0]
  // and look like "jumping back" to the first task.
  if (!isTaskDialogOpen()) fillTypeSelect();
  fillSimTypeSelect();
  fillSimWalletSelect();
  renderPresetManageList();
}

async function setActiveRpcs(ids) {
  if (!ids.length) {
    toast("至少选择一个 RPC", true);
    renderRpcMulti(true);
    return;
  }
  await api("/rpc/active", { method: "PUT", body: JSON.stringify({ ids }) });
  state.activeIds = ids;
  await refreshHealth();
  renderRpcMulti(false);
}

async function refreshAll() {
  await refreshHealth();
  await refreshLists();
}

function tickCountdowns() {
  const h = state.health;
  if (!h) return;
  // Frontend self-drives: roll past targets locally; health poll only calibrates.
  const hour = rollCountdownTarget(h.nextHour, 3600 * 1000);
  if (hour && hour !== h.nextHour) h.nextHour = hour;
  const helix = rollCountdownTarget(h.next1159, 24 * 3600 * 1000);
  if (helix && helix !== h.next1159) h.next1159 = helix;
  setTextQuiet($("#cdHour"), fmtCountdown(h.nextHour));
  setTextQuiet($("#cdHelix"), fmtCountdown(h.next1159));
}

function bindDialogBackdrop(_dlg) {
  // Intentionally empty: clicking the backdrop must not close dialogs.
}

function askApproveConfirm(spender) {
  const dlg = $("#confirmApprove");
  if (!dlg) return Promise.resolve(null);
  const msg = $("#confirmApproveMsg");
  const sp = $("#confirmApproveSpender");
  const input = $("#approveAmountInput");
  if (msg) msg.textContent = "当前 LGNS 授权不足。确认后将向合约发送 approve。";
  if (sp) sp.textContent = spender || "";
  if (input) input.value = "无限";
  dlg.returnValue = "";
  dlg.showModal();
  return new Promise((resolve) => {
    dlg.addEventListener("close", () => {
      if (dlg.returnValue !== "ok") {
        resolve(null);
        return;
      }
      resolve((input?.value || "无限").trim() || "无限");
    }, { once: true });
  });
}

function bind() {
  $$(".nav-item").forEach((btn) => btn.addEventListener("click", () => {
    $$(".nav-item").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    $$(".view").forEach((v) => v.classList.remove("active"));
    const view = $("#view-" + btn.dataset.view);
    view.classList.add("active");
    view.classList.remove("fade-once");
    void view.offsetWidth;
    view.classList.add("fade-once");
  }));

  $("#modeToggle").addEventListener("change", async (e) => {
    if (e.target.checked) {
      const dlg = $("#confirmLive");
      dlg.showModal();
      const res = await new Promise((resolve) => {
        dlg.addEventListener("close", () => resolve(dlg.returnValue), { once: true });
      });
      if (res !== "ok") {
        e.target.checked = false;
        return;
      }
      try {
        await api("/mode", { method: "PUT", body: JSON.stringify({ mode: "live", confirm: true }) });
        toast("已切换到 LIVE（独立钱包/任务）");
      } catch (err) {
        e.target.checked = false;
        toast(err.message, true);
      }
    } else {
      try {
        await api("/mode", { method: "PUT", body: JSON.stringify({ mode: "sim" }) });
        toast("已切换到 SIM（独立钱包/任务）");
      } catch (err) {
        toast(err.message, true);
      }
    }
    state.sig.tasks = "";
    state.sig.wallets = "";
    await refreshHealth();
    await refreshLists();
    fillWalletSelect();
    fillTaskSelects();
  });

  const rpcBtn = $("#rpcMultiBtn");
  const rpcPanel = $("#rpcMultiPanel");
  rpcBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = !rpcPanel.hidden;
    rpcPanel.hidden = open;
    rpcBtn.setAttribute("aria-expanded", open ? "false" : "true");
  });
  document.addEventListener("click", (e) => {
    if (!$("#rpcMulti").contains(e.target)) {
      rpcPanel.hidden = true;
      rpcBtn.setAttribute("aria-expanded", "false");
    }
  });
  rpcPanel.addEventListener("click", (e) => e.stopPropagation());
  rpcPanel.addEventListener("change", async (e) => {
    const cb = e.target.closest("input[type=checkbox]");
    if (!cb) return;
    const ids = $$(".rpc-row input:checked", rpcPanel).map((el) => el.closest(".rpc-row").dataset.id);
    try {
      await setActiveRpcs(ids);
      toast(ids.length > 1 ? `已选 ${ids.length} 个 RPC` : "已更新 RPC");
    } catch (err) {
      toast(err.message, true);
      await refreshLists();
    }
  });

  const rpcDlg = $("#rpcDialog");
  const rpcEditDlg = $("#rpcEditDialog");
  let rpcDraft = null;
  let rpcDraftBase = null;
  let rpcEditId = null;
  bindDialogBackdrop(rpcEditDlg);

  function cloneRpcList(list) {
    return (list || []).map((r) => ({
      id: r.id,
      name: r.name || "",
      url: r.url || "",
      builtin: !!r.builtin,
    }));
  }

  function isRpcDraftDirty() {
    if (!rpcDraft || !rpcDraftBase) return false;
    return JSON.stringify(stripProbeFields(rpcDraft)) !== JSON.stringify(stripProbeFields(rpcDraftBase));
  }

  function stripProbeFields(list) {
    return (list || []).map((r) => ({
      id: r.id,
      name: r.name || "",
      url: r.url || "",
      builtin: !!r.builtin,
    }));
  }

  function openRpcManage() {
    rpcDraft = cloneRpcList(state.rpcList);
    rpcDraftBase = cloneRpcList(state.rpcList);
    renderRpcManageList();
    rpcDlg.showModal();
  }

  function tryCloseRpcManage() {
    if (isRpcDraftDirty() && !confirm("有未保存的修改，确定关闭？")) return;
    rpcDraft = null;
    rpcDraftBase = null;
    rpcDlg.close();
  }

  function openRpcEditDialog(rpc) {
    const form = $("#rpcForm");
    form?.reset();
    const hint = $("#rpcProbeHint");
    if (hint) { hint.hidden = true; hint.textContent = ""; }
    const idInput = $("#rpcIdInput");
    if (rpc) {
      rpcEditId = rpc.id;
      form.id.value = rpc.id;
      form.name.value = rpc.name || "";
      form.url.value = rpc.url || "";
      if (idInput) idInput.readOnly = true;
      $("#rpcFormTitle").textContent = "编辑 RPC";
    } else {
      rpcEditId = null;
      if (idInput) idInput.readOnly = false;
      $("#rpcFormTitle").textContent = "新增 RPC";
    }
    rpcEditDlg.showModal();
  }

  function closeRpcEditDialog() {
    rpcEditId = null;
    $("#rpcForm")?.reset();
    rpcEditDlg.close();
  }

  function applyOffsetMs(off) {
    if (off == null || Number.isNaN(Number(off))) return;
    const n = Number(off);
    if (!state.health) state.health = {};
    state.health.offsetMs = n;
    const el = $("#offsetText");
    if (el) el.textContent = `offset ${n >= 0 ? "+" : ""}${n}ms`;
  }

  async function probeRpcUrl(url, btn) {
    const u = String(url || "").trim();
    if (!u) {
      toast("请先填写 URL", true);
      return null;
    }
    const prev = btn ? btn.textContent : "";
    if (btn) {
      btn.disabled = true;
      btn.textContent = "测试中…";
    }
    try {
      const res = await api("/rpc/probe", {
        method: "POST",
        body: JSON.stringify({ url: u }),
      });
      if (!res.ok) {
        toast(res.error || "连通失败", true);
        return res;
      }
      applyOffsetMs(res.offsetMs);
      toast(`连通 · ${res.rttMs} ms`);
      return res;
    } catch (err) {
      toast(err.message, true);
      return null;
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.textContent = prev || "测试";
      }
    }
  }

  function renderRpcManageList() {
    const box = $("#rpcManageList");
    if (!box) return;
    const list = rpcDraft || [];
    box.innerHTML = list.map((r) => {
      // 管理弹窗只展示本行手动「测试」结果；未测则不显示 RTT
      const rttVal = r._probeRtt > 0 ? r._probeRtt : 0;
      const rttHtml = rttVal > 0
        ? ` · <span class="rpc-rtt ok">${rttVal} ms</span>`
        : "";
      const tag = r.builtin ? `<span class="rpc-tag">内置</span>` : "";
      return `<div class="rpc-manage-row" data-id="${escapeHtml(r.id)}">
        <div class="rpc-manage-meta">
          <div class="rpc-manage-title">${escapeHtml(r.name || r.id)} ${tag}</div>
          <div class="mono addr">${escapeHtml(r.id)}${rttHtml}</div>
          <div class="mono addr" title="${escapeHtml(r.url || "")}">${escapeHtml(r.url || "")}</div>
        </div>
        <div class="actions">
          <button type="button" class="btn secondary sm" data-act="probe-rpc">测试</button>
          <button type="button" class="btn secondary sm" data-act="edit-rpc">编辑</button>
          <button type="button" class="btn secondary sm rpc-del-btn" data-act="del-rpc">删除</button>
        </div>
      </div>`;
    }).join("") || `<div class="desc">暂无 RPC</div>`;
  }

  $("#btnRpcManage").addEventListener("click", () => openRpcManage());
  $("#btnCloseRpc").addEventListener("click", () => tryCloseRpcManage());
  rpcDlg.addEventListener("cancel", (e) => {
    e.preventDefault();
    tryCloseRpcManage();
  });
  rpcDlg.addEventListener("click", (e) => {
    if (e.target === rpcDlg) tryCloseRpcManage();
  });
  $("#btnOpenAddRpc").addEventListener("click", () => openRpcEditDialog(null));
  $("#btnCancelRpcEdit").addEventListener("click", () => closeRpcEditDialog());

  $("#btnProbeRpcUrl").addEventListener("click", async () => {
    const form = $("#rpcForm");
    const url = form?.url?.value || "";
    const res = await probeRpcUrl(url, $("#btnProbeRpcUrl"));
    const hint = $("#rpcProbeHint");
    if (!hint) return;
    if (!res) {
      hint.hidden = true;
      return;
    }
    hint.hidden = false;
    hint.textContent = res.ok ? `连通 · ${res.rttMs} ms` : (res.error || "连通失败");
    hint.classList.toggle("err", !res.ok);
  });

  $("#rpcManageList")?.addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-act]");
    if (!btn || !rpcDraft) return;
    const row = btn.closest(".rpc-manage-row");
    const id = row?.dataset.id;
    if (!id) return;
    const rpc = rpcDraft.find((x) => x.id === id);
    if (!rpc) return;
    if (btn.dataset.act === "probe-rpc") {
      e.stopPropagation();
      const res = await probeRpcUrl(rpc.url, btn);
      if (res?.ok) {
        rpc._probeRtt = res.rttMs;
        // 只更新本行展示，不改全局 rtts，避免看起来像测了全部节点
        renderRpcManageList();
      } else if (res && res.ok === false) {
        rpc._probeRtt = 0;
        renderRpcManageList();
      }
      return;
    }
    if (btn.dataset.act === "edit-rpc") {
      openRpcEditDialog(rpc);
      return;
    }
    if (btn.dataset.act === "del-rpc") {
      if (!confirm(`确定删除 RPC「${rpc.name || id}」？`)) return;
      rpcDraft = rpcDraft.filter((x) => x.id !== id);
      if (rpcEditId === id) closeRpcEditDialog();
      renderRpcManageList();
    }
  });

  function suggestDraftRpcId(url, name) {
    const fromName = String(name || "").trim().replace(/[^a-zA-Z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
    if (fromName) return fromName;
    try {
      let u = String(url || "").trim();
      if (!u.includes("://")) u = "https://" + u;
      const host = new URL(u).hostname.replace(/^www\./, "");
      const id = host.replace(/[^a-zA-Z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
      return id || "rpc";
    } catch {
      return "rpc";
    }
  }

  function uniqueDraftRpcId(base) {
    let id = base || "rpc";
    const used = new Set((rpcDraft || []).map((x) => x.id));
    if (!used.has(id)) return id;
    for (let i = 2; i < 1000; i++) {
      const cand = `${base}-${i}`;
      if (!used.has(cand)) return cand;
    }
    return `${base}-${Date.now().toString(36)}`;
  }

  $("#btnSaveRpc").addEventListener("click", () => {
    if (!rpcDraft) return;
    const form = $("#rpcForm");
    const fd = new FormData(form);
    const id = String(fd.get("id") || "").trim();
    const name = String(fd.get("name") || "").trim();
    const url = String(fd.get("url") || "").trim();
    if (!url) return toast("URL 必填", true);
    if (rpcEditId) {
      const row = rpcDraft.find((x) => x.id === rpcEditId);
      if (!row) return toast("节点不存在", true);
      row.name = name || row.id;
      if (row.url !== url) delete row._probeRtt;
      row.url = url;
    } else {
      if (id && rpcDraft.some((x) => x.id === id)) {
        return toast("ID 已存在", true);
      }
      const newId = id || uniqueDraftRpcId(suggestDraftRpcId(url, name));
      rpcDraft.push({
        id: newId,
        name: name || newId,
        url,
        builtin: false,
      });
    }
    closeRpcEditDialog();
    renderRpcManageList();
  });

  $("#btnSaveRpcManage").addEventListener("click", async () => {
    if (!rpcDraft) return;
    if (!isRpcDraftDirty()) return toast("无变更");
    if (rpcDraft.length === 0) return toast("至少保留一个 RPC", true);
    const payload = stripProbeFields(rpcDraft).map((r) => ({
      id: r.id,
      name: r.name,
      url: r.url,
    }));
    try {
      const res = await api("/rpc", {
        method: "PUT",
        body: JSON.stringify({ rpcs: payload }),
      });
      toast("已保存");
      state.rpcList = res.rpcs || [];
      await refreshLists();
      await refreshHealth();
      rpcDraft = cloneRpcList(state.rpcList);
      rpcDraftBase = cloneRpcList(state.rpcList);
      renderRpcManageList();
    } catch (err) {
      toast(err.message, true);
    }
  });

  const taskDlg = $("#taskDialog");
  bindDialogBackdrop(taskDlg);
  taskDlg.addEventListener("close", () => resetTaskDialogMode());
  $("#taskFilter").addEventListener("input", (e) => {
    state.taskFilter = e.target.value || "";
    renderTasks(true);
  });

  $("#btnNewTask").addEventListener("click", () => openNewTaskDialog());
  $("#btnCancelTask").addEventListener("click", () => {
    taskDlg.close();
  });
  $("#typeSelect").addEventListener("change", updateTypeDesc);

  $("#taskForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    const taskId = (state.editingTaskId || fd.get("taskId") || "").toString().trim();
    const taskType = fd.get("taskType");
    const params = buildTaskParamsFromForm(fd, taskType);
    const walletId = taskId
      ? (fd.get("walletIdMirror") || state.tasks.find((t) => t.id === taskId)?.walletId || "").toString()
      : (fd.get("walletId") || "").toString();
    const dup = state.tasks.find(
      (t) => t.walletId === walletId && t.taskType === taskType && t.id !== taskId
    );
    if (dup) {
      const typeLabel = state.presets[taskType]?.label || taskType;
      toast(
        taskId
          ? `该钱包已有相同任务类型「${typeLabel}」`
          : `该钱包已有相同任务类型「${typeLabel}」，请勿重复创建`,
        true
      );
      return;
    }
    const name = (fd.get("name") || "").toString().trim();
    try {
      if (taskId) {
        await api(`/tasks/${taskId}`, {
          method: "PATCH",
          body: JSON.stringify({
            name,
            taskType,
            params,
          }),
        });
        toast("任务已更新");
      } else {
        await api("/tasks", {
          method: "POST",
          body: JSON.stringify({
            name: name || undefined,
            walletId,
            taskType,
            enabled: true,
            params,
          }),
        });
        toast("任务已创建并排队下次执行");
      }
      taskDlg.close();
      await refreshLists();
    } catch (err) {
      toast(err.message, true);
    }
  });

  $("#taskBody").addEventListener("dblclick", (e) => {
    if (e.target.closest(".toggle, [data-act], input.fire-edit, .addr-link-btn, .addr-copy-btn")) {
      return;
    }
    const tr = e.target.closest("tr[data-id]");
    if (!tr) return;
    const task = state.tasks.find((t) => t.id === tr.dataset.id);
    if (!task) return;
    openEditTaskDialog(task);
  });

  $("#taskBody").addEventListener("change", async (e) => {
    const input = e.target.closest("input.fire-edit");
    if (!input) return;
    const tr = input.closest("tr");
    const id = tr?.dataset.id;
    if (!id || !input.value.trim()) return;
    const d = parseFireAtText(input.value);
    if (!d) {
      toast("时间格式无效，例：2026/11/5 20:00:00", true);
      return;
    }
    try {
      const updated = await api(`/tasks/${id}`, {
        method: "PATCH",
        body: JSON.stringify({ fireAt: d.toISOString() }),
      });
      const i = state.tasks.findIndex((x) => x.id === id);
      if (i >= 0) state.tasks[i] = { ...state.tasks[i], ...updated };
      state.sig.tasks = "";
      renderTasks(true);
      toast("下次执行时间已更新");
    } catch (err) {
      toast(err.message, true);
      await refreshLists();
    }
  });

  $("#taskBody").addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-act]");
    if (!btn) return;
    if (btn.dataset.act === "copy-addr") {
      e.stopPropagation();
      await copyAddrFromBtn(btn);
      return;
    }
    if (btn.dataset.act === "open-addr") {
      openAddrFromClick(e, btn.dataset.addr);
      return;
    }
    const tr = btn.closest("tr");
    const id = tr?.dataset.id;
    if (!id) return;
    try {
      if (btn.dataset.act === "enable") {
        const on = !btn.classList.contains("on");
        btn.classList.toggle("on", on);
        try {
          const updated = await api(`/tasks/${id}/enable`, { method: "POST", body: JSON.stringify({ enabled: on }) });
          const i = state.tasks.findIndex((x) => x.id === id);
          if (i >= 0) state.tasks[i] = { ...state.tasks[i], ...updated };
          setTimeout(() => {
            state.sig.tasks = "";
            renderTasks(true);
          }, 420);
        } catch (err) {
          btn.classList.toggle("on", !on);
          throw err;
        }
        return;
      } else if (btn.dataset.act === "logs") {
        await openTaskLogs(id);
        return;
      } else if (btn.dataset.act === "run-once") {
        await api(`/tasks/${id}/run-once`, { method: "POST", body: "{}" });
        toast("已立即执行");
        await refreshLists();
        return;
      } else if (btn.dataset.act === "del-task") {
        const t = state.tasks.find((x) => x.id === id);
        const name = t?.name || state.presets[t?.taskType]?.label || id;
        if (!(await askDeleteTaskConfirm(name))) return;
        await api(`/tasks/${id}`, { method: "DELETE" });
        toast("任务已删除");
        await refreshLists();
        return;
      } else if (btn.dataset.act === "check-allow") {
        btn.disabled = true;
        const prevState = btn.dataset.allowState || state.allowByTask[id] || "unknown";
        paintAllowBtn(btn, "unknown", id);
        btn.textContent = "检测中…";
        try {
          const info = await api(`/tasks/${id}/lgns-allowance`);
          if (info.ok) {
            paintAllowBtn(btn, "ok", id);
            toast("已授权");
          } else {
            paintAllowBtn(btn, "bad", id);
            const amountLabel = await askApproveConfirm(info.spender);
            if (amountLabel == null) {
              paintAllowBtn(btn, "bad", id);
              return;
            }
            btn.textContent = "授权中…";
            const res = await api(`/tasks/${id}/lgns-approve`, {
              method: "POST",
              body: JSON.stringify({ amount: amountLabel }),
            });
            if (res.ok) {
              paintAllowBtn(btn, "ok", id);
              toast(res.sim ? "sim：已记录 would_approve" : `授权已发送 ${res.txHash || ""}`);
            } else {
              paintAllowBtn(btn, "bad", id);
              toast(res.error || "授权失败", true);
            }
          }
        } catch (err) {
          paintAllowBtn(btn, prevState, id);
          throw err;
        } finally {
          btn.disabled = false;
        }
        return;
      }
      await refreshLists();
    } catch (err) {
      toast(err.message, true);
    }
  });

  $("#failedOnly").addEventListener("change", () => refreshLists().catch((e) => toast(e.message, true)));

  const mnemonicDlg = $("#mnemonicDialog");
  const privateKeyDlg = $("#privateKeyDialog");
  const confirmApproveDlg = $("#confirmApprove");
  const confirmDeleteTaskDlg = $("#confirmDeleteTask");
  const taskLogsDlg = $("#taskLogsDialog");
  bindDialogBackdrop(mnemonicDlg);
  bindDialogBackdrop(privateKeyDlg);
  bindDialogBackdrop(confirmApproveDlg);
  bindDialogBackdrop(confirmDeleteTaskDlg);
  bindDialogBackdrop(taskLogsDlg);
  $("#btnCloseTaskLogs")?.addEventListener("click", () => taskLogsDlg?.close());
  $("#btnImportMnemonic").addEventListener("click", () => {
    $("#mnemonicForm").reset();
    $("#mnemonicForm [name=path]").value = "m/44'/60'/0'/0/0";
    mnemonicDlg.showModal();
  });
  $("#btnImportPrivateKey").addEventListener("click", () => {
    $("#privateKeyForm").reset();
    privateKeyDlg.showModal();
  });
  $("#btnCloseMnemonic").addEventListener("click", () => mnemonicDlg.close());
  $("#btnClosePrivateKey").addEventListener("click", () => privateKeyDlg.close());
  $("#mnemonicForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    try {
      await api("/wallets/import", {
        method: "POST",
        body: JSON.stringify({
          mode: "mnemonic",
          name: fd.get("name") || undefined,
          mnemonic: fd.get("mnemonic"),
          path: fd.get("path") || undefined,
        }),
      });
      toast("助记词钱包已导入");
      mnemonicDlg.close();
      await refreshLists();
    } catch (err) {
      toast(err.message, true);
    }
  });
  $("#privateKeyForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    try {
      await api("/wallets/import", {
        method: "POST",
        body: JSON.stringify({
          mode: "privateKey",
          name: fd.get("name") || undefined,
          privateKey: fd.get("privateKey"),
        }),
      });
      toast("私钥钱包已导入");
      privateKeyDlg.close();
      await refreshLists();
    } catch (err) {
      toast(err.message, true);
    }
  });
  const walletRenameDlg = $("#walletRenameDialog");
  if (walletRenameDlg) {
    bindDialogBackdrop(walletRenameDlg);
    $("#btnCancelWalletRename")?.addEventListener("click", () => walletRenameDlg.close());
    $("#walletRenameForm")?.addEventListener("submit", async (e) => {
      e.preventDefault();
      const fd = new FormData(e.target);
      const id = (fd.get("walletId") || "").toString();
      const name = (fd.get("name") || "").toString().trim();
      if (!id) return;
      if (!name) {
        toast("名称不能为空", true);
        return;
      }
      try {
        await api(`/wallets/${id}`, { method: "PATCH", body: JSON.stringify({ name }) });
        toast("钱包名称已更新");
        walletRenameDlg.close();
        await refreshLists();
      } catch (err) {
        toast(err.message, true);
      }
    });
  }

  $("#walletBody").addEventListener("click", async (e) => {
    const copyAddr = e.target.closest("[data-act=copy-addr]");
    if (copyAddr) {
      e.stopPropagation();
      await copyAddrFromBtn(copyAddr);
      return;
    }
    const openAddr = e.target.closest("[data-act=open-addr]");
    if (openAddr) {
      openAddrFromClick(e, openAddr.dataset.addr);
      return;
    }
    const editName = e.target.closest("[data-act=edit-wallet-name]");
    if (editName) {
      e.stopPropagation();
      const tr = editName.closest("tr");
      const id = tr?.dataset.id;
      const wal = state.wallets.find((w) => w.id === id);
      if (!wal || !walletRenameDlg) return;
      const form = $("#walletRenameForm");
      form.walletId.value = wal.id;
      form.name.value = wal.name || "";
      walletRenameDlg.showModal();
      form.name.focus();
      form.name.select();
      return;
    }
    const btn = e.target.closest("[data-act=del-wallet]");
    if (!btn) return;
    const id = btn.closest("tr")?.dataset.id;
    if (!id || !confirm("确定删除该钱包？")) return;
    try {
      await api(`/wallets/${id}`, { method: "DELETE" });
      toast("钱包已删除");
      await refreshLists();
    } catch (err) {
      toast(err.message, true);
    }
  });

  const settingsDlg = $("#settingsDialog");
  const presetDlg = $("#presetDialog");
  bindDialogBackdrop(settingsDlg);
  bindDialogBackdrop(presetDlg);
  $("#btnSettings").addEventListener("click", () => openSettings("general"));
  $("#btnCloseSettings").addEventListener("click", () => settingsDlg.close());
  $("#btnAddPreset").addEventListener("click", () => showPresetEditor(null));
  $("#btnCancelPreset").addEventListener("click", () => clearPresetEditor());
  $("#btnBackPreset")?.addEventListener("click", () => clearPresetEditor());
  $("#presetAbiInput")?.addEventListener("input", () => updatePresetAbiUI());
  $("#presetFunctionInput")?.addEventListener("change", () => updatePresetAbiUI());
  $("#presetFunctionInput")?.addEventListener("input", () => updatePresetAbiUI());
  $$("#settingsTabs .tab").forEach((btn) => btn.addEventListener("click", () => {
    $$("#settingsTabs .tab").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    $$(".tab-panel").forEach((p) => p.classList.toggle("active", p.id === "tab-" + btn.dataset.tab));
    if (btn.dataset.tab === "params") renderPresetManageList();
  }));

  $("#settingsForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    try {
      await api("/settings", {
        method: "PUT",
        body: JSON.stringify({
          signLeadMs: Number(fd.get("signLeadMs")),
          earlyBroadcast: fd.get("earlyBroadcast") === "on",
          rpcTimeoutMs: Number(fd.get("rpcTimeoutMs")) || 12000,
          batchConcurrency: Number(fd.get("batchConcurrency")),
          gasMultiplier: Number(fd.get("gasMultiplier")),
          ntpHost: String(fd.get("ntpHost") || "").trim() || "pool.ntp.org",
          ntpIntervalSec: Number(fd.get("ntpIntervalSec")) || 30,
        }),
      });
      toast("通用设置已保存");
      await refreshHealth();
    } catch (err) {
      toast(err.message, true);
    }
  });

  $("#presetManageList").addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-act]");
    if (!btn) return;
    const row = btn.closest(".task-manage-row");
    const id = row?.dataset.id;
    if (!id) return;
    if (btn.dataset.act === "edit-preset") {
      showPresetEditor(id);
      return;
    }
    if (btn.dataset.act === "del-preset") {
      if (!confirm(`确定删除任务类型「${id}」？`)) return;
      try {
        await api(`/presets/${id}`, { method: "DELETE" });
        toast("类型已删除");
        if (state.selectedPresetId === id) clearPresetEditor();
        await refreshLists();
        fillTaskSelects();
      } catch (err) {
        toast(err.message, true);
      }
    }
  });

  $("#presetForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    const id = String(fd.get("id") || "").trim();
    if (!/^[a-z][a-z0-9_]{1,31}$/.test(id)) {
      toast("类型 ID 须为小写字母开头，仅含 a-z 0-9 _", true);
      return;
    }
    let abi;
    try {
      abi = JSON.parse(String(fd.get("abi") || "").trim());
    } catch {
      toast("ABI 不是合法 JSON", true);
      return;
    }
    if (!Array.isArray(abi)) {
      toast("ABI 须为 JSON 数组", true);
      return;
    }
    const fn = updatePresetAbiUI();
    if (!fn) {
      toast($("#presetAbiMeta")?.textContent || "ABI 无效", true);
      return;
    }
    const args = collectArgFields(e.target, "arg", fn.inputs?.length || 0);
    const body = {
      label: fd.get("label"),
      description: fd.get("description") || "",
      icon: state.presets[id]?.icon || "diamond",
      to: fd.get("to"),
      abi,
      function: String(fd.get("function") || fn.name || "").trim(),
      defaultSchedule: fd.get("defaultSchedule"),
      defaultParams: {
        args,
        gasLimit: Number(fd.get("gasLimit") || 0) || undefined,
      },
      needsLgnsApprove: !!fd.get("needsLgnsApprove"),
    };
    // keep legacy fields for older tasks/logs (claim only; claimAll has no args)
    if (body.function === "claim" && args[0] != null && args[0] !== "") {
      const n = Number(args[0]);
      if (Number.isFinite(n)) body.defaultParams.claimArg = n;
    }
    if (body.function === "burnForPower" && args[0]) {
      body.defaultParams.burnAmount = args[0];
    }
    try {
      const res = await api(`/presets/${id}`, { method: "PUT", body: JSON.stringify(body) });
      const n = res.syncedTasks || 0;
      toast(n > 0 ? `任务类型已保存，已同步 ${n} 个任务的定时` : "任务类型已保存");
      clearPresetEditor();
      await refreshLists();
      fillTypeSelect();
    } catch (err) {
      toast(err.message, true);
    }
  });

  $("#simTypeSelect")?.addEventListener("change", updateSimTypeDesc);
  $("#simWalletSelect")?.addEventListener("change", () => {
    syncSimFromAddress();
  });
  $("#simFromInput")?.addEventListener("input", () => {
    const input = $("#simFromInput");
    const walletSel = $("#simWalletSelect");
    if (!walletSel) return;
    const addr = String(input?.value || "").trim().toLowerCase();
    const hit = state.wallets.find((w) => String(w.address || "").toLowerCase() === addr);
    walletSel.value = hit ? hit.id : "";
  });

  $("#btnSimulate").addEventListener("click", async () => {
    const taskType = $("#simTypeSelect")?.value;
    if (!taskType) return toast("请先选择任务类型", true);
    const from = String($("#simFromInput")?.value || "").trim();
    if (!/^0x[a-fA-F0-9]{40}$/.test(from)) {
      return toast("请填写有效的 from 地址", true);
    }
    const box = $("#simResult");
    const summary = $("#simResultSummary");
    box.textContent = "模拟中…";
    box.className = "sim-result";
    if (summary) {
      summary.textContent = "";
      summary.className = "sim-summary";
    }
    try {
      const res = await api("/simulate", {
        method: "POST",
        body: JSON.stringify({ taskType, from }),
      });
      box.textContent = JSON.stringify(res, null, 2);
      box.className = "sim-result " + (res.ok ? "ok" : "bad");
      if (summary) {
        summary.textContent = explainSimResult(res);
        summary.className = "sim-summary " + (res.ok ? "ok" : "bad");
      }
      toast(res.ok ? "模拟成功" : "模拟失败（revert）", !res.ok);
    } catch (err) {
      box.textContent = err.message;
      box.className = "sim-result bad";
      if (summary) {
        summary.textContent = `结果：请求失败 — ${err.message}`;
        summary.className = "sim-summary bad";
      }
      toast(err.message, true);
    }
  });

}

bind();
bindAddrHoverTip();
refreshAll().catch((e) => toast(e.message, true));
setInterval(() => {
  refreshHealth().catch(() => {});
  refreshLogsLight().catch(() => {});
}, 2000);
setInterval(() => { refreshLists().catch(() => {}); }, 8000);
setInterval(tickCountdowns, 1000);
