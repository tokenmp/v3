import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import path from "node:path";
import { openapiRoot, packageRoot, forbiddenTerms } from "../scripts/contract-helpers.mjs";
import { validateOpenAPI, collectRefs, resolvePointer } from "../scripts/validate.mjs";
import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";

const yamlPath = path.join(openapiRoot, "auth", "v1.yaml");

describe("Auth v1 OpenAPI contract", () => {
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
      "/api/v1/auth/register",
      "/api/v1/auth/login",
      "/api/v1/auth/refresh",
      "/api/v1/auth/logout",
      "/api/v1/auth/logout-all",
      "/api/v1/auth/me",
      "/api/v1/auth/password",
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

  it("does not mention database, SQL or ORM details in descriptions", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const lines = content.split("\n");
    const violations = [];
    const forbiddenWithPattern = [
      { term: "sql", pattern: /\bsql\b/i },
      { term: "database", pattern: /\bdatabase\b/i },
      { term: "table", pattern: /\btable\b/i },
      { term: "column", pattern: /\bcolumn\b/i },
      { term: "index", pattern: /\bindex\b/i },
      { term: "migration", pattern: /\bmigration\b/i },
      { term: "orm", pattern: /\borm\b/i },
      { term: "gorm", pattern: /\bgorm\b/i },
      { term: "query", pattern: /\bquery\b/i },
      { term: "insert", pattern: /\binsert\b/i },
      { term: "update", pattern: /\bupdate\b/i },
      { term: "delete", pattern: /\bdelete\b/i },
    ];
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      for (const { term, pattern } of forbiddenWithPattern) {
        if (pattern.test(line)) {
          violations.push(`line ${i + 1}: '${term}'`);
        }
      }
    }
    assert.strictEqual(violations.length, 0, `Forbidden database/SQL terms found:\n${violations.join("\n")}`);
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

  it("strict schemas have additionalProperties=false; LogoutRequest does not", async () => {
    content ??= await readFile(yamlPath, "utf8");
    const YAML = (await import("yaml")).default;
    const doc = YAML.parse(content, { strict: true });
    const schemas = doc.components.schemas;

    // Four strict request schemas must have additionalProperties: false
    const strictSchemas = ["RegisterRequest", "LoginRequest", "RefreshRequest", "ChangePasswordRequest"];
    for (const name of strictSchemas) {
      assert.ok(schemas[name], `Schema ${name} must exist`);
      assert.strictEqual(schemas[name].additionalProperties, false, `${name} must have additionalProperties: false`);
    }

    // LogoutRequest must exist as an independent schema (not RefreshRequest)
    assert.ok(schemas.LogoutRequest, "LogoutRequest schema must exist");
    // LogoutRequest must NOT have additionalProperties: false (default allows extra fields)
    assert.strictEqual(schemas.LogoutRequest.additionalProperties, undefined, "LogoutRequest must not set additionalProperties: false");

    // Logout requestBody must reference LogoutRequest, not RefreshRequest
    const logoutBody = doc.paths["/api/v1/auth/logout"].post.requestBody.content["application/json"].schema;
    assert.strictEqual(logoutBody.$ref, "#/components/schemas/LogoutRequest", "Logout requestBody must reference LogoutRequest");
  });
});

describe("Auth v1 OpenAPI build output", () => {
  it("dist/openapi/auth/v1.yaml matches source", async () => {
    const source = await readFile(yamlPath, "utf8");
    const distPath = path.join(packageRoot, "dist", "openapi", "auth", "v1.yaml");
    const distContent = await readFile(distPath, "utf8");
    assert.strictEqual(distContent, source, "dist must be an exact copy of source");
  });
});

describe("Malformed YAML detection", () => {
  const tmpDir = path.join(packageRoot, "tests", "__tmp_malformed__");

  after(async () => {
    await rm(tmpDir, { recursive: true, force: true });
  });

  it("rejects YAML with broken indentation", async () => {
    const brokenYaml = `openapi: 3.0.3\ninfo:\n   title: Test\n  version: 0.1.0\npaths:\n  /foo:\n    get:\n      operationId: testOp\n`;
    const tmpFile = path.join(tmpDir, "broken-indent.yaml");
    await mkdir(tmpDir, { recursive: true });
    await writeFile(tmpFile, brokenYaml, "utf8");
    const errors = await validateOpenAPI([tmpFile], "--lint");
    assert.ok(errors.length > 0, `Expected errors for broken indentation, got none`);
    assert.ok(errors.some((e) => /YAML parse error/i.test(e) || /indentation/i.test(e)), `Expected indentation/parse error, got: ${errors.join("; ")}`);
  });

  it("rejects YAML with syntax error (unclosed bracket)", async () => {
    const brokenYaml = `openapi: 3.0.3\ninfo:\n  title: Test\n  version: 0.1.0\npaths:\n  /foo: [\n`;
    const tmpFile = path.join(tmpDir, "broken-syntax.yaml");
    await mkdir(tmpDir, { recursive: true });
    await writeFile(tmpFile, brokenYaml, "utf8");
    const errors = await validateOpenAPI([tmpFile], "--lint");
    assert.ok(errors.length > 0, `Expected errors for syntax error, got none`);
    assert.ok(errors.some((e) => /YAML parse error/i.test(e)), `Expected YAML parse error, got: ${errors.join("; ")}`);
  });

  it("rejects YAML with tab indentation", async () => {
    const brokenYaml = `openapi: 3.0.3\ninfo:\n\ttitle: Test\n  version: 0.1.0\npaths:\n  /foo:\n    get:\n      operationId: testOp\n`;
    const tmpFile = path.join(tmpDir, "broken-tabs.yaml");
    await mkdir(tmpDir, { recursive: true });
    await writeFile(tmpFile, brokenYaml, "utf8");
    const errors = await validateOpenAPI([tmpFile], "--lint");
    assert.ok(errors.length > 0, `Expected errors for tab indentation, got none`);
    assert.ok(errors.some((e) => /YAML parse error/i.test(e) || /tab/i.test(e)), `Expected tab/parse error, got: ${errors.join("; ")}`);
  });
});

describe("validate.mjs exported helpers", () => {
  it("collectRefs finds all $ref values", () => {
    const obj = {
      foo: { $ref: "#/components/schemas/A" },
      bar: [{ $ref: "#/components/schemas/B" }, { baz: { $ref: "#/components/headers/C" } }],
    };
    const refs = collectRefs(obj);
    assert.deepStrictEqual(refs, ["#/components/schemas/A", "#/components/schemas/B", "#/components/headers/C"]);
  });

  it("resolvePointer resolves valid pointers", () => {
    const doc = { components: { schemas: { Error: { type: "object" } } } };
    assert.deepStrictEqual(resolvePointer(doc, "#/components/schemas/Error"), { type: "object" });
  });

  it("resolvePointer returns undefined for invalid pointers", () => {
    const doc = { components: { schemas: {} } };
    assert.strictEqual(resolvePointer(doc, "#/components/schemas/NonExistent"), undefined);
  });
});
