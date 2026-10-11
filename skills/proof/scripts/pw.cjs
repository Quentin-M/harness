// Playwright driver behind shot, record and diagram (svg->png). Run via lib.sh `pw`.
//   node pw.cjs shot    <url> <out.png>  <WxH> <full:0|1>
//   node pw.cjs record  <url> <out.webm> <WxH> [steps.js]
//   node pw.cjs svg2png <in.svg> <out.png>
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const size = (s) => {
  const [width, height] = s.split('x').map(Number);
  if (!width || !height) throw new Error(`bad size ${s}, want WxH`);
  return { width, height };
};

async function shot(url, out, wh, full) {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: size(wh) });
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.screenshot({ path: out, fullPage: full === '1' });
  await browser.close();
}

// Default session: load, pause, scroll to the bottom and back. A steps file replaces it:
//   module.exports = async (page) => { await page.click('text=Login'); ... }
async function record(url, out, wh, steps) {
  const dir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'harness-rec-'));
  const browser = await chromium.launch();
  const context = await browser.newContext({ viewport: size(wh), recordVideo: { dir, size: size(wh) } });
  const page = await context.newPage();
  await page.goto(url, { waitUntil: 'networkidle' });
  if (steps) {
    await require(path.resolve(steps))(page);
  } else {
    await page.waitForTimeout(1500);
    await page.mouse.wheel(0, 2000);
    await page.waitForTimeout(1000);
    await page.mouse.wheel(0, -2000);
    await page.waitForTimeout(1000);
  }
  const video = page.video();
  await context.close();
  await video.saveAs(out);
  await browser.close();
  fs.rmSync(dir, { recursive: true, force: true });
}

async function svg2png(input, out) {
  const browser = await chromium.launch();
  const page = await browser.newPage({ deviceScaleFactor: 2 });
  await page.goto('file://' + path.resolve(input));
  // d2 emits a root <svg> with only a viewBox, which would stretch to the viewport.
  await page.evaluate(() => {
    const svg = document.querySelector('svg');
    const vb = svg.viewBox.baseVal;
    if (!svg.getAttribute('width') && vb && vb.width) {
      svg.setAttribute('width', vb.width);
      svg.setAttribute('height', vb.height);
    }
  });
  await page.locator('svg').first().screenshot({ path: out });
  await browser.close();
}

const [mode, ...args] = process.argv.slice(2);
const fn = { shot, record, svg2png }[mode];
if (!fn) { console.error(`unknown mode ${mode}`); process.exit(1); }
fn(...args).then(() => console.log(args[1]), (e) => { console.error(e.message); process.exit(1); });
