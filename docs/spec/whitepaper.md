# $NUTZ — The Squirrel That Pays You

**Whitepaper v0.2 · Draft for internal review · September 2026** (v0.1 updated 2026-09-12 to match the engineering spec v0.2)
**Chain:** Robinhood Chain (Ethereum L2, chain id 4663)
**Ticker:** NUTZ · **Site:** nutz.wtf · **Socials:** @stacknutz

> Kinda chic to get paid in nutz.

---

## TL;DR

$NUTZ is a memecoin on Robinhood Chain that pays its holders every hour in two things: **tokenized stocks** and **cash (USDG)**. Every trade of $NUTZ generates a fee. 100% of the creator side of that fee goes into the **Nut Vault**, which buys a fixed basket of stock tokens (SPY, NVDA, MU, SPCX) plus USDG, and drops them to holders in proportion to how much they hold and how long they've held it. 98% goes to holders; 2% pays the system's own gas so it runs forever without anyone funding it. The team takes nothing. The rules live in open-source contracts, and every payout can be independently verified.

The mascot is the Tornado Squirrel. He holds through the storm and keeps stacking nutz. So do you.

---

## 1. Why this exists

Robinhood Chain launched in July 2026 and memecoins immediately became its biggest source of activity. The winners so far share one thing: they are about **money and markets**, because the people trading them are Robinhood users. Cash Cat is Robinhood lore. Artificial Inu is paired with Nvidia stock. Saylormoon holds MicroStrategy. The hottest trade on the chain is memecoins that touch real stocks.

Most of them have a gap. They *talk* about paying holders. They rarely *do* it, automatically, into your wallet, on a schedule you can set your watch by. The few that do (MarsCoin on BNB, BOOMER on Robinhood Chain) proved the demand: holders stay, because leaving means the drops stop.

$NUTZ closes the gap with a simple promise: **hold the squirrel, get paid in nutz** — real stock tokens and real dollars, every hour, no claim required for most wallets, no minimum bag, no team cut.

---

## 2. The meme

**The Tornado Squirrel.** A cartoon squirrel calmly raising a paw as a tornado pulls it away. The format took off on TikTok in July 2026 and was everywhere on X by late August — the reaction image for "everything is falling apart and I'm fine." The joke started as an edit of a movie scene; the squirrel itself is nobody's character. NUTZ uses its own original squirrel and never the film footage: the same feeling, our own drawing.

A squirrel is also the original investor: it stacks nutz for winter and doesn't touch them. The metaphor writes itself. Nutz are stocks. The tornado is the market. The squirrel is you.

**The voice: Kinda Chic.** The "Kinda chic to…" caption format turns un-glamorous things into flexes. It is the perfect content engine for a dividend coin, because every payout screenshot becomes a post:

- Kinda chic to own Nvidia because of a squirrel.
- Kinda chic to have a retirement plan at 23.
- Kinda chic to get a paycheck every hour and do nothing.

Holders write the marketing. The squirrel just keeps dropping.

---

## 3. How it works (the short version)

1. **You buy $NUTZ** on Robinhood Chain (bonding curve first, then a locked Uniswap v4 pool).
2. **Every trade pays a fee.** The creator's share of that fee — plus a creator tax set to the protocol maximum — is routed to the **Nut Vault**, not to a team wallet.
3. **The Nut Vault converts fees** into the Stash (a fixed basket of stock tokens) and USDG.
4. **Every hour, the squirrel drops nutz.** Rewards are allocated to every holder based on time-weighted balance. Wallets above a small threshold get paid automatically; everyone else can claim any time.
5. **A slice goes to the Acorn Draw** — a weekly jackpot with one Golden Acorn winner plus Silver and Rain tiers whose winner counts grow with the holder base.

That's it. No staking. No lockups. No "utility roadmap." Hold the squirrel, get paid in nutz.

---

## 4. Token

