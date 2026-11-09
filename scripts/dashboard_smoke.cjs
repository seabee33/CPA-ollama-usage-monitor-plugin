// Browser behavior check with fixture data; no Ollama requests or real secrets.
// Install Playwright separately, then NODE_PATH=/path/to/node_modules node scripts/dashboard_smoke.cjs
const { chromium } = require('playwright');
const fs = require('node:fs/promises');
const http = require('node:http');
const path = require('node:path');
const assert = require('node:assert/strict');
(async () => {
  const token = 'synthetic-dashboard-token-32-characters';
  const name = '<img src=x onerror=alert(1)> Personal';
  const csp = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'";
  const server = http.createServer(async (req, res) => {
    res.setHeader('Content-Security-Policy', csp);
    res.setHeader('Cache-Control', 'no-store');
    const url = new URL(req.url, 'http://localhost');
    const file = {'/dashboard': 'index.html', '/app.js': 'app.js', '/style.css': 'style.css'}[url.pathname];
    if (file) {
      res.setHeader('Content-Type', file.endsWith('.js') ? 'application/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html');
      return res.end(await fs.readFile(path.join(__dirname, '../web', file)));
    }
    res.setHeader('Content-Type', 'application/json');
    if (req.headers['x-ollama-monitor-token'] !== token) {
      res.statusCode = 401; return res.end(JSON.stringify({error: 'Invalid dashboard access token'}));
    }
    if (!url.searchParams.has('account')) return res.end(JSON.stringify({accounts: [name]}));
    res.end(JSON.stringify({served_at: new Date().toISOString(), quota: {groups: [{buckets: [
      {description: '5-hour usage', remainingFraction: .75, resetTime: '2026-12-01T01:00:00Z'},
      {description: 'Weekly usage', remainingFraction: .4, resetTime: '2026-12-06T01:00:00Z'},
    ]}]}}));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const browser = await chromium.launch({headless: true});
  try {
    const page = await browser.newPage(); const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(`http://127.0.0.1:${server.address().port}/dashboard`);
    await page.locator('#token').fill('wrong'); await page.getByRole('button', {name:'Unlock',exact:true}).click();
    await page.getByText('Invalid dashboard access token', {exact:true}).waitFor();
    await page.locator('#token').fill(token); await page.getByRole('button', {name:'Unlock',exact:true}).click();
    await page.getByText('Quota updated.', {exact:true}).waitFor();
    assert.equal(await page.getByText('25.0% used', {exact:true}).count(), 1);
    assert.equal(await page.getByText('60.0% used', {exact:true}).count(), 1);
    assert.equal(await page.locator('#cards img').count(), 0);
    assert.equal(await page.locator('#cards h2').textContent(), name);
    assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
    await page.setViewportSize({width:390,height:844});
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    await page.getByRole('button', {name:'Lock dashboard'}).click();
    assert.equal(await page.locator('#cards').textContent(), '');
    assert.ok(await page.locator('#login').isVisible());
    assert.deepEqual(errors, []);
    console.log('Browser smoke passed: unlock, percentages, safe text rendering, mobile layout, no persistent token, lock');
  } finally { await browser.close(); server.close(); }
})().catch(error => {console.error(error);process.exit(1);});
