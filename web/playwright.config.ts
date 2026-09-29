import { defineConfig } from '@playwright/test';
export default defineConfig({ testDir: './tests', fullyParallel: false, workers: 1, timeout: 30000, use: { baseURL: process.env.DEMO_BASE_URL || 'http://127.0.0.1:8080', ignoreHTTPSErrors: process.env.DEMO_IGNORE_HTTPS_ERRORS === 'true', headless: true }, reporter: 'list' });
