# 机制说明

抢额度是时间竞争：在额度开放瞬间把已签名交易送入 mempool，争取落入开放后第 1 个区块。

## 流程

1. 任务列表按 `hourly` / `daily_1159_cst` 计算 `fireAt`
2. `fireAt - SIGN_LEAD_MS` 刷新 nonce/gas 并签名
3. `MODE=sim` 只记日志；`MODE=live` 并发 `SendTransaction`
4. 回写任务状态与运行日志

## 三类入口

- **固定价销毁债券** → 默认 `claimAll()` @ `0x3f40…E71F`，整点；任务可改为 `claim(n)` 领指定编号
- **LGNS 销毁债券** → 默认 `claimAll()` @ `0x11b1…7c69`，整点；任务可改为 `claim(n)` 领指定编号
- **AI 算力机器人（Helix）** → `burnForPower(500e9)` @ `0x9E68…8C1b`，每日 11:59+08

债券任务参数 `claimMode`：`all`（或空）→ `claimAll()`；`index` → `claim(args[0])`（编号可为 0）。任务编辑与「查看债券」弹窗可切换；切换任务类型会回退为 `claimAll`。

固定价 **已领记录** 跟 LGNS 从 `0x9D46…` 转入，不是债券合约上的 `Claimed` 事件。两种查法（count API / 链上 Transfer）见 `docs/BURN_CLAIM_HISTORY.md`。

链 ID 6714，gas 用 DAI。详见 `docs/VERIFICATION.md`。
