import assert from "node:assert/strict";
import { access, readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { declarations, packageRoot, readSource, references, tokenSet } from "../scripts/token-contract.mjs";

const light = await readSource("semantic/light.css");
const dark = await readSource("semantic/dark.css");
const explicitDark = dark.split("@media")[0];
const systemDark = dark.slice(dark.indexOf("@media"));

function colorTokens(css) {
  return new Set([...tokenSet(css)].filter((name) => name.startsWith("--tmp-color-")));
}

function sorted(set) {
  return [...set].sort();
}

test("light, explicit dark, and system dark expose the same color contract", () => {
  assert.deepEqual(sorted(colorTokens(explicitDark)), sorted(colorTokens(light)));
  assert.deepEqual(sorted(colorTokens(systemDark)), sorted(colorTokens(light)));
});

test("all TokenMP references resolve to a core declaration", async () => {
  const coreFiles = [
    "reference/colors.css", "reference/typography.css", "reference/spacing.css",
    "reference/shape.css", "reference/effects.css", "semantic/base.css",
    "semantic/light.css", "semantic/dark.css",
  ];
  const core = (await Promise.all(coreFiles.map(readSource))).join("\n");
  const definitions = new Set(declarations(core).keys());
  for (const file of [...coreFiles, "integrations/tailwind.css", "integrations/shadcn.css"]) {
    for (const reference of references(await readSource(file))) {
      if (reference.startsWith("--tmp-")) assert.ok(definitions.has(reference), `${file}: ${reference}`);
    }
  }
});

test("integration files map tokens without owning raw color values", async () => {
  for (const file of ["integrations/tailwind.css", "integrations/shadcn.css"]) {
    const css = await readSource(file);
    assert.doesNotMatch(css, /oklch\(|#[0-9a-f]{3,8}/i);
    assert.ok(references(css).some((name) => name.startsWith("--tmp-")));
  }
});

test("previous TokenMP visual baseline remains locked", async () => {
  const colors = declarations(await readSource("reference/colors.css"));
  assert.equal(colors.get("--tmp-ref-color-neutral-950"), "oklch(0.145 0 0)");
  assert.equal(colors.get("--tmp-ref-color-blue-600"), "oklch(0.576 0.251 258)");
  assert.equal(colors.get("--tmp-ref-color-green-700"), "oklch(0.646 0.175 147)");
  assert.equal(colors.get("--tmp-ref-color-red-700"), "oklch(0.626 0.252 23)");
  assert.equal(colors.get("--tmp-ref-color-amber-700"), "oklch(0.819 0.197 76)");

  const shape = declarations(await readSource("reference/shape.css"));
  assert.equal(shape.get("--tmp-ref-radius-sm"), "0.375rem");
  assert.equal(shape.get("--tmp-ref-radius-lg"), "0.625rem");

  const effects = declarations(await readSource("reference/effects.css"));
  assert.equal(effects.get("--tmp-ref-duration-fast"), "150ms");
  assert.equal(effects.get("--tmp-ref-duration-normal"), "200ms");
  assert.equal(effects.get("--tmp-ref-duration-slow"), "300ms");
  assert.equal(effects.get("--tmp-ref-layer-toast"), "9999");
});

test("Tailwind integration preserves sidebar, ring offset, and accordion mappings", async () => {
  const tailwind = await readSource("integrations/tailwind.css");
  for (const token of [
    "--color-sidebar", "--color-sidebar-foreground", "--color-sidebar-primary",
    "--color-sidebar-primary-foreground", "--color-sidebar-accent",
    "--color-sidebar-accent-foreground", "--color-sidebar-border",
    "--color-sidebar-ring", "--color-ring-offset-background",
    "--animate-accordion-down", "--animate-accordion-up",
  ]) assert.ok(declarations(tailwind).has(token), token);
  assert.match(tailwind, /@keyframes accordion-down/);
  assert.match(tailwind, /@keyframes accordion-up/);
});

test("package exports and aggregate imports resolve after build", async () => {
  const manifest = JSON.parse(await readFile(path.join(packageRoot, "package.json"), "utf8"));
  assert.deepEqual(Object.keys(manifest.exports), [".", "./tailwind", "./shadcn"]);
  for (const target of Object.values(manifest.exports)) await access(path.join(packageRoot, target));

  const index = await readSource("index.css");
  for (const expected of [
    "reference/colors.css", "reference/typography.css", "reference/spacing.css",
    "reference/shape.css", "reference/effects.css", "semantic/base.css",
    "semantic/light.css", "semantic/dark.css",
  ]) assert.match(index, new RegExp(expected.replaceAll("/", "\\/")));
});
