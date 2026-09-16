// Generates the testdata/merkle fixtures internal/merkle is pinned against, using the
// library the Go port is a port of. Run: pnpm gen:fixtures (deterministic; commit output).
//
// claims.json is not generated here — it is copied verbatim from nutz-contracts.
import { StandardMerkleTree } from "@openzeppelin/merkle-tree";
import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ENCODING = ["uint256", "address", "uint256[5]"];
const MAX_UINT256 = 2n ** 256n - 1n;

type Claim = { account: `0x${string}`; amounts: bigint[] };
type Fixture = { name: string; note: string; id: bigint; claims: Claim[]; reverseInput?: boolean };

/** Repeats a nibble into an address: addr("a") -> 0xaaaa...aa. */
const addr = (nibble: string): `0x${string}` => `0x${nibble.repeat(40)}`;

const ether = (n: bigint) => n * 10n ** 18n;

const FIXTURES: Fixture[] = [
  {
    name: "single-claim",
    note: "One leaf: the tree is the leaf, the root is the leaf, and the proof is empty.",
    id: 1n,
    claims: [{ account: addr("a"), amounts: [ether(1n), 0n, 0n, 0n, 0n] }],
  },
  {
    name: "two-claims",
    note: "The smallest tree with a real proof: one internal node, one sibling each.",
    id: 2n,
    claims: [
      { account: addr("b"), amounts: [1n, 0n, 0n, 0n, 0n] },
      { account: addr("c"), amounts: [0n, 0n, 0n, 0n, 1n] },
    ],
  },
  {
    name: "odd-claims",
    note:
      "Five leaves, so the complete-binary-tree layout is unbalanced and proof lengths " +
      "differ between claims. Includes 0 and 2**256-1 to exercise the 224-byte serialiser.",
    id: 500001n,
    claims: [
      { account: addr("1"), amounts: [MAX_UINT256, 0n, 0n, 0n, 0n] },
      { account: addr("2"), amounts: [0n, 0n, 0n, 0n, 0n] },
      { account: addr("3"), amounts: [1n, 1n, 1n, 1n, 1n] },
      { account: addr("4"), amounts: [0n, MAX_UINT256, 0n, ether(3n), 0n] },
      { account: addr("5"), amounts: [ether(9n), 0n, 7n, 0n, MAX_UINT256] },
    ],
  },
  {
    name: "duplicate-amounts",
    note:
      "Four accounts sharing one amount vector: leaves stay distinct because the account " +
      "is part of the preimage. A serialiser that dropped or misplaced the account word " +
      "would collapse these into one leaf.",
    id: 500002n,
    claims: [addr("7"), addr("8"), addr("9"), addr("d")].map((account) => ({
      account,
      amounts: [ether(5n), 0n, 0n, ether(5n), 42n],
    })),
  },
  {
    name: "unsorted-input",
    note:
      "The sortLeaves landmine. Claims are written in the exact reverse of their leaf-hash " +
      "order, so an implementation that skipped the sort — or sorted by input order, or " +
      "descending — builds a valid-looking tree with a different root.",
    id: 500003n,
    reverseInput: true,
    claims: [
      { account: addr("1"), amounts: [ether(1n), 0n, 0n, 0n, 0n] },
      { account: addr("2"), amounts: [0n, ether(2n), 0n, 0n, 0n] },
      { account: addr("3"), amounts: [0n, 0n, ether(3n), 0n, 0n] },
      { account: addr("4"), amounts: [0n, 0n, 0n, ether(4n), 0n] },
      { account: addr("5"), amounts: [0n, 0n, 0n, 0n, ether(5n)] },
      { account: addr("6"), amounts: [6n, 6n, 6n, 6n, 6n] },
    ],
  },
];

const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, "..", "..", "testdata", "merkle");
mkdirSync(outDir, { recursive: true });

for (const fixture of FIXTURES) {
  const row = (c: Claim) => [fixture.id, c.account, c.amounts] as const;
  const leafHash = (c: Claim) => StandardMerkleTree.of([row(c)], ENCODING).leafHash(row(c));

  let claims = fixture.claims;
  if (fixture.reverseInput) {
    // Make input order the exact opposite of the order StandardMerkleTree will impose.
    claims = [...claims].sort((a, b) => (leafHash(a) < leafHash(b) ? -1 : 1)).reverse();
  }

  const tree = StandardMerkleTree.of(claims.map(row), ENCODING);

  // The dump the Go port must reproduce (merkle.Tree.Dump). Built from string inputs,
  // which is how a Bundle's tree.json is written and the only form JSON.stringify can
  // carry — a BigInt has no JSON form. Same leaves, so the same root, asserted.
  const stringRow = (c: Claim) =>
    [fixture.id.toString(), c.account, c.amounts.map((a) => a.toString())] as const;
  const dumped = StandardMerkleTree.of(claims.map(stringRow), ENCODING);
  if (dumped.root !== tree.root) {
    throw new Error(`${fixture.name}: string-valued tree has root ${dumped.root}, bigint-valued ${tree.root}`);
  }

  const out = {
    name: fixture.name,
    note: fixture.note,
    encoding: ENCODING,
    id: fixture.id.toString(),
    root: tree.root,
    claims: claims.map((c, i) => ({
      account: c.account,
      amounts: c.amounts.map((a) => a.toString()),
      proof: tree.getProof(i),
      leaf: tree.leafHash(row(c)),
    })),
    dump: dumped.dump(),
  };

  const target = join(outDir, `${fixture.name}.json`);
  writeFileSync(target, JSON.stringify(out, null, 2) + "\n");
  console.log(`${fixture.name.padEnd(18)} ${claims.length} claim(s)  root ${tree.root}`);
}
