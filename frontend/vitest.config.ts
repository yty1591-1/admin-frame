import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';
import { sharedTestConfig } from './packages/config/src/vitest-base';

export default defineConfig({
  plugins: [react()],
  test: sharedTestConfig,
});
