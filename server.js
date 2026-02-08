#!/usr/bin/env node
'use strict';

const http = require('node:http');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

// Resolve paths
const crewDir = __dirname;
const rigDir = path.resolve(crewDir, '..', '..');
const townRoot = path.resolve(rigDir, '..');
const configPath = path.join(crewDir, 'config.json');
const indexPath = path.join(crewDir, 'index.html');
const townBeadsDir = path.join(townRoot, '.beads');

// Build prefix-to-beadsDir map for resolving bead IDs across rigs
function buildPrefixMap() {
  const map = { hq: townBeadsDir };
  try {
    const entries = fs.readdirSync(townRoot, { withFileTypes: true });
    for (const e of entries) {
      if (!e.isDirectory()) continue;
      const beadsDir = path.join(townRoot, e.name, '.beads');
      const dbPath = path.join(beadsDir, 'beads.db');
      if (fs.existsSync(dbPath)) {
        // Read prefix from config or use directory name
        try {
          const cfgPath = path.join(beadsDir, 'config.json');
          const cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
          if (cfg.prefix) map[cfg.prefix] = beadsDir;
        } catch {}
        map[e.name] = beadsDir;
      }
    }
  } catch {}
  return map;
}
const prefixMap = buildPrefixMap();

function beadsDirForId(beadId) {
  // Extract prefix from bead ID (e.g. "rigradar-t46" -> "rigradar", "hq-cv-xxx" -> "hq")
  const dash = beadId.indexOf('-');
  if (dash > 0) {
    const prefix = beadId.substring(0, dash);
    if (prefixMap[prefix]) return prefixMap[prefix];
  }
  return townBeadsDir; // fallback
}

// Parse CLI args
const args = process.argv.slice(2);
let portOverride = null;
let openBrowser = false;
for (let i = 0; i < args.length; i++) {
  if (args[i] === '--port' && args[i + 1]) {
    portOverride = parseInt(args[i + 1], 10);
    i++;
  } else if (args[i] === '--open') {
    openBrowser = true;
  }
}

function loadConfig() {
  try {
    return JSON.parse(fs.readFileSync(configPath, 'utf8'));
  } catch {
    return {
      filters: { hideSystemBeads: true, hideEvents: true, hideRigIdentity: true, hideMaintenanceWisps: true, hideHQBeads: true },
      server: { port: 9292, host: 'localhost' },
      refreshInterval: 30000
    };
  }
}

function saveConfig(config) {
  fs.writeFileSync(configPath, JSON.stringify(config, null, 2) + '\n');
}

function execCmd(cmd, args, opts = {}) {
  return new Promise((resolve, reject) => {
    const proc = spawn(cmd, args, {
      cwd: opts.cwd || townRoot,
      env: { ...process.env, ...(opts.env || {}) },
      timeout: 15000
    });
    let stdout = '';
    let stderr = '';
    proc.stdout.on('data', d => stdout += d);
    proc.stderr.on('data', d => stderr += d);
    proc.on('close', code => {
      if (code !== 0) {
        reject(new Error(`${cmd} ${args.join(' ')} exited ${code}: ${stderr}`));
      } else {
        try {
          resolve(JSON.parse(stdout));
        } catch {
          resolve(stdout.trim());
        }
      }
    });
    proc.on('error', reject);
  });
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let body = '';
    req.on('data', c => body += c);
    req.on('end', () => {
      try { resolve(JSON.parse(body)); }
      catch (e) { reject(e); }
    });
    req.on('error', reject);
  });
}

function sendJSON(res, data, status = 200) {
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET,POST,OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type'
  });
  res.end(JSON.stringify(data));
}

function sendHTML(res, html) {
  res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
  res.end(html);
}

function send404(res) {
  res.writeHead(404, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ error: 'Not found' }));
}

// Route handlers
async function handleReady(req, res) {
  try {
    const data = await execCmd('gt', ['ready', '--json']);
    sendJSON(res, data);
  } catch (e) {
    sendJSON(res, { error: e.message }, 500);
  }
}

