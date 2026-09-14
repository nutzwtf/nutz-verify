# NUTZ — week-1 fork measurements

Produces the three numbers the spec leaves open (§9 residuals for Q2, Q3, Q7) plus the four on-chain reads.

## Prereqs
- Foundry (`foundryup`), an RPC for chain 4663 (Chainstack / QuickNode; the public endpoint will rate-limit fork tests).
- Fill the TODOs at the top of `ForkMeasurements.t.sol`: MU and SPCX full addresses (docs.robinhood.com/chain/contracts, live table), Chainlink feed addresses (docs.robinhood.com/chain/oracles-and-price-feeds).

## On-chain reads (30 seconds)
```bash
export RPC=https://<your-4663-endpoint>
F=0x7eD598BcEf8bd9Edd8C97A195C6d13f40801EC7e
cast call $F "canLaunch(address)(bool)" <LAUNCH_WALLET> --rpc-url $RPC
cast call $F "maxCreatorTaxBps()(uint16)" --rpc-url $RPC
cast call $F "launchConfigCount()(uint256)" --rpc-url $RPC
cast call 0x0000000000000000000000000000000000000064 "arbOSVersion()(uint64)" --rpc-url $RPC   # subtract 55; need >= 51 for EIP-2537
# StockFactory provenance: on Blockscout, open each stock token, confirm it is a BeaconProxy whose
# implementation is the shared Stock implementation (0xb354…5ae2) and the deployer is Robinhood's StockFactory.
```

## Run the measurements
```bash
forge init nutz-fork && cd nutz-fork && rm -rf src test script
mkdir test && cp /path/to/ForkMeasurements.t.sol test/
forge test --match-contract ForkMeasurements -vvv --fork-url $RPC
```

## What to record in the spec
| Test | Fills |
|---|---|
| `test_6_swapCosts` | best fee tier per leg, cost in bps at $1k/$10k/$50k → `MAX_SLIPPAGE_BPS`, default routes, Q8 (ETH→USDG spread). **Measured by `ConverterFork.t.sol` below.** |
| `test_7_transfers` | transfer to EOA / contract OK per token, `uiMultiplier` values; then complete the paused/blocklisted assertions once the role addresses are read from the implementation |
| `test_8_pushGasEstimate` | `PUSH_GAS_UNITS`, `OPS_CAP_WEI`; add the L1 DA component from a real mainnet receipt (`l1Fee`) before finalizing |

Sanity rule from the spec: every hourly stock buy must stay under ~1% of that pool's liquidity; if the $50k leg shows > 100 bps cost on any stock, split that leg across v3 and v4 pools.

---

## Converter Sweep on the real Venues (`test/fork/ConverterFork.t.sol`)

Converter spec §10, ticket 08. The test deploys the Distributor and the Converter the way the script does (Converter address predicted from the deployer's next nonce) with the token, Venue and Pons addresses in `script/config/robinhood.json`, deals ETH worth ≈ $1k, $10k and $50k to the unbound Converter, and sweeps each once with the Keeper's policy Routes (`minOut` = quote × 0.99):

- v3 for all five Legs: WETH/USDG 0.01%, SPY/USDG 0.05%, NVDA/USDG 0.05%, MU/USDG 0.3%, SPCX/USDG 0.05%;
- the same with SPY on the v4 SPY/USDG (3000, 60) pool, hookless.

Every Leg is quoted first with the v3 QuoterV2 (`quoteExactInput`); the v4 Leg's `minOut` follows the v4 Quoter, as the Keeper would quote the Venue it routes through, but its output is still held to the v3 quote. The Sweep must then: run every Leg (no `LegSkipped`), realise each Leg within 1% of its v3 quote and at a cost of at most `MAX_SLIPPAGE_BPS` (100 bps) against a $100 trade in the same v3 pool, fund the Distributor with exactly the `Swept` amounts (ledger and token balances), pay Ops to the Keeper, and leave the Converter holding nothing but the ETH above `MAX_SWEEP_ETH`. Afterwards every Reward Token the Distributor holds is transferred to a fresh EOA.

### Run

```bash
# .env: RPC_4663=<Chainstack archive URL>. The test forks from RPC_4663 itself.
forge test --match-path test/fork/ConverterFork.t.sol --fork-url robinhood -vvv
# A non-archive endpoint cannot serve the pinned block: fork the latest one instead.
FORK_BLOCK_4663=0 forge test --match-path test/fork/ConverterFork.t.sol --fork-url robinhood -vvv
```

The self-skip on an empty `RPC_4663` applies to plain `forge test`: `--fork-url robinhood` resolves the variable before any test runs, so with it forge aborts instead. `--fork-url` is what lets `--fork-retry-backoff` be passed; the block is `FORK_BLOCK` in the test (`FORK_BLOCK_4663` overrides it). In CI the `fork` job of `.github/workflows/ci.yml` runs it on manual `workflow_dispatch` and nightly with `RPC_4663` from the repository secrets, and skips the step when the secret is empty. Note that the `forge` on PATH is a mise shim that re-exports `.env` on every call, so `RPC_4663=... forge test` on the command line is overridden by the `.env` value: fill `.env`.

### Measured 2026-09-13 (block ≈ 62,399,000, the latest at the time; `FORK_BLOCK` pins 62,393,542; 1 ETH = 2,483 USDG)

Cost is the Leg's quote at its size against the unit price of a $100 trade in the same v3 pool, both quoted before the Sweep, so the pool fee cancels and a v3 figure is price impact alone. For the v4 SPY Leg it is the gap to the v3 0.05% pool: the v4 pool charges 0.3%, so ≈ 25 bps is fee and the rest price.

| Sweep | ETH swept | ETH→USDG | SPY v3 | NVDA | MU | SPCX | SPY v4 (3000, 60) |
|---|---|---|---|---|---|---|---|
| $1k | 0.403 | 0.0 bps | 0.0 | 0.0 | 0.0 | 0.0 | 56.2 |
| $10k | 4.028 | 0.2 bps | 1.1 | 0.1 | 0.8 | 0.1 | 56.3 |
| $50k | 20 (cap; 0.13 left) | 1.1 bps | 5.9 | 0.5 | 4.3 | 0.7 | 56.3 |

Findings:

- Realised output equals the quote to the wei on every Leg and every size: one block, one deterministic AMM. The 1% assertion is the policy bound, not the observed error.
- Every v3 Leg costs under 6 bps at $50k, far inside `MAX_SLIPPAGE_BPS = 100`, which the test asserts per Leg. The v3 Routes above are the defaults; the v4 SPY pool is a fallback only, ≈ 56 bps worse at every size (still inside the bound, so a Keeper falling back to it stays within policy).
- ETH→USDG through the 0.01% pool costs 1.1 bps on 19.6 ETH: Q8 (ETH→USDG spread > ~1% would favour a USDG-quoted launch) is answered no.
- At $2,483 per ETH, $50k is 20.13 ETH, above `MAX_SWEEP_ETH` = 20 ETH: the Sweep converts 20 ETH and leaves 0.13 ETH for the next hour, so the third row measures a 20 ETH Sweep and exercises the cap.
- All four Stock Tokens and USDG transfer from the Distributor (a contract) to a fresh EOA. The paused and blocklisted cases (Q2 residual) are still open for `ForkMeasurements.t.sol`.
