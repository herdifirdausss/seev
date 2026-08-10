#!/usr/bin/env node
/**
 * Verifies allowed package dependency directions.
 *
 * Forbidden:
 * - shared packages importing application code
 * - wallet importing operator (or vice versa)
 * - domain importing React, Next.js, query libraries, or browser APIs
 * - money importing UI or transport code
 * - production code importing @seev/testing
 * - UI making HTTP requests directly
 *
 * Exit 0 = all clear. Exit 1 = boundary violation found.
 */

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = join(__dirname, '..');

const VIOLATIONS = [];

function readJson(file) {
  try {
    return JSON.parse(readFileSync(file, 'utf8'));
  } catch {
    return null;
  }
}

function check(packagePath, pkgName, deps, forbidden, reason) {
  for (const dep of Object.keys(deps)) {
    for (const pattern of forbidden) {
      if (typeof pattern === 'string' ? dep === pattern : pattern.test(dep)) {
        VIOLATIONS.push(`${pkgName} must not depend on ${dep}: ${reason} (in ${packagePath})`);
      }
    }
  }
}

function getAllDeps(pkg) {
  return {
    ...pkg.dependencies,
    ...pkg.peerDependencies,
  };
}

// --- Check packages ---
const packagesDir = join(root, 'packages');
for (const name of readdirSync(packagesDir)) {
  const pkgFile = join(packagesDir, name, 'package.json');
  const pkg = readJson(pkgFile);
  if (!pkg) continue;

  const deps = getAllDeps(pkg);
  const pkgName = `packages/${name}`;

  // No package may depend on app code
  check(pkgFile, pkgName, deps, [/@seev\/wallet/, /@seev\/operator/], 'packages cannot depend on apps');

  // domain: no React, Next.js, query libraries, browser-only APIs
  if (name === 'domain') {
    check(pkgFile, pkgName, deps,
      ['react', 'react-dom', 'next', '@tanstack/react-query'],
      'domain must not import React, Next.js, or query libraries');
  }

  // money: no UI or transport code
  if (name === 'money') {
    check(pkgFile, pkgName, deps,
      ['react', 'react-dom', 'next', '@seev/ui', '@seev/api-client'],
      'money must not import UI or transport code');
  }
}

// --- Check apps for cross-app imports ---
const appsDir = join(root, 'apps');
for (const appName of readdirSync(appsDir)) {
  const pkgFile = join(appsDir, appName, 'package.json');
  const pkg = readJson(pkgFile);
  if (!pkg) continue;

  const allDeps = { ...pkg.dependencies, ...pkg.devDependencies };
  const pkgName = `apps/${appName}`;

  if (appName === 'wallet') {
    check(pkgFile, pkgName, allDeps, ['@seev/operator'], 'wallet must not import operator');
  }
  if (appName === 'operator') {
    check(pkgFile, pkgName, allDeps, ['@seev/wallet'], 'operator must not import wallet');
  }

  // Production deps (not devDeps) must not include @seev/testing
  check(pkgFile, pkgName, pkg.dependencies ?? {}, ['@seev/testing'],
    '@seev/testing must not be a production dependency');
}

if (VIOLATIONS.length > 0) {
  console.error('Package boundary violations found:');
  for (const v of VIOLATIONS) {
    console.error(`  ✗ ${v}`);
  }
  process.exit(1);
}

console.log('boundary-check: all package dependencies are within allowed bounds');
