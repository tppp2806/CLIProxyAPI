const { chromium } = require('@playwright/test');

(async () => {
  console.log('Opening browser in visible mode...');
  const browser = await chromium.launch({ headless: false });
  const page = await browser.newPage();

  // Set viewport
  await page.setViewportSize({ width: 1280, height: 800 });

  console.log('Navigating to balance-checker.html...');
  await page.goto('http://localhost:8888/balance-checker.html');

  console.log('Waiting for page to load...');
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(3000);

  console.log('Browser is now open. Close it when you\'re done viewing.');
  console.log('Press Ctrl+C in terminal to exit...');

  // Keep the process alive
  await new Promise(() => {});
})();
