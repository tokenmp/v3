import { cp, mkdir, rm } from "node:fs/promises";
import path from "node:path";
import { openapiRoot, packageRoot } from "./contract-helpers.mjs";

const dist = path.join(packageRoot, "dist");
await rm(dist, { recursive: true, force: true });
await mkdir(dist, { recursive: true });
await cp(openapiRoot, path.join(dist, "openapi"), { recursive: true });
console.log("built OpenAPI contract distribution in dist/openapi/");
