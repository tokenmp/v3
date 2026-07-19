import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const sourceRoot = path.join(packageRoot, "src");

export async function listCssFiles(directory = sourceRoot) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(entries.map((entry) => {
    const target = path.join(directory, entry.name);
    return entry.isDirectory() ? listCssFiles(target) : target;
  }));
  return files.flat().filter((file) => file.endsWith(".css")).sort();
}

export function declarations(css) {
  return new Map([...css.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;{}]+);/g)].map((match) => [match[1], match[2].trim()]));
}

export function references(css) {
  return [...css.matchAll(/var\((--[a-z0-9-]+)(?:\s*,[^)]*)?\)/g)].map((match) => match[1]);
}

export function tokenSet(css, prefix = "--tmp-") {
  return new Set([...declarations(css).keys()].filter((name) => name.startsWith(prefix)));
}

export async function readSource(relativePath) {
  return readFile(path.join(sourceRoot, relativePath), "utf8");
}
