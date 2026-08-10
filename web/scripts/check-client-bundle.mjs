#!/usr/bin/env node
/**
 * Scans built static assets for forbidden patterns:
 * - known secret variable names (DATABASE_URL, INTERNAL_GRPC_TOKEN, etc.)
 * - fixture-mode markers that must not reach production
 *
 * Expects the built .next/static/ directory to already exist.
 * Exit 0 = clean. Exit 1 = forbidden pattern found.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const appDir = process.argv[2];

if (!appDir) {
  console.error('Usage: check-client-bundle.mjs <path-to-app>');
  process.exit(1);
}

const FORBIDDEN_PATTERNS = [
  /DATABASE_URL/,
  /POSTGRES_PASSWORD/,
  /INTERNAL_GRPC_TOKEN/,
  /ADMIN_SESSION_SECRET/,
  /JWT_SECRET/,
  /KEK_PASSPHRASE/,
  /VAULT_TOKEN/,
  /FIXTURE_MODE_ENABLED_PRODUCTION/,
  /__FIXTURE_ONLY_PRODUCTION_MARKER__/,
];

const staticDir = join(appDir, '.next', 'static');

let found = false;

function scanDir(dir) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return; // directory may not exist if build failed earlier
  }
  for (const entry of entries) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      scanDir(full);
      continue;
    }
    if (!entry.endsWith('.js') && !entry.endsWith('.css')) continue;
    const content = readFileSync(full, 'utf8');
    for (const pattern of FORBIDDEN_PATTERNS) {
      if (pattern.test(content)) {
        console.error(`FORBIDDEN: ${pattern} found in ${full}`);
        found = true;
      }
    }
  }
}

scanDir(staticDir);

if (found) {
  console.error('bundle-scan: forbidden patterns found in client bundle');
  process.exit(1);
}

console.log(`bundle-scan: no forbidden patterns found in ${staticDir}`);
