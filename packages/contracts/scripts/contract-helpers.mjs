import { fileURLToPath } from "node:url";
import path from "node:path";

export const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const openapiRoot = path.join(packageRoot, "openapi");

/** Terms that reveal private implementation details and must not appear in consumer-facing contracts. */
export const forbiddenTerms = [
  "argon2id",
  "bcrypt",
  "gorm",
  "autoMigrate",
  "auto_migrate",
  "SELECT FOR UPDATE",
  "SELECT ... FOR UPDATE",
  "SHA-256",
  "BYTEA",
  "revoke_reason",
  "token_version",
  "password_hash",
  "replaced_by_session_id",
  "token_family_id",
  "refresh_token_hash",
  "LOWER(BTRIM)",
  "LOWER (BTRIM)",
  "BTRIM",
  "pgcrypto",
  "CHECK (",
  "VARCHAR",
  "INET",
  "PHC",
  "token_rotated",
  "token_reuse",
  "password_changed",
  "logout_all",
  "user_disabled",
  "admin_revoked",
  "revoke_reason=",
  "revoke_reason='",
  "CompareDummy",
  "dummy",
  "timing side channel",
  "timing",
  "transaction",
  "commit",
  "rollback",
  "TxRunner",
  "sentinel",
  "ErrDuplicateEmail",
  "ErrNotFound",
  "ErrConstraint",
  "ErrInternal",
  "driver",
  "DSN",
  "pq:",
  "postgres",
  "postgresql",
  "tokenmp_auth",
  "auth_sessions",
  "users.",
  "users table",
];

/**
 * Recursively find all .yaml/.yml files under a directory.
 */
export async function findYamlFiles(dir) {
  const { readdir } = await import("node:fs/promises");
  const entries = await readdir(dir, { withFileTypes: true });
  const files = await Promise.all(
    entries.map((entry) => {
      const target = path.join(dir, entry.name);
      return entry.isDirectory() ? findYamlFiles(target) : target;
    }),
  );
  return files.flat().filter((f) => f.endsWith(".yaml") || f.endsWith(".yml")).sort();
}
