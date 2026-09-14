// Fills in expected.root for every Case in testdata/cases, using @openzeppelin/merkle-tree —
// the implementation ADR-0003 makes normative for tree shape. Run: pnpm gen:case-roots
// (deterministic; commit output).
//
// This hashes what a Case already declares and recomputes nothing: the allocations, totals
// and Carry are hand-authored data pinning the rules, which are normative in Go (ADR-0003).
// A generator that derived them would turn the Cases into a second rules implementation,
// free to drift from the first.
import { StandardMerkleTree } from "@openzeppelin/merkle-tree";
import { readFileSync, writeFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ENCODING = ["uint256", "address", "uint256[5]"];

type Allocation = { account: `0x${string}`; amounts: string[] };
type Case = {
  name: string;
  epochId: number;
  expected: { allocations: Allocation[]; root: string | null };
};

const here = dirname(fileURLToPath(import.meta.url));
const caseDir = join(here, "..", "..", "testdata", "cases");

for (const file of readdirSync(caseDir).filter((f) => f.endsWith(".json")).sort()) {
  const path = join(caseDir, file);
  const testCase: Case = JSON.parse(readFileSync(path, "utf8"));
  const { allocations } = testCase.expected;

  if (allocations.length === 0) {
    // A Skipped Epoch posts no Root, and a tree of no leaves has none to compute.
    testCase.expected.root = null;
  } else {
    const rows = allocations.map((a) => [
      BigInt(testCase.epochId),
      a.account,
      a.amounts.map(BigInt),
    ] as const);

    testCase.expected.root = StandardMerkleTree.of(rows, ENCODING).root;
  }

  writeFileSync(path, JSON.stringify(testCase, null, 2) + "\n");
  console.log(
    `${testCase.name.padEnd(32)} ${String(allocations.length).padStart(2)} leaf/leaves  ` +
      `root ${testCase.expected.root ?? "none (Skipped Epoch)"}`,
  );
}
