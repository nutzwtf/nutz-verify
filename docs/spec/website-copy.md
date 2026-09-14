# nutz.wtf — Website Copy (FE handover)

**v0.1 · September 2026 · Voice: Tornado Squirrel + "Kinda chic to…" · Language: EN (ES to follow)**

## Notes for FE

- Single-page landing plus `/dashboard` (app). This doc covers the landing page, the dashboard's static strings, and meta/social.
- `{{…}}` are live values from the API (`GET /v1/vault`, `GET /v1/account/:addr`). Never hard-code numbers.
- `[[CONTRACT]]` is the NUTZ token address; `[[CONVERTER]]` the Converter; `[[DISTRIBUTOR]]` the Distributor (together with the Draw contract they are the Nut Vault). Render every address in full with a copy button and an explorer link — addresses are the only anti-counterfeit signal we have.
- Tone: short lines, lowercase-friendly, no exclamation marks except where marked. The squirrel is calm. The storm is behind him. Never "🚀", never "100x", never "guaranteed".
- Don't use any Superman / Man of Steel imagery or references. Our squirrel is original art.
- All numbers in copy below are illustrative placeholders; the FE renders live values.
- Keep every disclaimer string verbatim; legal may edit them, nobody else.

---

## 0. Meta

- **Title:** NUTZ — the squirrel that pays you
- **Description:** A memecoin on Robinhood Chain that drops tokenized stocks and USDG to holders every hour. 98% of fees go to holders. Zero team allocation.
- **OG title:** Kinda chic to get paid in nutz.
- **OG description:** Hold $NUTZ. Every hour the squirrel drops SPY, NVDA, MU, SPCX and cash into your wallet.
- **OG image:** squirrel waving, tornado behind, ticker overlay "$NUTZ · nutz.wtf"
- **Twitter card:** summary_large_image · @stacknutz

---

## 1. Nav

`NUTZ` (logo) · How it works · The Stash · The Draw · Verify · **Dashboard** (button) · X · Telegram

---

## 2. Hero

**Eyebrow:** Robinhood Chain · $NUTZ

**H1:** The squirrel that pays you.

**Sub:** Hold $NUTZ. Every hour, the squirrel drops tokenized stocks and cash straight into your wallet. No staking. No claiming. No team cut.

**CTA primary:** Buy on Pons →
**CTA secondary:** See the vault

**Live strip (three tiles):**
- Dropped to holders so far — `{{lifetime_distributed_usd}}`
- Next drop in — `{{next_epoch_countdown}}`
- Holders — `{{holder_count}}`

**Micro-line under strip:** Contract `[[CONTRACT]]` · the only thing that can't be faked.

---

## 3. Kinda chic (ticker band)

A slow horizontal marquee of captions (loop):

- Kinda chic to own Nvidia because of a squirrel.
- Kinda chic to have a retirement plan at 23.
- Kinda chic to get a paycheck every hour and do nothing.
- Kinda chic to hold through the tornado.
- Kinda chic to get paid in nutz.

---

## 4. How it works

**H2:** Hold the squirrel, get paid in nutz.

Four cards, numbered:

**01 · You buy $NUTZ**
On Pons, on Robinhood Chain. Bonding curve first, then a Uniswap pool with liquidity locked forever.

**02 · Every trade pays a fee**
Pons charges a fee on every buy and sell. The creator's share doesn't go to a team wallet. It goes to the Nut Vault.

**03 · The vault buys nutz**
Every hour it converts fees into the Stash — SPY, NVDA, MU, SPCX — plus USDG cash.

**04 · The squirrel drops**
Rewards land in every holder's wallet, sized by how much you hold and how long you've held it. Most wallets get paid automatically. Everyone can claim any time.

**Footnote line:** 98% of every fee goes to holders. 2% keeps the lights on: it pays the system's own gas so NUTZ runs forever without anyone funding it.

---

## 5. The Stash

**H2:** Four nutz. Fixed forever.

**Intro:** The squirrel doesn't diversify into 40 things. He stacks four, and he never changes his mind.

Four tiles:

- **SPY** — The market. The boring one. The ballast.
- **NVDA** — AI. The chip everyone wants.
- **MU** — Memory. The trade of 2026, and the one that fits a squirrel who stores things.
- **SPCX** — Space. The IPO everyone wanted.

