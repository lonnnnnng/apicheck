// scripts/ui-smoke.mjs —— 前端冒烟测试
//
// 作者 long: 之前每个 UI 改动都是临时写 /tmp/*.mjs 跑一遍，改完就丢，回归只能靠手点。
// 这里把最关键的几条链路固化成脚本，本地和 CI 都能直接跑：
//   1. 页面能加载且没有 JS 报错
//   2. 获取模型 → 表格出现模型行
//   3. 全部检测 → 可用/不支持 单元格与状态栏正确
//   4. 导出 CSV → 行数与列名正确
//   5. 刷新页面 → 检测结果从 localStorage 恢复
//   6. 设置弹窗与「系统提示词预设预览」弹窗能打开
//
// 依赖：playwright-core（本地可来自 npx 缓存，CI 由 workflow 安装）。
// 用法：node scripts/ui-smoke.mjs

import http from 'node:http';
import net from 'node:net';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import fs from 'node:fs';

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// ---------- 断言 ----------
let failed = 0;
function check(name, ok, detail = '') {
  if (ok) {
    console.log(`  ✓ ${name}`);
  } else {
    failed += 1;
    console.log(`  ✗ ${name}${detail ? ` —— ${detail}` : ''}`);
  }
}

// ---------- 假网关：两个模型，一个正常、一个被限流 ----------
function readBody(req) {
  return new Promise((resolve) => {
    let raw = '';
    req.on('data', (chunk) => (raw += chunk));
    req.on('end', () => resolve(raw));
  });
}

function startMockProvider() {
  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, 'http://127.0.0.1');
    const raw = await readBody(req);
    const send = (status, payload, headers = {}) => {
      const body = typeof payload === 'string' ? payload : JSON.stringify(payload);
      res.writeHead(status, { 'Content-Type': 'application/json', ...headers });
      res.end(body);
    };

    if (url.pathname.endsWith('/models')) {
      return send(200, { object: 'list', data: [{ id: 'mock-ok' }, { id: 'mock-limited' }] });
    }

    let model = 'mock-ok';
    let stream = false;
    try {
      const parsed = JSON.parse(raw || '{}');
      model = parsed.model || model;
      stream = !!parsed.stream;
    } catch (error) {
      // 忽略解析失败，走默认分支
    }

    if (model.includes('limited')) {
      return send(429, { error: { message: 'slow down', type: 'rate_limit_error' } }, { 'Retry-After': '1' });
    }

    if (url.pathname.includes('/messages')) {
      if (stream) {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.write(`event: message_start\ndata: ${JSON.stringify({ type: 'message_start', message: { usage: { input_tokens: 11, output_tokens: 0 } } })}\n\n`);
        res.write(`event: content_block_delta\ndata: ${JSON.stringify({ type: 'content_block_delta', delta: { type: 'text_delta', text: 'pong' } })}\n\n`);
        res.write(`event: message_delta\ndata: ${JSON.stringify({ type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 5 } })}\n\n`);
        return res.end('event: message_stop\ndata: {"type":"message_stop"}\n\n');
      }
      return send(200, { content: [{ type: 'text', text: 'pong' }], usage: { input_tokens: 11, output_tokens: 5 } });
    }

    if (url.pathname.endsWith('/responses')) {
      const events = [
        ['response.created', { type: 'response.created', response: { id: 'resp_1', status: 'in_progress' } }],
        ['response.output_text.delta', { type: 'response.output_text.delta', delta: 'pong' }],
        ['response.completed', { type: 'response.completed', response: { id: 'resp_1', status: 'completed', usage: { input_tokens: 11, output_tokens: 5, total_tokens: 16 } } }],
      ];
      if (stream) {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        events.forEach(([name, payload]) => res.write(`event: ${name}\ndata: ${JSON.stringify(payload)}\n\n`));
        return res.end('event: response.done\ndata: {"type":"response.done"}\n\n');
      }
      return send(200, { status: 'completed', output: [{ type: 'message', content: [{ type: 'output_text', text: 'pong' }] }], usage: { input_tokens: 11, output_tokens: 5, total_tokens: 16 } });
    }

    if (stream) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.write(`data: ${JSON.stringify({ choices: [{ delta: { content: 'pong' } }] })}\n\n`);
      res.write(`data: ${JSON.stringify({ choices: [{ delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 11, completion_tokens: 5, total_tokens: 16 } })}\n\n`);
      return res.end('data: [DONE]\n\n');
    }
    return send(200, { choices: [{ finish_reason: 'stop', message: { role: 'assistant', content: 'pong' } }], usage: { prompt_tokens: 11, completion_tokens: 5, total_tokens: 16 } });
  });
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

