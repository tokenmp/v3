import { readFile } from "node:fs/promises";
import path from "node:path";
import { declarations, listCssFiles, packageRoot, references } from "./token-contract.mjs";

const mode = process.argv[2] ?? "--contract";
const files = await listCssFiles();
const contents = new Map(await Promise.all(files.map(async (file) => [file, await readFile(file, "utf8")])));
const errors = [];

for (const [file, css] of contents) {
  const relative = path.relative(packageRoot, file);
  if (/[ \t]+$/m.test(css)) errors.push(`${relative}: trailing whitespace`);
  if (!css.endsWith("\n")) errors.push(`${relative}: missing final newline`);
  const opens = [...css].filter((char) => char === "{").length;
  const closes = [...css].filter((char) => char === "}").length;
  if (opens !== closes) errors.push(`${relative}: unbalanced braces`);

  if (relative.includes(`${path.sep}reference${path.sep}`) || relative.includes(`${path.sep}semantic${path.sep}`)) {
    for (const name of declarations(css).keys()) {
      if (!name.startsWith("--tmp-")) errors.push(`${relative}: core token ${name} must use --tmp- prefix`);
    }
  }

  if (relative.includes(`${path.sep}integrations${path.sep}`) && /(?:oklch|#[0-9a-f]{3,8}|\b\d+(?:\.\d+)?rem\b)/i.test(css)) {
    errors.push(`${relative}: integration must alias tokens instead of defining design values`);
  }
}

if (mode === "--contract") {
  const allDefinitions = new Map([...contents.values()].flatMap((css) => [...declarations(css)]));
  for (const [file, css] of contents) {
    for (const reference of references(css)) {
      if (reference.startsWith("--tmp-") && !allDefinitions.has(reference)) {
        errors.push(`${path.relative(packageRoot, file)}: undefined reference ${reference}`);
      }
    }
  }

  const visit = (token, trail = []) => {
    if (trail.includes(token)) {
      errors.push(`circular token reference: ${[...trail, token].join(" -> ")}`);
      return;
    }
    const value = allDefinitions.get(token);
    if (!value) return;
    for (const reference of references(value)) visit(reference, [...trail, token]);
  };
  for (const token of allDefinitions.keys()) {
    if (token.startsWith("--tmp-")) visit(token);
  }
}

if (errors.length > 0) {
  console.error(errors.join("\n"));
  process.exitCode = 1;
} else {
  console.log(`${mode.slice(2)} validation passed for ${files.length} CSS files`);
}
