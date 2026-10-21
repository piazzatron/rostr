// @ts-check
import { defineConfig } from 'astro/config';

import expressiveCode from 'astro-expressive-code';

// https://astro.build/config
export default defineConfig({
  site: 'https://getrostr.sh',
  integrations: [
    expressiveCode({
      // Terminals stay dark in both light and dark mode, like a real terminal
      themes: ['github-dark'],
      useDarkModeMediaQuery: false,
      styleOverrides: {
        borderRadius: '12px',
        codeFontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
        codeFontSize: '14px',
        codeLineHeight: '1.7',
      },
    }),
  ],
});