async function handleStatus(req, res) {
  try {
    const data = await execCmd('gt', ['status', '--json']);
    sendJSON(res, data);
  } catch (e) {
    sendJSON(res, { error: e.message }, 500);
  }
}

async function handleBeads(req, res) {
  try {
    const url = new URL(req.url, `http://${req.headers.host}`);
    const status = url.searchParams.get('status');
    const type = url.searchParams.get('type');

    // Query each beads dir (town + all rigs)
    const dirs = new Set(Object.values(prefixMap));
    const promises = [...dirs].map(async (dir) => {
      try {
        const bdArgs = ['list', '--json'];
        if (status) bdArgs.push(`--status=${status}`);
        if (type) bdArgs.push(`--type=${type}`);
        const data = await execCmd('bd', bdArgs, { env: { BEADS_DIR: dir } });
        return Array.isArray(data) ? data : [];
      } catch {
        return [];
      }
    });
    const results = await Promise.all(promises);
    const all = results.flat();
    sendJSON(res, all);
  } catch (e) {
    sendJSON(res, { error: e.message }, 500);
  }
}

async function handleBeadDetail(req, res, id) {
  try {
    const dir = beadsDirForId(id);
    const data = await execCmd('bd', ['show', id, '--json'], { env: { BEADS_DIR: dir } });
    sendJSON(res, data);
  } catch (e) {
    sendJSON(res, { error: e.message }, 500);
  }
}

function handleGetConfig(req, res) {
  sendJSON(res, loadConfig());
}

async function handlePostConfig(req, res) {
  try {
    const body = await readBody(req);
    const current = loadConfig();
    const merged = { ...current, ...body, filters: { ...current.filters, ...(body.filters || {}) } };
    saveConfig(merged);
    sendJSON(res, merged);
  } catch (e) {
    sendJSON(res, { error: e.message }, 400);
  }
}

function handleHealth(req, res) {
  sendJSON(res, { status: 'ok', uptime: process.uptime(), town: townRoot });
}

function handleIndex(req, res) {
  try {
    const html = fs.readFileSync(indexPath, 'utf8');
    sendHTML(res, html);
  } catch (e) {
    res.writeHead(500, { 'Content-Type': 'text/plain' });
    res.end('Failed to load index.html: ' + e.message);
  }
}

// Router
const server = http.createServer(async (req, res) => {
  // CORS preflight
  if (req.method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET,POST,OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type'
    });
    return res.end();
  }

  const url = new URL(req.url, `http://${req.headers.host}`);
  const pathname = url.pathname;

  if (req.method === 'GET' && pathname === '/') return handleIndex(req, res);
  if (req.method === 'GET' && pathname === '/health') return handleHealth(req, res);
  if (req.method === 'GET' && pathname === '/api/ready') return handleReady(req, res);
  if (req.method === 'GET' && pathname === '/api/status') return handleStatus(req, res);
  if (req.method === 'GET' && pathname === '/api/beads') return handleBeads(req, res);
  if (req.method === 'GET' && pathname === '/api/config') return handleGetConfig(req, res);
  if (req.method === 'POST' && pathname === '/api/config') return handlePostConfig(req, res);

  // /api/bead/:id
  const beadMatch = pathname.match(/^\/api\/bead\/(.+)$/);
  if (req.method === 'GET' && beadMatch) return handleBeadDetail(req, res, beadMatch[1]);

  send404(res);
});

const config = loadConfig();
const port = portOverride || config.server.port || 9292;
const host = config.server.host || 'localhost';

server.listen(port, host, () => {
  console.log(`Rigradar running at http://${host}:${port}`);
  console.log(`Town root: ${townRoot}`);
  if (openBrowser) {
    const cmd = process.platform === 'darwin' ? 'open' : 'xdg-open';
    spawn(cmd, [`http://${host}:${port}`], { detached: true, stdio: 'ignore' }).unref();
  }
});
