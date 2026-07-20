import { rm } from "node:fs/promises";
import path from "node:path";
import { packageRoot } from "./contract-helpers.mjs";

await rm(path.join(packageRoot, "dist"), { recursive: true, force: true });