**Callout:** Dividends reinvest themselves. When SPY pays a dividend or NVDA splits, Robinhood's stock tokens update a multiplier and every token already in your wallet is worth more shares. Nothing to claim. The nutz you got last month keep growing on their own.

**Small print:** Equal weight, fixed at launch, cannot be changed by anyone. Stock Tokens are issued by Robinhood; NUTZ is not affiliated with Robinhood or any company in the Stash.

---

## 6. The Drop

**H2:** Every hour. Every holder. No minimum.

Three columns:

**Time-weighted**
Rewards follow your time-weighted balance. Hold 1,000 $NUTZ for the whole hour, earn a full share. Buy at minute 59, earn 1/60th. Flash-holding earns nothing.

**Winter Mode**
Squirrels get rewarded for not touching the stash.
7+ days without selling — 1.25×
30+ days — 1.5×
Any outgoing transfer resets the streak — selling, sending, burning. Adding to your bag doesn't.

**Auto-push**
Once your pending rewards are big enough, the keeper sends them to your wallet and deducts only the actual gas from your USDG. Under the threshold? They accumulate. Prefer to claim yourself? Always available.

**Split bar (visual):** 60% Stash · 28% Cash (USDG) · 10% Acorn Draw · 2% Ops

---

## 7. The Acorn Draw

**H2:** One squirrel wins big every week. Then a lot more win.

**Intro:** 10% of every week's fees go into the Acorn pool and get dropped, in stock tokens, to randomly selected holders. The more holders there are, the more winners there are.

Three tiers:

- **Golden Acorn** — 50% of the pool. One winner. If it's below $2,500 it rolls over and grows.
- **Silver Acorns** — 30% of the pool. At least 3 winners, more as holders grow. Never below $250 each.
- **Acorn Rain** — 20% of the pool. At least 10 winners, more as holders grow. Never below $25 each.

**How tickets work:** One ticket per 1,000 $NUTZ held over the week, capped so whales can't farm it. 30-day holders get double tickets.

**Fairness line:** Winners come from drand, the public randomness beacon run by the League of Entropy since 2019. The ticket list is locked on-chain before the random number exists, and the number is verified on-chain. Anyone can check.

**Live tiles:** Next draw in `{{next_draw_countdown}}` · Current Acorn pool `{{acorn_pool_usd}}` · Last Golden Acorn `{{last_golden_amount}}` → `{{last_golden_wallet_short}}`

**Dev wallet note (small):** The team's dev-buy wallet earns hourly drops like any holder, at a flat 1.0×, and is permanently excluded from the Draw.

---

## 8. Verify

**H2:** Don't trust the squirrel. Check him.

Three cards:

**Contracts are open source**
The Nut Vault contracts — Converter (`[[CONVERTER]]`) and Distributor (`[[DISTRIBUTOR]]`) — are verified on Blockscout. No owner. No mint. No pause on claims. No withdraw. No unlockable liquidity.
→ View on explorer · → GitHub

**Every drop is verifiable**
Each hour publishes a Merkle root of who earned what. `nutz-verify` recomputes any hour from public chain data and tells you if it matches. Run it yourself.
→ GitHub · → Artifacts

**No treasury to rug**
The vault holds at most one hour of fees before distributing. Liquidity is locked in Uniswap forever. The few admin switches need 2-of-3 signers, are public, and none can move value away from holders. Pons itself keeps a 3-day-notice power over where any launch's fees go; we watch it on-chain and would say so immediately.
→ Read the whitepaper

---

## 9. FAQ

**Do I need a Robinhood account?**
No. $NUTZ and the stock tokens it pays are ERC-20s on Robinhood Chain. A wallet and some ETH is all you need.

**Are these real shares?**
They're Robinhood Stock Tokens: tokens issued by a Robinhood entity that track the stock's price. You get price exposure and can trade or hold them on-chain. You don't get voting rights. Redeeming through Robinhood depends on where you live; for most holders, the on-chain market is the exit. Know your local rules.

**Where does the money come from?**
Trading fees. If people trade, holders get paid. If nobody trades, nobody gets paid. There is no treasury.

