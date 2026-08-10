import { createVitestConfig } from '@seev/config/vitest';
import react from '@vitejs/plugin-react';

export default createVitestConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
  },
});
