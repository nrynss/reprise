import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default tseslint.config(
	js.configs.recommended,
	...tseslint.configs.recommended,
	...svelte.configs['flat/recommended'],
	{
		ignores: ['build/', '.svelte-kit/', 'node_modules/', 'test-results/', 'playwright-report/']
	},
	{
		files: ['**/*.svelte'],
		languageOptions: {
			globals: { ...globals.browser }
		}
	}
);