**What's the team's cut?**
Zero allocation, zero fee take. One public dev wallet that earns like any holder, with no bonus and no jackpot.

**Is it audited?**
Contracts and the verifier are open source, reviewed with public security tooling and invariant tests, with a live bug bounty. An external audit is a post-launch item and will be published.

**Why 2% ops?**
Gas. Sweeping, converting and publishing every hour costs transactions. The 2% pays for them so the system needs no outside money. It's capped; anything above the cap flows back to holders.

**Can the basket change?**
No. SPY, NVDA, MU, SPCX, equal weight, forever.

**What if I move tokens between my own wallets?**
That counts as a sell for the sending wallet's streak. Keep your bag where it is if you want Winter Mode.

**Is this financial advice?**
No. $NUTZ is a meme. It can go to zero. Please don't sue the squirrel.

---

## 10. Footer

`$NUTZ` · nutz.wtf · @stacknutz · Telegram · GitHub · Whitepaper (EN / ES) · Dashboard

**Disclaimer (verbatim):** $NUTZ is a memecoin with no intrinsic value. Rewards are a share of trading fees and are not guaranteed. Stock Tokens are issued by Robinhood Assets (Jersey) Limited and are not shares; eligibility to hold or redeem them depends on your jurisdiction. NUTZ is not affiliated with, endorsed by, or connected to Robinhood, Pons, or any company referenced in the Stash. Nothing here is financial, legal or tax advice. Verify every address on-chain.

**Address block:** Token `[[CONTRACT]]` · Converter `[[CONVERTER]]` · Distributor `[[DISTRIBUTOR]]`

---

## 11. Dashboard strings (`/dashboard`)

**Header:** Your nutz

**Connect state:** Connect a wallet to see your drops. → Connect

**Overview tiles:**
- Your $NUTZ — `{{balance}}`
- This hour (time-weighted) — `{{twab_live}}`
- Streak — `{{streak_days}} days · {{multiplier}}×`  (tooltip: "Winter Mode: 7+ days 1.25×, 30+ days 1.5×. Any sell resets.")
- Pending — `{{pending_usd}}` (expand: per token, raw + shares, USDG)
- Lifetime received — `{{lifetime_usd}}`

**Push status line (one of):**
- "Auto-push: eligible. Next push after the current drop." 
- "Accumulating: pending rewards are under the auto-push threshold. They're yours; claim any time."

**Claim button:** Claim all `({{claimable_epochs}} hours)` · Sub: "You pay gas for a manual claim."

**Fees itemization table header:** Push fees paid (gas, deducted from USDG) — date · epoch · fee

**Stuck rewards (only if > 0):** "Some stock tokens couldn't be delivered (token paused or transfer blocked). They're held for you. Try again → Claim stuck"

**Vault panel:** Vault balance `{{vault_eth}}` ETH pending · Ops wallet `{{ops_eth}} / {{ops_cap}}` ETH · Last drop `{{last_epoch_time}}` · Next drop `{{next_epoch_countdown}}` · Verify status `{{last_verify_result}}` ("MATCH ✓" / "MISMATCH — under review")

**Draw panel:** Next draw `{{next_draw_countdown}}` · Your tickets `{{tickets}}` · Acorn pool `{{acorn_pool_usd}}` · Past winners (table: draw · tier · wallet · prize)

**Empty state:** "No drops yet. The squirrel is stacking."

**Error state:** "Couldn't reach the API. Your rewards are on-chain regardless — claim directly from the contract if you need to."

---

## 12. X bot strings (for reference)

- Hourly: "🌰 The squirrel dropped `{{epoch_usd}}` this hour: `{{spy}}` SPY · `{{nvda}}` NVDA · `{{mu}}` MU · `{{spcx}}` SPCX · `{{usdg}}` USDG to `{{n_wallets}}` wallets. Kinda chic."
- Draw: "🌰 Golden Acorn: `{{wallet_short}}` just got `{{prize}}` in stock tokens for holding a squirrel. `{{silver_n}}` Silver, `{{rain_n}}` Rain. Next draw in 7 days."
- Rollover: "🌰 No Golden Acorn this week. The acorn is now `{{rolled_amount}}`. Hold."
