# 链上核验

证据来源：https://web3origin.com/tools/bot-vip/ Real results → browser.anubispace.org

## 已证实

| 类型 | 合约 | 方法 | 开放时刻 |
| --- | --- | --- | --- |
| 固定价销毁债券 | `0x3f403751975e1EB06d95901f7FF246513444E71F` | 默认 `claimAll()` `0xd1058e59`；可选 `claim(uint256)` `0x379607f5` | 整点整秒 |
| LGNS 销毁债券 | `0x11b10C9827c5B7071E96fcAa143B4e6E86b17c69` | 同上 | 整点整秒 |
| Helix | `0x9E68F61E2615cc1D2C4a2D0026bFc1EBFdf68C1b` | `burnForPower` `0xffa6b88a` amount=500e9 | ~11:59:01 +08 |

债券类默认与 Origin「领取全部」对齐（`claimAll`）。任务可改为按存款下标 `claim(n)`。查看债券列表读：

| 调用 | selector |
| --- | --- |
| `depositCount(address)`（现用） | `1bae91a4` |
| `getTotalClaimable(address)` | `267e8ab6` |
| `userDeposits(address,uint256)` | `08f43333` |
| `getClaimable(address,uint256)` | `6f5244b1` |
| `accReleasedFrac()` | `e0a12f52` |

`userDeposits` 返回 9 个 word（与 Origin ABI 一致）：

| # | 名称 | 含义 |
| --- | --- | --- |
| 0 | `daiValue` | 本金 DAI（1e18） |
| 1 | `capBps` | 总释放倍数（20000=200%，23000=230%） |
| 2 | `burnedLgns` | 已销毁 LGNS（1e9） |
| 3 | `releasedDaiValue` | 已入账释放 DAI（未含当期 acc 增量） |
| 4 | `claimedLgns` | 已领取 LGNS（1e9） |
| 5 | `lastAccReleasedFrac` | 上次结算的全局释放分数 |
| 6 | `lastAccLgnsPerDai` | （列表展示不用） |
| 7 | `depositTimestamp` | 存入时间 unix |
| 8 | `mode` | 模式 |

已释放展示（对齐 Origin，**分母是总释放不是本金**）：

```text
total    = daiValue * capBps / 10000
delta    = accReleasedFrac > lastAccReleasedFrac
         ? daiValue * (accReleasedFrac - lastAccReleasedFrac) / 1_000_000
         : 0
released = min(releasedDaiValue + delta, total)
display  = released / total
```

历史样例（按债券 `claim(1)`）：

- fixed: https://browser.anubispace.org/tx/0x7e55a237aaffdac03a4cf3d2b40f5f7f1f03ba51d806fc6d08a9808e3fcf34e8
- lgns: https://browser.anubispace.org/tx/0xe9790ffc7996fccc2b01a5b90f04ca239be191e8e4d17a86a27e6d972fcb36d3
- helix: https://browser.anubispace.org/tx/0xe7acf67089a776d790964575a91c5257c007c709ac35cd2549e616cb9b43b86d

债券 proxy 共用 implementation `0x8AF6b33632725f6E30F8266DEb0835295332B626`。
样例 claim tx 内未见 DAI transfer；Helix 烧毁 LGNS，需 approve。

固定价 claim 成功时 LGNS 常从 `0x9D46CB414E76237d3b83E2a1630481038368f217` 转入钱包（非债券地址直接转出）。查已领的两种方式见 `docs/BURN_CLAIM_HISTORY.md`。

## 待观察

- 非整点 `claimAll` 的 revert（额度帽 / 窗口）
- 固定价是否需前置 DAI approve/存款
- 额度耗尽与单地址限制的 revert 原因
