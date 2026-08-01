import nextPlugin from '@next/eslint-plugin-next';

const recommendedRules =
  (nextPlugin.configs.recommended && nextPlugin.configs.recommended.rules) || {};

const eslintConfig = [
  {
    plugins: { '@next/next': nextPlugin },
    rules: {
      ...recommendedRules,
    },
  },
  {
    ignores: ['node_modules/**', '.next/**', '.next-e2e/**', 'dist/**'],
  },
];

export default eslintConfig;