function freePort() {
  return new Promise((resolve) => {
    const probe = net.createServer();
    probe.listen(0, '127.0.0.1', () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

function waitForServer(url, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    const attempt = () => {
      const req = http.get(url, (res) => {
        res.resume();
        resolve();
      });
      req.on('error', () => {
        if (Date.now() > deadline) reject(new Error(`服务未能就绪：${url}`));
        else setTimeout(attempt, 200);
      });
    };
    attempt();
  });
}

async function loadPlaywright() {
  const candidates = [
    'playwright-core',
    process.env.PLAYWRIGHT_CORE_PATH,
    '/Users/long/.npm/_npx/31e32ef8478fbf80/node_modules/playwright-core/index.js',
  ].filter(Boolean);
  for (const candidate of candidates) {
    try {
      const loaded = await import(candidate);
      // playwright-core 是 CJS，动态 import 后导出挂在 default 上。
      const chromium = loaded.chromium ?? loaded.default?.chromium;
      if (chromium) return { chromium };
    } catch (error) {
      // 试下一个
    }
  }
  throw new Error('未找到 playwright-core，请先执行：npm install --no-save playwright-core');
}

// ---------- 主流程 ----------
const mock = await startMockProvider();
const mockPort = mock.address().port;
const servePort = await freePort();
const binary = path.join(rootDir, 'build', 'ui-smoke-apicheck');

console.log('构建 CLI…');
await new Promise((resolve, reject) => {
  fs.mkdirSync(path.dirname(binary), { recursive: true });
  const build = spawn('go', ['build', '-o', binary, './cmd/apicheck'], { cwd: rootDir, stdio: 'inherit' });
  build.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`go build 退出码 ${code}`))));
});

console.log('启动本地服务…');
const server = spawn(binary, ['-serve', '-listen', `127.0.0.1:${servePort}`, '-static-dir', 'prototype'], {
  cwd: rootDir,
  stdio: ['ignore', 'pipe', 'pipe'],
});
server.stderr.on('data', (chunk) => process.stderr.write(`[serve] ${chunk}`));

const shutdown = () => {
  server.kill('SIGTERM');
  mock.close();
};
process.on('exit', shutdown);

