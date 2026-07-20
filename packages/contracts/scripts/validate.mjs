import { readFile } from "node:fs/promises";
import path from "node:path";
import YAML from "yaml";
import { findYamlFiles, forbiddenTerms, openapiRoot, packageRoot } from "./contract-helpers.mjs";

const VALID_HTTP_METHODS = new Set(["get", "put", "post", "delete", "options", "head", "patch", "trace"]);

/** Recursively collect all $ref string values from a parsed object. */
export function collectRefs(obj) {
  const refs = [];
  if (typeof obj !== "object" || obj === null) return refs;
  if (Array.isArray(obj)) {
    for (const item of obj) refs.push(...collectRefs(item));
  } else {
    if (typeof obj.$ref === "string") refs.push(obj.$ref);
    for (const value of Object.values(obj)) refs.push(...collectRefs(value));
  }
  return refs;
}

/** Resolve a JSON Pointer (#/a/b/c) against a parsed document. */
export function resolvePointer(doc, pointer) {
  if (pointer === "#") return doc;
  if (!pointer.startsWith("#/")) return undefined;
  const parts = pointer.slice(2).split("/");
  let current = doc;
  for (const part of parts) {
    if (current === null || current === undefined || typeof current !== "object") return undefined;
    current = current[part];
  }
  return current;
}

function escapeJsonPointerToken(token) {
  return String(token).replaceAll("~", "~0").replaceAll("/", "~1");
}