| Item | Value |
|---|---|
| Name / Ticker | Nutz / NUTZ |
| Chain | Robinhood Chain (chain id 4663) |
| Launchpad | Pons v2 |
| Supply | Fixed at launch by the Pons launch config; no mint function |
| Quote asset | ETH (see Appendix A for why not a stock token) |
| Team allocation | **0**. The entire supply is minted to the bonding curve. |
| Dev buy | Small (≈0.3–0.5 ETH), disclosed at launch. Earns Stash and Cash drops like any holder at a fixed 1.0× (no Winter Mode bonus); permanently excluded from the Acorn Draw |
| Liquidity | 100% locked forever in a Uniswap v4 pool at graduation. No unlock function exists. |
| Creator fee recipient | The Nut Vault's Converter contract, set at creation. The Converter has no function to change it. Pons's protocol owner keeps a disclosed power to redirect any launch's fee recipient after a 3-day public notice; we monitor it on-chain and would announce it immediately (see 8) |
| Creator tax | Protocol maximum (`maxCreatorTaxBps` at launch), 100% to the Nut Vault |
| Operating cost | Self-funded: a 2% Ops slice pays protocol transactions; auto-push gas is deducted from the recipient's USDG. No team funding, ever |
| Buyback | Off — every fee dollar goes to holders, not to a creator vest |

**Why Pons.** Pons is where Robinhood Chain's attention is (~59% of launchpad volume, $500M+ daily at peak). Its curve-to-locked-pool flow means there is nothing for the team to rug: no liquidity to pull, no mint, no tax that can be raised later. The only thing the creator controls after launch is *where the fees go* — and we point that at a contract whose rules you can read and which cannot point them anywhere else.

---

## 5. The Nut Vault

The Nut Vault is $NUTZ's rewards engine: a set of open-source contracts on Robinhood Chain (a Converter, a Distributor and a Draw contract) that receives 100% of creator fees and pays them back out to holders.

### 5.1 Inflows

- Creator share of the standard Pons trading fee (charged on every buy and sell, on the curve and in the pool).
- The creator tax (set to the maximum the protocol allows).
- Anything anyone chooses to donate. (Deposits are open.)

Fees accrue in ETH on the bonding curve. After graduation part of the pool fees accrue in NUTZ and are converted to ETH by Pons before the vault can collect them. The vault claims whatever has been credited to it, every hour.

### 5.2 The split

| Share | Destination | What holders receive |
|---|---|---|
| **60%** | **The Stash** | Stock tokens: SPY, NVDA, MU, SPCX in equal weight |
| **28%** | **Cash** | USDG stablecoin |
| **10%** | **Acorn Draw** | Weekly jackpot: 1 Golden Acorn + scaling Silver and Rain tiers |
| **2%** | **Ops** | Kept in ETH to pay the system's own transactions (see 5.6). Hard-capped; overflow returns to holders |

**Dividends reinvest automatically.** Robinhood's Stock Tokens carry a built-in corporate-action multiplier: when SPY pays a dividend or NVDA splits, the multiplier rises and every token already in your wallet is worth more underlying shares. Nothing to claim, nothing to do. The nutz the squirrel dropped last month keep compounding on their own.

**Why stocks *and* cash.** Stock tokens are the flex ("a squirrel bought me Nvidia"). Cash is the proof ("it actually hit my wallet"). Together they answer the complaint every dividend meme gets — *where's the money?* — with a screenshot.

**Why this basket.** SPY is the market. NVDA is AI. MU is memory — the trade of 2026, and the best fit for a squirrel that stores things. SPCX is space. Four tickers, four stories, and it still fits in a tweet. The basket is fixed at launch and cannot be changed by anyone.

On-chain liquidity verified Sept 9, 2026 (deepest Robinhood Chain pool per Stock Token): NVDA $8.4M, SPCX $3.4M, MU $1.8M; SPY is the most-traded Stock Token on the chain. The vault buys directly on Uniswap (v3 pools first, v4 as backup) with a hard slippage cap, and the amounts are hourly, so purchases never move a pool meaningfully. If a stock's swap fails, or Robinhood pauses that token, that hour's share is paid out in USDG instead; nothing waits inside the vault.

### 5.3 The drop (distribution mechanics)

