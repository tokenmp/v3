import { readFile } from "node:fs/promises";
import path from "node:path";
import { openapiRoot, packageRoot, forbiddenTerms } from "../scripts/contract-helpers.mjs";
import { describe, it } from "node:test";
import assert from "node:assert/strict";

const yamlPath = path.join(openapiRoot, "config", "v1.yaml");

describe("Config v1 OpenAPI contract", () => {
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

  it("has required endpoints", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const requiredPaths = [
      "/healthz",
      "/readyz",
      "/v1/config/snapshots/latest",
      "/v1/config/drafts",
      "/v1/config/drafts/{revisionId}",
      "/v1/config/revisions/{revisionId}/publish",
      "/v1/config/revisions/{revisionId}/archive",
      "/v1/config/revisions/{revisionId}/revert",
      "/v1/config/revisions",
      "/v1/config/audit",
    ];
    for (const p of requiredPaths) {
      assert.ok(content.includes(`  ${p}:`), `Missing path '${p}'`);
    }
  });

  it("does not contain forbidden internal implementation terms", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const lines = content.split("\n");
    const violations = [];
    for (let i = 0; i < lines.length; i++) {
      const lower = lines[i].toLowerCase();
      for (const term of forbiddenTerms) {
        if (lower.includes(term.toLowerCase())) {
          violations.push(`line ${i + 1}: '${term}'`);
        }
      }
    }
    assert.strictEqual(violations.length, 0, `Forbidden internal terms found:\n${violations.join("\n")}`);
  });

  it("uses uniform error envelope", async () => {
    content ??= await readFile(yamlPath, "utf8");
    assert.ok(content.includes("Error"), "Must define Error schema");
    assert.ok(content.includes("code:"), "Error schema must have code");
    assert.ok(content.includes("message:"), "Error schema must have message");
  });

  it("specifies Cache-Control: no-store on sensitive responses", async () => {
    content ??= await readFile(yamlPath, "utf8");
    assert.ok(content.includes("CacheControlNoStore"), "Must reference CacheControlNoStore header");
  });

  it("strict request schemas have additionalProperties=false", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const YAML = (await import("yaml")).default;
    const doc = YAML.parse(content, { strict: true });
    const schemas = doc.components.schemas;
    const strictSchemas = ["CreateDraftRequest", "DraftResult", "UpdateDraftResult", "PublishResult", "ArchiveResult", "RevertRequest", "RevertResult", "RevisionDetail", "RevisionSummary", "RevisionList", "AuditEntry", "AuditList", "Error", "HealthResponse"];
    for (const name of strictSchemas) {
      assert.ok(schemas[name], `Schema ${name} must exist`);
      assert.strictEqual(schemas[name].additionalProperties, false, `${name} must have additionalProperties: false`);
    }
  });

  it("never exposes a secret or plaintext key field", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const YAML = (await import("yaml")).default;
    const doc = YAML.parse(content, { strict: true });
    const forbiddenProps = /^(api_key|secret|password|token)$/i;
    const violations = [];
    const scan = (schema, name) => {
      if (!schema || typeof schema !== "object") return;
      if (schema.properties) {
        for (const prop of Object.keys(schema.properties)) {
          if (forbiddenProps.test(prop)) violations.push(`${name}.${prop}`);
        }
      }
    };
    if (doc.components?.schemas) {
      for (const [name, schema] of Object.entries(doc.components.schemas)) scan(schema, name);
    }
    assert.strictEqual(violations.length, 0, `Forbidden secret-like fields: ${violations.join(", ")}`);
    assert.ok(content.includes("vault://"), "Must document opaque vault:// credential references");
  });

  it("documents optimistic concurrency via If-Match and ETag", async () => {
    content ??= await readFile(yamlPath, "utf8");
    assert.ok(content.includes("IfMatch"), "Must define If-Match parameter");
    assert.ok(content.includes("ETag"), "Must define ETag header");
    assert.ok(content.includes("412"), "Must define a 412 precondition-failed response");
  });

  it("defines admin authorization security scheme", async () => {
    content ??= await readFile(yamlPath, "utf8");
    assert.ok(content.includes("AdminTokenAuth"), "Must define AdminTokenAuth security scheme");
    assert.ok(content.includes("X-Admin-Token"), "Must use X-Admin-Token header");
  });
});

describe("Config v1 OpenAPI build output", () => {
  it("dist/openapi/config/v1.yaml matches source", async () => {
    const source = await readFile(yamlPath, "utf8");
    const distPath = path.join(packageRoot, "dist", "openapi", "config", "v1.yaml");
    const distContent = await readFile(distPath, "utf8");
    assert.strictEqual(distContent, source, "dist must be an exact copy of source");
  });
});
