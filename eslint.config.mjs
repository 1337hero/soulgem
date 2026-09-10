import parser from '@typescript-eslint/parser'

export default [{
  ignores: ['bundle.js', 'node_modules/**', 'texcache/**', 'server/rhubarb/**'],
}, {
  files: ['main.js', 'client/**/*.ts', 'server/*.{js,ts}'],
  languageOptions: { parser, ecmaVersion: 'latest', sourceType: 'module' },
  rules: { complexity: ['error', { max: 20, variant: 'classic' }] },
}]