let browser;
try {
  await waitForServer(`http://127.0.0.1:${servePort}/`);
  const { chromium } = await loadPlaywright();
  browser = await chromium.launch(
    process.env.PLAYWRIGHT_CHROMIUM_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } : {},
  );
  const context = await browser.newContext({ viewport: { width: 1420, height: 860 }, acceptDownloads: true });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));
  page.on('console', (message) => {
    if (message.type() === 'error') pageErrors.push(`console: ${message.text()}`);
  });

  console.log('检查页面加载…');
  await page.goto(`http://127.0.0.1:${servePort}/`);
  await page.waitForTimeout(300);
  check('页面无 JS 报错', pageErrors.length === 0, pageErrors[0]);
  check('标题栏与模型列表存在', await page.locator('.panebar h2').innerText() === '模型列表');

  await page.evaluate((baseUrl) => {
    accounts = [{ id: 'smoke', name: '冒烟账号', base_url: baseUrl, api_key: 'key-smoke', enabled: true, status: 'idle', error: '', modelCount: 0 }];
    activeAccountId = 'smoke';
    saveCachedInputs();
    applyActiveAccountToForm(activeAccount());
    renderAccounts();
    render();
  }, `http://127.0.0.1:${mockPort}/v1`);

  console.log('获取当前账号模型…');
  await page.click('#modelsCurrentBtn');
  await page.waitForTimeout(600);
  check('表格出现 2 个模型', await page.locator('#rows tr[data-index]').count() === 2);

  console.log('全部检测…');
  await page.click('#checkAllBtn');
  await page.waitForTimeout(1500);
  const cells = await page.evaluate(() =>
    [...document.querySelectorAll('#rows tr[data-index]')].map((row) => ({
      model: row.children[0].innerText.trim(),
      non: row.children[1].innerText.trim(),
      title: row.children[1].title,
    })),
  );
  const okCell = cells.find((cell) => cell.model.includes('mock-ok'));
  const limitedCell = cells.find((cell) => cell.model.includes('limited'));
  check('可用模型标记 ok 与耗时', !!okCell && okCell.non.includes('1'), okCell?.non);
  check('被限流模型显示状态码 + 中文标签', !!limitedCell && limitedCell.non.includes('429') && limitedCell.non.includes('限流'), limitedCell?.non);
  check('失败单元格 tooltip 带 error_class', !!limitedCell && limitedCell.title.includes('rate_limited'), limitedCell?.title);

  // 检测弹窗在批量检测结束后仍然开着，导出按钮在它后面，先关掉弹窗。
  await page.keyboard.press('Escape');
  await page.waitForTimeout(250);
  check('Esc 关闭检测弹窗', !(await page.locator('#checkModal').isVisible()));

  console.log('检查结果持久化…');
  const persisted = await page.evaluate(() => {
    const raw = localStorage.getItem('apicheck:results:v1');
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    return { rows: parsed.rows.length, hasRawResponse: raw.includes('raw_response') };
  });
  check('结果写入 localStorage', persisted?.rows === 2, JSON.stringify(persisted));
  check('持久化时丢弃原始响应', persisted?.hasRawResponse === false);

  console.log('导出 CSV…');
  const [download] = await Promise.all([page.waitForEvent('download', { timeout: 15000 }), page.click('#exportCsvBtn')]);
  const csv = fs.readFileSync(await download.path(), 'utf8').replace(/^\ufeff/, '');
  const lines = csv.split('\r\n').filter(Boolean);
  check('CSV 含中文表头', lines[0].startsWith('账号,BaseURL,模型,协议'), lines[0]);
  check('CSV 每个账号·模型·协议一行', lines.length === 3, `实际 ${lines.length} 行`);

  console.log('刷新后恢复结果…');
  await page.reload();
  await page.waitForTimeout(600);
  const restored = await page.evaluate(() => ({ rows: data.length, non: document.querySelector('#rows tr[data-index]')?.children[1].innerText.trim() || '' }));
  check('刷新后结果仍可见', restored.rows === 2 && restored.non.length > 0, JSON.stringify(restored));

  console.log('检查弹窗…');
  await page.click('#settingsBtn');
  await page.waitForTimeout(250);
  check('设置弹窗可打开', await page.locator('#settingsModal').isVisible());
  check('运行参数含模型等级', await page.locator('#reasoningEffort').count() === 1);
  await page.click('#presetPreviewBtn');
  await page.waitForTimeout(500);
  check('系统提示词预览弹窗可打开', await page.locator('#presetModal').isVisible());
  const identityText = await page.locator('#agentIdentityHeaders').innerText();
  check('预览弹窗展示两种风格的请求头', identityText.includes('OpenAI 风格') && identityText.includes('Anthropic 风格'));
  await page.keyboard.press('Escape');
  await page.waitForTimeout(200);
  check('Esc 关闭预览弹窗', !(await page.locator('#presetModal').isVisible()));
  await page.keyboard.press('Escape');

  check('全程无 JS 报错', pageErrors.length === 0, pageErrors.slice(0, 2).join(' | '));
} catch (error) {
  failed += 1;
  console.error('冒烟测试异常：', error);
} finally {
  if (browser) await browser.close();
  shutdown();
}

console.log(failed === 0 ? '\nUI 冒烟测试全部通过' : `\nUI 冒烟测试失败 ${failed} 项`);
process.exit(failed === 0 ? 0 : 1);
