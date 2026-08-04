import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    environmentOptions: {
      // pretendToBeVisual gives jsdom a requestAnimationFrame so React's
      // scheduler/act can run normally in component tests.
      jsdom: { pretendToBeVisual: true },
    },
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
});
