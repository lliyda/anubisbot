async function loadPresets() {
  const presets = await fetch("/api/presets").then((r) => r.json());
  const sel = document.getElementById("type");
  sel.innerHTML = Object.entries(presets).map(([k, p]) => `<option value="${k}">${p.label}</option>`).join("");
  const sync = () => {
    document.getElementById("desc").textContent = presets[sel.value]?.description || "";
  };
  sel.onchange = sync;
  sync();
}

document.getElementById("btnConnect").onclick = async () => {
  if (!window.ethereum) {
    alert("未检测到钱包扩展");
    return;
  }
  const accounts = await window.ethereum.request({ method: "eth_requestAccounts" });
  document.getElementById("account").textContent = accounts[0] || "";
  try {
    await window.ethereum.request({
      method: "wallet_addEthereumChain",
      params: [{
        chainId: "0x1A3A",
        chainName: "Anubis",
        nativeCurrency: { name: "DAI", symbol: "DAI", decimals: 18 },
        rpcUrls: ["https://rpc.anubispace.org"],
        blockExplorerUrls: ["https://browser.anubispace.org"],
      }],
    });
  } catch (_) {}
};

loadPresets().catch(console.error);
