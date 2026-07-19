import { cp, mkdir, rm } from "node:fs/promises";
import path from "node:path";
import { packageRoot, sourceRoot } from "./token-contract.mjs";

const dist = path.join(packageRoot, "dist");
await rm(dist, { recursive: true, force: true });
await mkdir(dist, { recursive: true });
await cp(sourceRoot, dist, { recursive: true });
console.log("built CSS token distribution in dist/");