- **Epochs are hourly.** At the end of each epoch, the vault's indexer computes every wallet's **time-weighted average balance (TWAB)** across the hour. Buying at minute 59 earns 1/60th of a full hour. Selling at minute 1 earns 1/60th. Flash-holding earns nothing.
- **Allocations are pro-rata to TWAB**, multiplied by the streak bonus (5.4).
- **Payout is claim-based with auto-push.** Each epoch publishes a Merkle root of allocations on-chain. Any holder can claim any time, paying their own gas. A keeper automatically pushes rewards to every wallet whose pending balance is large enough, deducting the actual gas cost from that wallet's USDG reward (see 5.6), so most people never click anything and the system never needs outside money.
- **No minimum bag.** One $NUTZ earns its share.
- **Excluded from rewards:** the Uniswap pool, the Pons locker and buyback vault, the Nut Vault itself, and any centralized-exchange deposit address the community flags.
- **The dev-buy wallet** is a public, named address. It earns the Stash and Cash drops at a hard-coded 1.0× (it can never earn the Winter Mode bonus) and is hard-coded out of the Acorn Draw. Its balance and its cumulative rewards are shown on the dashboard.

Everything behind a payout is reproducible from public chain data. The payout rules ship as an open-source **verifier**: a small tool that recomputes any hour's allocations from the chain and confirms they match the root that was posted. Anyone can run it. If the team disappears, the vault keeps its balance, claims keep working, and the community can rebuild distribution from the public verifier.

### 5.4 Winter Mode (streak bonus)

Squirrels get rewarded for not touching the stash.

| Held without selling | Multiplier |
|---|---|
| 0–6 days | 1.0× |
| 7–29 days | 1.25× |
| 30+ days | 1.5× |

Any outgoing transfer resets the streak to zero: selling, sending to another wallet, or burning. Adding to a position does not. Multipliers apply to the Stash and Cash pools; the Acorn Draw uses tickets instead (5.5).

### 5.5 The Acorn Draw (jackpot)

Every week, 10% of that week's vault inflow becomes the **Acorn pool**, paid out in stock tokens to randomly selected holders in three tiers. The number of winners grows automatically with the number of holders, but a prize floor keeps every prize worth posting about.

| Tier | Share of pool | Winners | Prize floor |
|---|---|---|---|
| **Golden Acorn** | 50% | Always 1 | $2,500 (rolls over if not met) |
| **Silver Acorns** | 30% | max(3, holders ÷ 1,000) | $250 |
| **Acorn Rain** | 20% | max(10, holders ÷ 100) | $25 |

- **Prize floor:** if the pool can't fund the formula at the floor, the winner count in that tier shrinks until it can. Prizes never dilute into dust.
- **Roll-over:** if the Golden Acorn would fall below its floor, it rolls into next week's pool. A growing acorn is better content than a small winner.
- **Tickets, not weight:** one ticket per 1,000 $NUTZ of weekly TWAB, capped at the equivalent of 1% of supply per wallet. Winter Mode (30+ days) doubles a wallet's tickets. Small holders have a real shot; whales can't farm it.
- **Verifiable randomness:** winners are selected from drand, the public randomness beacon run by the League of Entropy since 2019; the ticket list is committed on-chain before the beacon value exists, the beacon signature is verified on-chain, and the seed, the ticket list and the result are all public.
- **The draw is an event:** the dashboard shows a countdown, then the winning wallets; an X bot posts every win.
- **Excluded:** the dev-buy wallet and all addresses excluded from regular rewards.

Worked: 500 holders and a $12k pool pays 1 × $6,000, 3 × $1,200 and 10 × $240. At 50,000 holders and a $300k pool it pays 1 × $150,000, 50 × $1,800 and 500 × $120 — the ladder widens by itself.

If the token grows large, a daily Acorn Rain can be added alongside the weekly Golden Acorn. Cadence is a configuration change, not a redesign.

### 5.6 Self-funding (who pays the gas)

NUTZ must run with zero external funding, forever. Two mechanisms cover its only cost, transaction gas:

- **Ops slice (2% of inflows).** Kept in ETH, never swapped. It pays the transactions the system itself must send: hourly sweep and swaps, hourly root posting, the weekly randomness request and draw root — roughly 25–30 transactions a day regardless of holder count. The ops balance is hard-capped (initially 0.5 ETH); anything above the cap automatically flows into the next drop. The balance is public on the dashboard.
- **Recipient-paid auto-push.** Pushing rewards to thousands of wallets scales with holders, so it can't come from a fixed slice. When the keeper pushes a wallet's rewards, it deducts the actual gas cost of that transfer from the wallet's USDG reward and reimburses the ops wallet (in USDG, which the ops wallet periodically swaps back to ETH). A wallet is pushed only when the deduction is at most 5% of its payout; smaller balances accumulate until they qualify. Every deduction is itemized on the dashboard. Holders who prefer can claim manually at any time and pay gas from their own wallet instead.

