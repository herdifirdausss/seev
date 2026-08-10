import { defineConfig } from 'vitest/config';

/** @param {import('vitest/config').UserConfig} overrides */
export function createVitestConfig(overrides = {}) {
  return defineConfig({
    test: {
      environment: 'node',
      globals: false,
      coverage: {
        provider: 'v8',
        reporter: ['text', 'lcov'],
      },
      ...overrides.test,
    },
    ...overrides,
  });
}
