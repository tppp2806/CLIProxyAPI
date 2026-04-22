const { chromium } = require('@playwright/test');

(async () => {
  console.log('Opening browser in visible mode...');
  const browser = await chromium.launch({ headless: false });
  const page = await browser.newPage();
  await page.setViewportSize({ width: 1280, height: 800 });

  console.log('Navigating to balance-checker.html...');
  await page.goto('http://localhost:8888/balance-checker.html');
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(3000);

  console.log('Browser opened! You should see the balance checker page.');
  console.log('Close the browser window when you are done viewing.');

  // Keep browser open for 60 seconds
  await new Promise(resolve => setTimeout(resolve, 60000));
  await browser.close();
})();
