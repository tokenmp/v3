import { readFile } from "node:fs/promises";
import path from "node:path";
import { openapiRoot, packageRoot, forbiddenTerms } from "../scripts/contract-helpers.mjs";
import { validateOpenAPI, collectRefs, resolvePointer } from "../scripts/validate.mjs";
import { describe, it, before } from "node:test";
import assert from "node:assert/strict";

const yamlPath = path.join(openapiRoot, "billing", "v1.yaml");

describe("Billing v1 OpenAPI contract", () => {
  let content;

  it("file exists and is readable", async () => {
    content = await readFile(yamlPath, "utf8");
    assert.ok(content.length > 0);
  });

  it("starts with openapi key", async () => {
    content ??= await readFile(yamlPath, "utf8");
    assert.ok(content.startsWith("openapi:"), "Must start with 'openapi:' key");
  });

  it("has no trailing whitespace", async () => {
    content ??= await readFile(yamlPath, "utf8");
    assert.ok(!/[ \t]+$/m.test(content), "No trailing whitespace");
  });

  it("ends with newline", async () => {
    content ??= await readFile(yamlPath, "utf8");
    assert.ok(content.endsWith("\n"), "Must end with newline");
  });

  it("has unique operationIds", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const ids = [...content.matchAll(/operationId:\s*(\S+)/g)].map((m) => m[1]);
    const unique = new Set(ids);
    assert.strictEqual(ids.length, unique.size, `Duplicate operationIds: ${ids.filter((id, i) => ids.indexOf(id) !== i)}`);
  });

  it("has required settlement endpoints", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const requiredPaths = [
      "/healthz",
      "/readyz",
      "/v1/billing/quota/reserve",
      "/v1/billing/quota/finalize",
      "/v1/billing/quota/release",
      "/v1/billing/quota/reservations/{reservationId}",
      "/v1/billing/quota/mark-pending",
      "/v1/billing/quota/reconcile",
    ];
    for (const p of requiredPaths) {
      assert.ok(content.includes(`  ${p}:`), `Missing path '${p}'`);
    }
  });

  it("does not contain forbidden internal implementation terms", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const lines = content.split("\n");
    for (let i = 0; i < lines.length; i++) {
      const lower = lines[i].toLowerCase();
      for (const term of forbiddenTerms) {
        if (lower.includes(term.toLowerCase())) {
          assert.fail(`line ${i + 1}: forbidden internal term '${term}' found`);
        }
      }
    }
  });

  it("passes shared OpenAPI lint + contract validation", async () => {
    const errors = await validateOpenAPI([yamlPath], "--contract");
    assert.deepStrictEqual(errors, [], `Validation errors:\n${errors.join("\n")}`);
  });

  it("resolves all internal $ref pointers", async () => {
    const raw = await readFile(yamlPath, "utf8");
    const YAML = (await import("yaml")).default;
    const doc = YAML.parse(raw, { strict: true });
    const refs = collectRefs(doc);
    for (const ref of refs) {
      if (!ref.startsWith("#/")) continue;
      const resolved = resolvePointer(doc, ref);
      assert.notStrictEqual(resolved, undefined, `Unresolved $ref '${ref}'`);
    }
  });

  it("reserve/finalize/reconcile schemas reject unknown fields", async () => {
    const raw = await readFile(yamlPath, "utf8");
    const YAML = (await import("yaml")).default;
    const doc = YAML.parse(raw, { strict: true });
    for (const name of ["ReserveRequest", "FinalizeRequest", "ReleaseRequest", "MarkPendingRequest", "ReconcileRequest", "ReservationStatus"]) {
      const schema = doc.components.schemas[name];
      assert.ok(schema, `missing schema ${name}`);
      assert.strictEqual(schema.additionalProperties, false, `${name} must set additionalProperties: false`);
    }
  });

  it("finalize/reconcile require usage_known", async () => {
    const raw = await readFile(yamlPath, "utf8");
    const YAML = (await import("yaml")).default;
    const doc = YAML.parse(raw, { strict: true });
    for (const name of ["FinalizeRequest", "ReconcileRequest"]) {
      const schema = doc.components.schemas[name];
      assert.ok(schema.required.includes("usage_known"), `${name} must require usage_known`);
    }
  });
});