Net effect: holders receive 98% of all fees, minus their own postage on pushes. No treasury, no team subsidy, and nothing that stops working if the team disappears.

---

## 6. Worked example (illustrative only)

Assume: $1,000,000 daily volume; the Pons base fee of 1.0% per trade with 70% reaching the creator side after the protocol take (both confirmed on-chain); creator tax assumed at 1.0% `[the protocol cap is read from the factory at launch; if higher, vault inflow rises accordingly]`.

- Base fee to creator side: $1,000,000 × 1.0% × 70% ≈ **$7,000/day**
- Creator tax: $1,000,000 × 1.0% ≈ **$10,000/day**
- **Vault inflow ≈ $17,000/day**, of which ≈ $10,200 buys the Stash, ≈ $4,760 is USDG, ≈ $1,700 accrues to the Acorn Draw pool (≈ $12k per week), and ≈ $340 covers the system's own gas.

A wallet holding 0.1% of supply at 1.0× would receive about **$15/day** in stock tokens and USDG; at 1.5× (30+ day streak) about **$22/day**. At $10M daily volume the numbers are 10× that. At $50k daily volume they are 1/20th. The vault pays what the market trades — it never pays from a treasury, because there is no treasury.

---

## 7. Launch

- **Pre-launch (T-5 to T-1 days):** site, X, Telegram live; Nut Vault contracts deployed and verified; verifier repo public; contract address announced only via nutz.wtf and @stacknutz.
- **Launch:** `launchAndBuy` in one transaction (creator dev buy cannot be front-run). Named team/community wallets exempted from the opening snipe tax so the first minutes aren't a bot race.
- **Curve:** 4.2 ETH graduation threshold (confirmed on recent ETH-quoted launches). Rewards begin from the first hour, on the curve — not after graduation.
- **Graduation:** liquidity permanently locked in Uniswap v4. Nothing changes for holders.
- **First Acorn Draw:** end of launch week.

The team's launch commitment: **zero allocation, zero fee take, one disclosed dev wallet that earns exactly what any holder earns (no bonus, no jackpot), everything public.**

---

## 8. What you're actually getting (read this)

- **Stock Tokens on Robinhood Chain are not shares.** They are tokens issued by a Robinhood entity that track the price of the underlying stock. You do not get voting rights. You do get price exposure and the ability to trade or move them on-chain. They trade permissionlessly on Uniswap: anyone with a wallet can buy, sell or hold them, with no Robinhood account. What depends on where you live is whether you can redeem them *through Robinhood* (currently its EU customers only) and whether your local rules allow you to hold them at all. For most holders, the on-chain market is the only exit. **Know your local rules.**
- **$NUTZ has no intrinsic value.** It is a meme. Its price can go to zero. The rewards are a share of trading fees, and if nobody trades, nobody gets paid.
- **The Nut Vault is code.** The contracts and the verifier are open-source, reviewed before launch with public security tooling and invariant tests, and covered by a public bug bounty from day one. An external audit is a post-launch item, funded as the token grows, and will be published when complete. Code has bugs; the vault is built so that a bug can only ever touch one hour of fees, because it sweeps and distributes every hour and never holds a treasury.
- **Nobody controls your tokens.** No mint, no freeze, no blacklist, no unlockable liquidity, no way for anyone to withdraw from the vault. The only admin actions in the whole system require 2-of-3 signers and are publicly visible on-chain: posting or voiding an hour's payout root (capped by what that hour actually earned), a circuit breaker that can only reroute a stock leg into USDG if Robinhood pauses or blocks that token (re-enabling is timelocked 48h), appending exchange deposit addresses to the exclusion list (48h), rotating signers (48h), and the pointer to the draw contract (48h). None of them can move value away from holders. One thing sits outside our control and you should know it: Pons, the launchpad, keeps a protocol-level power to redirect any launch's creator fees to a new address after a 3-day public notice. Our Converter has no function to change the recipient itself, we watch for that notice on-chain, and we would announce it the moment it appeared. Fees already credited to the vault cannot be taken back.
- **Not financial advice. Not affiliated with Robinhood, Pons, or any company in the Stash.** The squirrel is a meme. Please don't sue the squirrel.

