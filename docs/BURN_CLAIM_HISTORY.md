# 固定价销毁债券：已领记录怎么查

`claimAll()` 打到固定价债券合约，但 **LGNS 实际从付款合约转入钱包**。查「已领」应跟 LGNS 转入，不要只扫债券合约上的 `Claimed` 事件（该路径对样例地址常为 0 条）。

## 相关地址

| 角色 | 地址 |
| --- | --- |
| 固定价债券（claimAll 目标） | `0x3f403751975e1EB06d95901f7FF246513444E71F` |
| LGNS 付款 / 已领来源 | `0x9D46CB414E76237d3b83E2a1630481038368f217` |
| LGNS 代币 | `0x4D1D808a081FdAc440703b3765FC61f8028C06B8` |
| 浏览器合约页 | https://browser.anubispace.org/address/0x9D46CB414E76237d3b83E2a1630481038368f217 |

判定一条「固定价已领」：

```text
token = LGNS
方向 = 转入（in）
from = 0x9D46CB414E76237d3b83E2a1630481038368f217
to   = 查询钱包
```

样例钱包 `0xC29F96861b1e5f00cac071e6a1139ef5CefCDc34`：整点成功 tx `0xc513fc769f32ffd25defb98ee96aead9eaa7dca6d0878a6e6a941cb1e2fa8a4a`，约 **7594.16 LGNS**（from `0x9D46…`）。

---

## 方式一：count.web3origin.com（快，近期窗口）

```http
POST https://count.web3origin.com/wallet?chain=anubis&addr={addr}
Content-Type: application/x-www-form-urlencoded

chain=anubis&addr={addr}
```

- 响应 JSON：`ok`、`tokens`、`transfers[]`（字段含 `ts` / `token` / `from` / `to` / `amount` / `dir` / `hash`）
- 领取记录：过滤 `token=="LGNS" && dir=="in" && from==0x9D46…`
- 实测样例地址约 **1～2s** 返回；`transfers` 偏**近期**，更早历史可能不全
- 同 URL 的 GET 也可返回同类数据；对接以 POST + form 为准

---

## 方式二：链上 / 浏览器（全一点，可分页）

### 2a. Blockscout 代币转账

```text
GET https://browser.anubispace.org/api/v2/addresses/{addr}/token-transfers?type=ERC-20
```

筛：`token` 为 LGNS，且 `from.hash == 0x9D46…`。可翻页补历史。

### 2b. RPC `eth_getLogs`（LGNS `Transfer`）

在 LGNS 合约上按 topic 过滤：

- `topic0` = `Transfer(address,address,uint256)` 的 keccak
- `topic1` = `from` = `0x9D46…`（左垫 32 字节）
- `topic2` = `to` = 查询钱包

分块扫（注意节点 `eth_getLogs` 块范围上限，如 5000），新→旧，默认取最近 **10** 条；需要更多再继续往前扫（对应运维页「展示更多」）。

---

## 运维页建议

1. 优先方式一（count API），快速填「领取记录」表  
2. 方式一失败、为空、或用户点「展示更多」时，用方式二补更早记录  
3. 不要再把 FixedPrice 上的 `Claimed` 事件当作固定价已领的主数据源  

可领取余额仍用债券合约 `eth_call`（`getTotalClaimable` 等），与已领历史数据源分开。
