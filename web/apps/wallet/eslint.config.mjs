import { react } from '@seev/config/eslint';

export default [
  ...react,
  {
    languageOptions: {
      parserOptions: {
        project: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    ignores: ['.next/**', 'node_modules/**'],
  },
];
