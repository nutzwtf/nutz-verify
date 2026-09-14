# 03 — Chain reader: JSON-RPC, log decoding, exclusion reconstruction

Status: ready-for-agent
Type: task
Spec: ../spec.md §4; ADR-0002, ADR-0004
Blocked by: 02

Implement `internal/chain` on `net/http` + `encoding/json`. No go-ethereum (ADR-0004). Four decode shapes, all known at compile time: `Transfer(address,address,uint256)` (2 topics + 1 data word), `RootPosted(uint8,uint256,bytes32,uint256[5],uint256[5])` (2 topics + 11 static data words), `ExcludedAppended(address)` (1 topic), and the one dynamic return, `excluded() → address[]`. Plus `ledger()`, block-by-number, and the `latest`/`safe`/`finalized` tags.

Page `eth_getLogs` in ≤ 2,000-block ranges (engineering spec §4.1: the public-RPC cap). Support repeatable `--rpc` with cross-check: any disagreement between endpoints is INDETERMINATE, never a silent pick.

Build the Excluded set **as of an Epoch** from the log stream alone: every `account` of an `ExcludedAppended` log whose block timestamp is `< 3600(e+1)`. No `excluded()` subtraction — the Distributor's constructor emits `ExcludedAppended` for every base entry (verified in `src/NutzDistributor.sol`), so the logs are self-sufficient. An entry applies to **the whole Epoch containing its block**, not from its block onward. Also expose `keccak256(abi.encodePacked(set))` over the ascending, de-duplicated addresses — that is engineering spec §4.5's `exclusion set hash` and makes artifact cross-checking free.

Tests: anvil fork of 4663 running `nutz-contracts/script/Deploy.s.sol`, a stand-in ERC-20 for NUTZ, transfers and `postRoot` driven from the harness, every decoder asserted against real node output. This harness is what justifies hand-rolling the decoding at all (ADR-0004), so it is part of this ticket and not a follow-up. Include an exclusion appended mid-Epoch and assert the set reconstructed for an earlier Epoch does not contain it.
