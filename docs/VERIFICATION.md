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
