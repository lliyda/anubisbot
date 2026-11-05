# 机制说明

抢额度是时间竞争：在额度开放瞬间把已签名交易送入 mempool，争取落入开放后第 1 个区块。

## 流程

1. 任务列表按 `hourly` / `daily_1159_cst` 计算 `fireAt`
2. `fireAt - SIGN_LEAD_MS` 刷新 nonce/gas 并签名
3. `MODE=sim` 只记日志；`MODE=live` 并发 `SendTransaction`
4. 回写任务状态与运行日志

## 三类入口

- **固定定价销毁债券** → `claim(1)` @ `0x3f40…E71F`，整点
- **LGNS 销毁债券** → `claim(1)` @ `0x11b1…7c69`，整点
- **AI 算力机器人（Helix）** → `burnForPower(500e9)` @ `0x9E68…8C1b`，每日 11:59+08

链 ID 6714，gas 用 DAI。详见 `docs/VERIFICATION.md`。
