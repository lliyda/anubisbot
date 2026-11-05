const $ = (s) => document.querySelector(s);

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
  const el = document.createElement("div");
  el.className = "toast" + (bad ? " bad" : "");
  el.textContent = msg;
  box.appendChild(el);
  setTimeout(() => el.remove(), 3200);
}

function fillConfig(cfg) {
  const f = $("#cfgForm");
  if (!f || !cfg) return;
  f.listen.value = cfg.listen || "";
  f.botHTTP.value = cfg.botHTTP || "";
  f.botExe.value = cfg.botExe || "";
  f.githubRepo.value = cfg.githubRepo || "";
  f.assetName.value = cfg.assetName || "";
}

async function refreshLogs() {
  try {
    const data = await api("/logs");
    $("#logBox").textContent = (data.logs || []).join("\n") || "（暂无）";
  } catch {
    /* ignore */
  }
}

async function refreshStatus() {
  try {
    const st = await api("/status");
    $("#panelVer").textContent = st.panelVersion || "—";
    const h = st.botHealth || {};
    $("#botVer").textContent = h.version || (st.botHealthy ? "—" : "离线");
    const ok = !!st.botHealthy;
    $("#statusDot").className = "dot " + (ok ? "ok" : "bad");
    $("#statusText").textContent = ok ? "运行中" : "未运行 / 不可达";
    const bits = [];
    if (h.mode) bits.push("mode=" + h.mode);
    if (h.offsetMs != null) bits.push("offset=" + h.offsetMs + "ms");
    if (h.rttMs != null) bits.push("rtt=" + h.rttMs + "ms");
    $("#statusMeta").textContent = bits.join(" · ") || (st.botHTTP || "");
    if (st.config) fillConfig(st.config);
  } catch (err) {
    $("#statusDot").className = "dot bad";
    $("#statusText").textContent = "面板状态失败";
    $("#statusMeta").textContent = err.message;
  }
  await refreshLogs();
}

function busy(btns, on) {
  btns.forEach((b) => { if (b) b.disabled = !!on; });
}

$("#btnOpen").addEventListener("click", async () => {
  try {
    await api("/open", { method: "POST", body: "{}" });
    toast("已打开运维页");
  } catch (e) {
    toast(e.message, true);
  }
});

$("#btnStart").addEventListener("click", async () => {
  busy([$("#btnStart")], true);
  try {
    await api("/bot/start", { method: "POST", body: "{}" });
    toast("已启动");
    await refreshStatus();
  } catch (e) {
    toast(e.message, true);
  } finally {
    busy([$("#btnStart")], false);
  }
});

$("#btnStop").addEventListener("click", async () => {
  busy([$("#btnStop")], true);
  try {
    await api("/bot/stop", { method: "POST", body: "{}" });
    toast("已停止");
    await refreshStatus();
  } catch (e) {
    toast(e.message, true);
  } finally {
    busy([$("#btnStop")], false);
  }
});

$("#btnRestart").addEventListener("click", async () => {
  const btn = $("#btnRestart");
  busy([btn], true);
  try {
    await api("/bot/restart", { method: "POST", body: "{}" });
    toast("已重启");
    await refreshStatus();
  } catch (e) {
    toast(e.message, true);
  } finally {
    busy([btn], false);
  }
});

$("#btnRefresh").addEventListener("click", () => refreshStatus());

$("#btnOtaCheck").addEventListener("click", async () => {
  const btn = $("#btnOtaCheck");
  busy([btn], true);
  try {
    const info = await api("/ota/check", { method: "POST", body: "{}" });
    $("#otaBox").textContent = JSON.stringify(info, null, 2);
    toast(info.update ? `发现 ${info.tagName}` : `已是最新 ${info.current}`);
  } catch (e) {
    $("#otaBox").textContent = e.message;
    toast(e.message, true);
  } finally {
    busy([btn], false);
    refreshLogs();
  }
});

$("#btnOtaApply").addEventListener("click", async () => {
  if (!confirm("将下载 Release、停止服务并替换 anubisbot.exe，继续？")) return;
  const btn = $("#btnOtaApply");
  busy([btn], true);
  try {
    const res = await api("/ota/apply", { method: "POST", body: "{}" });
    $("#otaBox").textContent = JSON.stringify(res, null, 2);
    toast("OTA 完成");
    await refreshStatus();
  } catch (e) {
    $("#otaBox").textContent = e.message;
    toast(e.message, true);
  } finally {
    busy([btn], false);
    refreshLogs();
  }
});

$("#cfgForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  try {
    const saved = await api("/config", {
      method: "PUT",
      body: JSON.stringify({
        listen: String(fd.get("listen") || "").trim(),
        botHTTP: String(fd.get("botHTTP") || "").trim(),
        botExe: String(fd.get("botExe") || "").trim(),
        githubRepo: String(fd.get("githubRepo") || "").trim(),
        assetName: String(fd.get("assetName") || "").trim(),
      }),
    });
    fillConfig(saved);
    toast("配置已保存");
  } catch (err) {
    toast(err.message, true);
  }
});

refreshStatus();
setInterval(refreshStatus, 3000);