function isMapping(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Validate a Schema Object and its schema-bearing children.
 *
 * OpenAPI uses `required: true` on Parameter and Request Body Objects, so this
 * intentionally runs only after reaching a Schema Object rather than scanning
 * every `required` key in the document.
 */
export function validateSchemaObject(schema, pointer, errors) {
  if (!isMapping(schema)) return;

  if (
    Object.hasOwn(schema, "required") &&
    (!Array.isArray(schema.required) || !schema.required.every((name) => typeof name === "string"))
  ) {
    errors.push(`${pointer}/required: Schema Object required must be an array of strings`);
  }

  if (isMapping(schema.properties)) {
    for (const [name, property] of Object.entries(schema.properties)) {
      validateSchemaObject(property, `${pointer}/properties/${escapeJsonPointerToken(name)}`, errors);
    }
  }

  if (isMapping(schema.items)) {
    validateSchemaObject(schema.items, `${pointer}/items`, errors);
  }

  if (isMapping(schema.additionalProperties)) {
    validateSchemaObject(schema.additionalProperties, `${pointer}/additionalProperties`, errors);
  }

  for (const keyword of ["allOf", "anyOf", "oneOf"]) {
    if (!Array.isArray(schema[keyword])) continue;
    for (const [index, child] of schema[keyword].entries()) {
      validateSchemaObject(child, `${pointer}/${keyword}/${index}`, errors);
    }
  }

  if (isMapping(schema.not)) {
    validateSchemaObject(schema.not, `${pointer}/not`, errors);
  }
}

/** Validate every Schema Object root in an OpenAPI document. */
export function validateSchemaObjects(doc, errors) {
  if (!isMapping(doc)) return;

  if (isMapping(doc.components?.schemas)) {
    for (const [name, schema] of Object.entries(doc.components.schemas)) {
      validateSchemaObject(schema, `#/components/schemas/${escapeJsonPointerToken(name)}`, errors);
    }
  }

  function findSchemaValues(value, pointer) {
    if (Array.isArray(value)) {
      for (const [index, child] of value.entries()) findSchemaValues(child, `${pointer}/${index}`);
      return;
    }
    if (!isMapping(value)) return;

    for (const [key, child] of Object.entries(value)) {
      const childPointer = `${pointer}/${escapeJsonPointerToken(key)}`;
      if (key === "schema") validateSchemaObject(child, childPointer, errors);
      findSchemaValues(child, childPointer);
    }
  }

  findSchemaValues(doc, "#");
}

/**
 * Validate a list of OpenAPI YAML files. Returns an array of error strings.
 * @param {string[]} files - Absolute file paths
 * @param {string} mode - "--lint" or "--contract"
 * @returns {Promise<string[]>}
 */
export async function validateOpenAPI(files, mode) {
  const errors = [];

  if (files.length === 0) {
    errors.push("No OpenAPI YAML files found under openapi/");
  }

  /** @type {Map<string, {file: string, doc: object}>} */
  const parsedDocs = new Map();

  for (const file of files) {
    const relative = path.relative(packageRoot, file);
    let content;
    try {
      content = await readFile(file, "utf8");
    } catch (err) {
      errors.push(`${relative}: read error: ${err.message}`);
      continue;
    }

    // Text-level checks
    if (/[ \t]+$/m.test(content)) errors.push(`${relative}: trailing whitespace`);
    if (!content.endsWith("\n")) errors.push(`${relative}: missing final newline`);

    // Parse YAML
    let doc;
    try {
      doc = YAML.parse(content, { strict: true });
    } catch (err) {
      errors.push(`${relative}: YAML parse error: ${err.message}`);
      continue;
    }

    if (typeof doc !== "object" || doc === null) {
      errors.push(`${relative}: parsed YAML is not a mapping`);
      continue;
    }

    parsedDocs.set(relative, { file, doc });

    // OpenAPI version
    if (!doc.openapi || !/^3\.\d+\.\d+$/.test(String(doc.openapi))) {
      errors.push(`${relative}: openapi must be 3.x.x, got '${doc.openapi}'`);
    }

    // info.title and info.version
    if (!doc.info || typeof doc.info !== "object") {
      errors.push(`${relative}: missing or invalid info section`);
    } else {
      if (!doc.info.title || typeof doc.info.title !== "string") {
        errors.push(`${relative}: missing info.title`);
      }
      if (!doc.info.version || typeof doc.info.version !== "string") {
        errors.push(`${relative}: missing info.version`);
      }
    }

    // paths must exist and be non-empty
    if (!doc.paths || typeof doc.paths !== "object" || Object.keys(doc.paths).length === 0) {
      errors.push(`${relative}: missing or empty paths section`);
    } else {
      for (const [route, methods] of Object.entries(doc.paths)) {
        if (typeof methods !== "object" || methods === null) continue;
        let hasOperation = false;
        for (const [method, operation] of Object.entries(methods)) {
          if (!VALID_HTTP_METHODS.has(method)) continue;
          hasOperation = true;
          if (typeof operation !== "object" || operation === null) continue;
          if (!operation.operationId || typeof operation.operationId !== "string") {
            errors.push(`${relative}: path '${route}' method '${method}' missing operationId`);
          }
        }
        if (!hasOperation) {
          errors.push(`${relative}: path '${route}' has no valid HTTP method operations`);
        }
      }
    }

    // Forbidden internal implementation terms (checked on raw text)
    const lines = content.split("\n");
    for (let i = 0; i < lines.length; i++) {
      const lower = lines[i].toLowerCase();
      for (const term of forbiddenTerms) {
        if (lower.includes(term.toLowerCase())) {
          errors.push(`${relative}:${i + 1}: forbidden internal term '${term}' found`);
        }
      }
    }

    // Schema Object `required` must be an array of property names. This is
    // deliberately separate from OpenAPI Parameter/Request Body `required`.
    validateSchemaObjects(doc, errors);

    // Intra-file operationId uniqueness
    if (doc.paths && typeof doc.paths === "object") {
      const seen = new Set();
      for (const methods of Object.values(doc.paths)) {
        if (typeof methods !== "object" || methods === null) continue;
        for (const [method, operation] of Object.entries(methods)) {
          if (!VALID_HTTP_METHODS.has(method)) continue;
          if (typeof operation !== "object" || operation === null) continue;
          const id = operation.operationId;
          if (!id) continue;
          if (seen.has(id)) {
            errors.push(`${relative}: duplicate operationId '${id}'`);
          }
          seen.add(id);
        }
      }
    }
  }

  if (mode === "--contract") {
    // Cross-file operationId uniqueness
    const allOperationIds = new Map();
    for (const [relative, { doc }] of parsedDocs) {
      if (!doc.paths || typeof doc.paths !== "object") continue;
      for (const methods of Object.values(doc.paths)) {
        if (typeof methods !== "object" || methods === null) continue;
        for (const [method, operation] of Object.entries(methods)) {
          if (!VALID_HTTP_METHODS.has(method)) continue;
          if (typeof operation !== "object" || operation === null) continue;
          const id = operation.operationId;
          if (!id) continue;
          if (allOperationIds.has(id)) {
            errors.push(
              `cross-file: duplicate operationId '${id}' in ${relative} and ${allOperationIds.get(id)}`,
            );
          }
          allOperationIds.set(id, relative);
        }
      }
    }

    // $ref resolution within each file
    for (const [relative, { doc }] of parsedDocs) {
      const refs = collectRefs(doc);
      for (const ref of refs) {
        if (!ref.startsWith("#/")) continue;
        const resolved = resolvePointer(doc, ref);
        if (resolved === undefined) {
          errors.push(`${relative}: unresolved $ref '${ref}'`);
        }
      }
    }
  }

  return errors;
}

// CLI entry point
const mode = process.argv[2] ?? "--contract";
const files = await findYamlFiles(openapiRoot);
const errors = await validateOpenAPI(files, mode);

if (errors.length > 0) {
  console.error(errors.join("\n"));
  process.exitCode = 1;
} else {
  console.log(`${mode.slice(2)} validation passed for ${files.length} OpenAPI file(s)`);
}