---

## 9. Roadmap (short on purpose)

1. **Launch.** Curve, graduation, first drops.
2. **Dashboard.** Live vault balance, next drop countdown, your pending nutz, Acorn Draw history and countdown.
3. **Nutz Generator.** PFP and meme generator (Tornado Squirrel + Kinda Chic captions) so holders can post their drops.
4. **Secondary stock-paired pool.** If the community wants it, a NUTZ/SPCX or NUTZ/NVDA Uniswap pool after graduation puts NUTZ in the stock-paired screeners without gating the launch.
5. **Basket changes:** none. The Stash is fixed forever.

No token 2.0. No "ecosystem." No partnerships announced by the squirrel.

---

## Appendix A — Stock-quoted launch (alternative)

Pons v2 allows a launch to be quoted in a tokenized stock instead of ETH. A NUTZ/NVDA or NUTZ/SPCX launch would appear under the "Stocks" filter and would pay creator fees directly in that stock token. Decided against: it puts an extra swap in front of every buyer (fewer holders for a coin whose point is holders), a successful pool can end up owning a large share of a Stock Token's on-chain float (one memecoin absorbed over half of HIMS and distorted its price — a regulatory magnet), the graduation target moves with the stock, and it makes NUTZ read as "the X coin" when its story is a four-stock basket. If the community wants stock-paired exposure later, anyone can open a secondary NUTZ/stock pool on Uniswap after graduation; that is on the roadmap as an option, not a launch decision.

## Appendix B — Open decisions

| # | Decision | Default | Owner |
|---|---|---|---|
| 1 | Launchpad (Pons vs Flap) | Pons v2 | **Decided** |
| 2 | Quote asset (ETH vs stock token) | ETH | **Decided** |
| 3 | Split (Stash / Cash / Acorn Draw / Ops) | 60 / 28 / 10 / 2 | **Decided** |
| 3b | Gas model | 2% Ops slice + recipient-paid auto-push | **Decided** |
| 4 | Basket composition | SPY, NVDA, MU, SPCX (liquidity verified Sept 9) | **Decided** |
| 5 | Buyback on/off | Off | **Decided** |
| 6 | Epoch length | 1 hour | **Decided** |
| 6b | Distribution model | Merkle-verifiable from day one | **Decided** |
| 6c | Dev wallet multiplier | Flat 1.0×, no Acorn Draw | **Decided** |
| 7 | Streak tiers | 7d 1.25× / 30d 1.5× | Eduar |
| 7b | Acorn Draw prize floors and scaling ratios | $2.5k / $250 / $25; ÷1,000 and ÷100 | Eduar |
| 8 | Basket fixed forever vs governable | Fixed forever | **Decided** |
| 9 | Audit timing | Self-review + tooling + bounty at launch; external audit post-launch | **Decided** |
| 10 | Jurisdictional handling of Stock Token rewards | Pending legal review | Legal |

## Appendix C — Glossary

- **Nut Vault** — the set of open-source contracts (Converter, Distributor, Draw) that receives 100% of creator fees and pays holders.
- **Epoch** — one hourly accounting period; holdings are measured and rewards allocated per epoch.
- **The Stash** — the fixed stock-token basket (SPY, NVDA, MU, SPCX).
- **The Drop** — the hourly distribution.
- **Winter Mode** — the streak multiplier for not selling.
- **Acorn Draw** — the weekly jackpot; Golden (1 winner), Silver and Rain tiers scale with holders.
- **TWAB** — time-weighted average balance; how much you held, for how long, within an epoch.
- **USDG** — Global Dollar, the regulated stablecoin native to Robinhood Chain.
- **Ops** — the 2% slice, held in ETH, that pays the system's own transactions.

---

*Draft v0.2. Not a prospectus, not an offer, not advice. Verify everything on-chain; the contract address is the only identifier that can't be faked.*
