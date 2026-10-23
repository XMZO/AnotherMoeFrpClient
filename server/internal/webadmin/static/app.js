// MoeFrp 管理后台。原生 JS，无依赖、无构建步骤。
// 所有界面都用 DOM API 拼出来，用户数据只经过 textContent，不用 innerHTML。
'use strict';

(() => {
  const API = new URL('../api/v1/admin', location.href).pathname;
  const $app = document.getElementById('app');
  const $toasts = document.getElementById('toasts');

  // ---------- DOM ----------

  function h(tag, attrs, ...children) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k === 'value') el.value = v;
      else el.setAttribute(k, v === true ? '' : String(v));
    }
    add(el, children);
    return el;
  }

  function add(el, children) {
    for (const c of children.flat(Infinity)) {
      if (c != null && c !== false) el.append(c);
    }
    return el;
  }

  const SVG = 'http://www.w3.org/2000/svg';
  const ICONS = {
    grid: 'M4 4h6v6H4zM14 4h6v6h-6zM14 14h6v6h-6zM4 14h6v6H4z',
    ticket: 'M3 8a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v1.5a2.5 2.5 0 0 0 0 5V16a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-1.5a2.5 2.5 0 0 0 0-5zM14 6v2M14 11v2M14 16v2',
    users: 'M16 20v-1.5a3.5 3.5 0 0 0-3.5-3.5h-5A3.5 3.5 0 0 0 4 18.5V20M10 11.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM20 20v-1.5a3.5 3.5 0 0 0-2.5-3.35M15.5 4.65a3.5 3.5 0 0 1 0 6.7',
    list: 'M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01',
    database: 'M12 8.5c4.4 0 8-1.3 8-3s-3.6-3-8-3-8 1.3-8 3 3.6 3 8 3zM4 5.5v13c0 1.7 3.6 3 8 3s8-1.3 8-3v-13M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3',
    logout: 'M9 20H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h3M16 16l4-4-4-4M20 12H9',
    plus: 'M12 5v14M5 12h14',
    copy: 'M9 9h10v10H9zM15 9V6a1 1 0 0 0-1-1H6a1 1 0 0 0-1 1v8a1 1 0 0 0 1 1h3',
    refresh: 'M20 12a8 8 0 1 1-2.34-5.66L20 8.5M20 4v4.5h-4.5',
    search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4',
    download: 'M20 15v3a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2v-3M8 11l4 4 4-4M12 15V4',
    key: 'M15.5 7.5l2.5 2.5M19 4l-6.6 6.6M21 2l-2 2M9 20a5 5 0 1 0 0-10 5 5 0 0 0 0 10z',
    x: 'M17 7L7 17M7 7l10 10',
    check: 'M20 6L9 17l-5-5',
    alert: 'M12 9v4M12 17h.01M10.3 3.9L2.4 17.5A2 2 0 0 0 4.1 20.5h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
    info: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 16v-4M12 8h.01',
    inbox: 'M22 12h-6l-2 3h-4l-2-3H2M5.5 5.1L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z',
    activity: 'M22 12h-4l-3 9L9 3l-3 9H2',
    layers: 'M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5',
    logo: 'M7 17V7l5 6 5-6v10',
    server: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
    terminal: 'M4 17l6-6-6-6M12 19h8',
    back: 'M19 12H5M12 19l-7-7 7-7',
    upgrade: 'M12 19V5M5 12l7-7 7 7',
    trash: 'M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6',
    play: 'M6 4l14 8-14 8z',
    eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
    lock: 'M5 11h14v10H5zM8 11V7a4 4 0 0 1 8 0v4',
  };

  function icon(name, size = 18, width = 1.8) {
    const svg = document.createElementNS(SVG, 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('width', size);
    svg.setAttribute('height', size);
    svg.setAttribute('class', 'icon');
    svg.setAttribute('aria-hidden', 'true');
    const p = document.createElementNS(SVG, 'path');
    p.setAttribute('d', ICONS[name]);
    p.setAttribute('fill', 'none');
    p.setAttribute('stroke', 'currentColor');
    p.setAttribute('stroke-width', width);
    p.setAttribute('stroke-linecap', 'round');
    p.setAttribute('stroke-linejoin', 'round');
    svg.append(p);
    return svg;
  }

  function button(label, opts = {}) {
    const cls = ['btn'];
    if (opts.kind) cls.push('btn-' + opts.kind);
    if (opts.small) cls.push('btn-sm');
    if (opts.block) cls.push('btn-block');
    return h('button', {
      class: cls.join(' '), type: opts.type || 'button', onclick: opts.onclick,
      disabled: opts.disabled, title: opts.title,
    }, opts.icon ? icon(opts.icon, 16) : null, label);
  }

  function iconButton(name, label, onclick) {
    return h('button', { class: 'icon-btn', type: 'button', title: label, 'aria-label': label, onclick }, icon(name, 16));
  }

  function brand() {
    return h('div', { class: 'brand' },
      h('div', { class: 'logo' }, icon('logo', 20, 2.4)),
      h('div', null, h('div', { class: 'brand-name' }, 'MoeFrp'), h('div', { class: 'brand-sub' }, '管理后台')));
  }

  function badge(text, kind) {
    return h('span', { class: 'badge' + (kind ? ' ' + kind : '') }, text);
  }

  function field(label, input, hint) {
    return h('label', { class: 'field' }, h('span', { class: 'field-label' }, label), input, hint ? h('span', { class: 'field-hint' }, hint) : null);
  }

  function input(attrs) {
    return h('input', Object.assign({ class: 'input', spellcheck: 'false' }, attrs));
  }

  function segmented(options, value, onchange, opts = {}) {
    const el = h('div', { class: 'seg', role: 'group', 'aria-label': opts.label });
    const set = (v) => {
      for (const b of el.children) b.setAttribute('aria-pressed', String(b.dataset.value === v));
    };
    for (const [v, text] of options) {
      const b = h('button', { type: 'button', disabled: opts.disabled }, text);
      b.dataset.value = v;
      b.addEventListener('click', () => {
        if (b.getAttribute('aria-pressed') === 'true') return;
        onchange(v, set);
      });
      el.append(b);
    }
    set(value);
    el.setValue = set;
    return el;
  }

  function searchBox(placeholder, oninput) {
    const inp = input({ type: 'search', placeholder, 'aria-label': placeholder });
    inp.addEventListener('input', () => oninput(inp.value.trim().toLowerCase()));
    return h('div', { class: 'search' }, icon('search', 16), inp);
  }

  function stateBox(iconName, title, text, action) {
    return h('div', { class: 'state' }, icon(iconName, 28, 1.5), h('div', { class: 'state-title' }, title), text ? h('div', null, text) : null, action);
  }

  function loadingBox() {
    return h('div', { class: 'state', 'aria-busy': 'true' }, h('div', { class: 'spinner' }), h('div', null, '加载中…'));
  }

  function errorBox(err, retry) {
    const el = stateBox('alert', '加载失败', errText(err), retry ? button('重试', { icon: 'refresh', onclick: retry }) : null);
    el.classList.add('error');
    return el;
  }

  function table(columns, rows) {
    return h('div', { class: 'table-wrap' }, h('table', null,
      h('thead', null, h('tr', null, columns.map((c) => h('th', { class: c.cls }, c.label)))),
      h('tbody', null, rows)));
  }

  // ---------- 格式化 ----------

  const dtf = new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  });
  const df = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' });

  const fmtTime = (s) => (s ? dtf.format(new Date(s)) : '—');
  const fmtDate = (s) => (s ? df.format(new Date(s)) : '—');

  function relTime(s) {
    const diff = (Date.now() - new Date(s).getTime()) / 1000;
    const a = Math.abs(diff);
    const suffix = diff >= 0 ? '前' : '后';
    if (a < 45) return diff >= 0 ? '刚刚' : '马上';
    if (a < 3600) return Math.round(a / 60) + ' 分钟' + suffix;
    if (a < 86400) return Math.round(a / 3600) + ' 小时' + suffix;
    if (a < 86400 * 30) return Math.round(a / 86400) + ' 天' + suffix;
    return fmtDate(s);
  }

  function timeEl(s) {
    if (!s) return h('span', { class: 'faint' }, '—');
    return h('time', { datetime: s, title: fmtTime(s), class: 'nowrap' }, relTime(s));
  }

  function fmtBytes(n) {
    n = Number(n) || 0;
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) {
      n /= 1024;
      i++;
    }
    return (i === 0 ? n : n.toFixed(n >= 100 ? 0 : 1)) + ' ' + units[i];
  }

  const fmtRate = (n) => fmtBytes(n) + '/s';

  function fmtUptime(sec) {
    sec = Number(sec) || 0;
    const d = Math.floor(sec / 86400);
    const h = Math.floor((sec % 86400) / 3600);
    const m = Math.floor((sec % 3600) / 60);
    if (d) return d + ' 天 ' + h + ' 小时';
    if (h) return h + ' 小时 ' + m + ' 分钟';
    return m + ' 分钟';
  }

  function fmtSeconds(s) {
    if (s < 60) return s + ' 秒';
    if (s < 3600) return Math.ceil(s / 60) + ' 分钟';
    return Math.ceil(s / 3600) + ' 小时';
  }

  // ---------- 接口 ----------

  class ApiError extends Error {
    constructor(e, status) {
      super(e.message || e.code);
      this.code = e.code;
      this.status = status;
      this.details = e.details || {};
    }
  }

  async function api(method, path, body, opts = {}) {
    const init = { method, credentials: 'same-origin', headers: { 'X-MoeFrp-Admin': '1' } };
    if (body !== undefined) {
      init.headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(API + path, init);
    } catch (e) {
      throw new ApiError({ code: 'network', message: '连不上服务器，请检查网络' }, 0);
    }
    if (res.ok && opts.raw) return res;
    if (res.status === 204) return null;
    let data = null;
    if ((res.headers.get('Content-Type') || '').includes('json')) {
      try { data = await res.json(); } catch (e) { /* 忽略 */ }
    }
    if (!res.ok) {
      const err = new ApiError((data && data.error) || { code: 'http_' + res.status, message: '请求失败（HTTP ' + res.status + '）' }, res.status);
      // 只有会话失效才回登录页；密码输错（invalid_credentials）也是 401，不能当成退出
      const sessionGone = ['unauthorized', 'session_expired', 'session_replaced'].includes(err.code);
      if (res.status === 401 && sessionGone && !opts.noRedirect && state.me) {
        err.handled = true;
        state.me = null;
        toast('登录已过期，请重新登录', 'error');
        showLogin();
      }
      // 敏感操作：先让管理员重新输入密码，成功后重发一次
      if (err.code === 'reauth_required' && !opts.noReauth) {
        if (await reauthDialog()) return api(method, path, body, Object.assign({}, opts, { noReauth: true }));
        err.handled = true;
      }
      throw err;
    }
    return data;
  }

  function errText(err) {
    if (err instanceof ApiError) {
      const fields = err.details.fields;
      if (Array.isArray(fields) && fields.length) return fields.map((f) => f.message).join('；');
      const wait = err.details.retryAfter;
      if (wait) return err.message + '（' + fmtSeconds(wait) + '后再试）';
      return err.message || err.code;
    }
    return String((err && err.message) || err);
  }

  function fail(err) {
    if (!err || !err.handled) toast(errText(err), 'error');
  }

  // 按钮执行异步操作时显示加载状态，防止重复点击
  async function busy(btn, fn) {
    if (btn.disabled) return;
    btn.disabled = true;
    btn.classList.add('loading');
    try {
      return await fn();
    } finally {
      btn.disabled = false;
      btn.classList.remove('loading');
    }
  }

  // ---------- 提示、复制、对话框 ----------

  function toast(msg, kind = 'ok') {
    const t = h('div', { class: 'toast ' + kind, role: kind === 'error' ? 'alert' : 'status' }, icon(kind === 'error' ? 'alert' : 'check', 16), h('span', null, msg));
    $toasts.append(t);
    setTimeout(() => {
      t.classList.add('out');
      setTimeout(() => t.remove(), 220);
    }, kind === 'error' ? 5000 : 2400);
  }

  async function copyText(text, what = '已复制') {
    try {
      await navigator.clipboard.writeText(text);
    } catch (e) {
      // 非 HTTPS 下没有 clipboard API
      const ta = h('textarea', { class: 'sr-only', readonly: true });
      ta.value = text;
      document.body.append(ta);
      ta.select();
      const ok = document.execCommand('copy');
      ta.remove();
      if (!ok) {
        toast('复制失败，请手动选择复制', 'error');
        return;
      }
    }
    toast(what);
  }

  // openDialog 打开模态框。onSubmit 存在时整个框是一个表单，回车即提交。
  function openDialog({ title, titleIcon, body, actions, wide, onSubmit, onClose }) {
    const dlg = h('dialog', { class: 'dialog' + (wide ? ' wide' : '') });
    const close = () => dlg.open && dlg.close();
    const form = h(onSubmit ? 'form' : 'div', { class: 'dialog-form', novalidate: !!onSubmit },
      h('div', { class: 'dialog-head' },
        h('h2', null, titleIcon ? icon(titleIcon, 18) : null, title),
        iconButton('x', '关闭', close)),
      h('div', { class: 'dialog-body' }, body),
      actions && actions.length ? h('div', { class: 'dialog-foot' }, actions) : null);
    if (onSubmit) {
      form.addEventListener('submit', (e) => {
        e.preventDefault();
        onSubmit(e.submitter || form.querySelector('[type=submit]'));
      });
    }
    dlg.append(form);
    // 点遮罩关闭（dialog 本身没有内边距，点到的是 dialog 说明点在了框外）
    dlg.addEventListener('mousedown', (e) => {
      if (e.target === dlg) dlg.dataset.downOutside = '1';
    });
    dlg.addEventListener('click', (e) => {
      if (e.target === dlg && dlg.dataset.downOutside) close();
      delete dlg.dataset.downOutside;
    });
    dlg.addEventListener('close', () => {
      dlg.remove();
      if (onClose) onClose();
    });
    document.body.append(dlg);
    dlg.showModal();
    const first = dlg.querySelector('input:not([disabled]), .dialog-foot .btn-primary, .dialog-foot .btn-danger');
    if (first) first.focus();
    return { dlg, close };
  }

  function confirmDialog({ title, message, ok = '确定', danger = false }) {
    return new Promise((resolve) => {
      let yes = false;
      const d = openDialog({
        title,
        titleIcon: danger ? 'alert' : null,
        body: h('p', { class: 'dialog-text' }, message),
        actions: [
          button('取消', { onclick: () => d.close() }),
          button(ok, { kind: danger ? 'danger' : 'primary', onclick: () => { yes = true; d.close(); } }),
        ],
        onClose: () => resolve(yes),
      });
    });
  }

  // ---------- 状态与路由 ----------

  const state = { me: null, page: null, gen: 0 };

  const PAGES = [
    { id: 'overview', label: '概览', icon: 'grid', desc: '用户、邀请码和客户端发布情况', render: pageOverview },
    { id: 'nodes', label: '节点', icon: 'server', desc: '在面板上管理 frps 节点：配置、监控、升级、执行命令、卸载', render: pageNodes },
    { id: 'invites', label: '邀请码', icon: 'ticket', desc: '注册需要邀请码，每个码只能用一次', render: pageInvites },
    { id: 'users', label: '用户', icon: 'users', desc: '调整角色、停用账号、踢下线、生成密码重置码', render: pageUsers },
    { id: 'audit', label: '审计日志', icon: 'list', desc: '登录、注册、分享和管理操作的记录', render: pageAudit },
    { id: 'backup', label: '备份', icon: 'database', desc: '下载数据库快照', render: pageBackup },
  ];

  // 路由：#/页面[/参数...]，例如 #/nodes/<节点ID>
  function currentPage() {
    const [id, ...params] = location.hash.replace(/^#\/?/, '').split('/').map(decodeURIComponent);
    const page = PAGES.find((p) => p.id === id) || PAGES[0];
    return { page, params: page.id === id ? params.filter(Boolean) : [] };
  }

  // 切换页面（包括浏览器后退）或回到登录页时，关掉还开着的对话框
  function closeDialogs() {
    document.querySelectorAll('dialog[open]').forEach((d) => d.close());
  }

  window.addEventListener('hashchange', () => {
    closeDialogs();
    if (state.me) renderPage();
  });

  // ---------- 初始化与登录 ----------

  async function boot() {
    try {
      const setup = await api('GET', '/setup', undefined, { noRedirect: true });
      if (setup.required) return showSetup(setup.recovery);
    } catch (e) {
      $app.replaceChildren(errorBox(e, boot));
      return;
    }
    try {
      state.me = await api('GET', '/me', undefined, { noRedirect: true });
      showShell();
    } catch (e) {
      if (e.status === 401 || e.status === 403) showLogin();
      else $app.replaceChildren(errorBox(e, boot));
    }
  }

  function authCard(title, sub, note, fields, submitLabel, onSubmit) {
    closeDialogs();
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button(submitLabel, { kind: 'primary', block: true, type: 'submit' });
    const form = h('form', { novalidate: true }, fields, err, submit);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      err.classList.remove('show');
      await busy(submit, async () => {
        try {
          await onSubmit();
        } catch (ex) {
          err.textContent = errText(ex);
          err.classList.add('show');
        }
      });
    });
    $app.replaceChildren(h('main', { class: 'auth' }, h('div', { class: 'auth-card' }, brand(), h('h1', null, title), h('p', null, sub), note, form)));
    const first = form.querySelector('input');
    if (first) first.focus();
  }

  function showSetup(recovery) {
    const code = input({ name: 'code', autocomplete: 'off', placeholder: 'XXXX-XXXX-XXXX', class: 'input mono', required: true });
    const nick = input({ name: 'username', autocomplete: 'username', required: true });
    const pw = input({ name: 'password', type: 'password', autocomplete: 'new-password', required: true });
    const pw2 = input({ name: 'password2', type: 'password', autocomplete: 'new-password', required: true });
    const note = h('div', { class: 'auth-note' + (recovery ? ' warn' : '') },
      recovery
        ? '当前是恢复模式（admin_recovery = true）。创建完管理员后请把这个选项关掉并重启服务。'
        : ['初始化码在服务端启动日志里，Docker 部署可以用 ', h('code', null, 'docker compose logs moefrp'), ' 查看。']);
    authCard(recovery ? '创建新管理员' : '初始化管理后台', '还没有管理员账号，先创建一个。', note, [
      field('初始化码', code),
      field('管理员昵称', nick, '2–20 个字符，之后也用它登录桌面客户端'),
      field('密码', pw, '至少 8 位'),
      field('确认密码', pw2),
    ], '创建并登录', async () => {
      if (pw.value !== pw2.value) throw new Error('两次输入的密码不一致');
      state.me = await api('POST', '/setup', { code: code.value, nickname: nick.value, password: pw.value }, { noRedirect: true });
      location.hash = '#/invites';
      showShell();
      toast('管理员已创建，可以开始生成邀请码了');
    });
  }

  function showLogin() {
    const nick = input({ name: 'username', autocomplete: 'username', required: true });
    const pw = input({ name: 'password', type: 'password', autocomplete: 'current-password', required: true });
    authCard('登录', '只有管理员账号可以登录。', null, [field('昵称', nick), field('密码', pw)], '登录', async () => {
      state.me = await api('POST', '/login', { nickname: nick.value, password: pw.value }, { noRedirect: true });
      showShell();
    });
  }

  async function logout(btn) {
    await busy(btn, async () => {
      try {
        await api('POST', '/logout', undefined, { noRedirect: true });
      } catch (e) { /* 会话已经失效也算退出成功 */ }
      state.me = null;
      showLogin();
    });
  }

  // ---------- 框架 ----------

  let $nav, $title, $desc, $actions, $body;

  function showShell() {
    const me = state.me;
    $nav = h('nav', { class: 'nav', 'aria-label': '主导航' }, PAGES.map((p) => h('a', { href: '#/' + p.id }, icon(p.icon, 18), p.label)));
    const out = iconButton('logout', '退出登录', () => logout(out));
    const sidebar = h('aside', { class: 'sidebar' },
      brand(), $nav,
      h('div', { class: 'sidebar-foot' },
        h('div', { class: 'avatar', 'aria-hidden': 'true' }, Array.from(me.nickname)[0] || '?'),
        h('div', { class: 'who' }, h('div', { class: 'who-name', title: me.nickname }, me.nickname), h('div', { class: 'who-sub' }, '管理员 · ' + (me.version || 'dev'))),
        out));
    $title = h('h1');
    $desc = h('p');
    $actions = h('div', { class: 'page-actions' });
    $body = h('div');
    const main = h('main', { class: 'main' }, h('div', { class: 'main-inner' },
      h('header', { class: 'page-head' }, h('div', null, $title, $desc), $actions), $body));
    $app.replaceChildren(h('div', { class: 'layout' }, sidebar, main));
    renderPage();
  }

  function renderPage() {
    const { page, params } = currentPage();
    const gen = ++state.gen;
    for (const a of $nav.children) {
      if (a.getAttribute('href') === '#/' + page.id) a.setAttribute('aria-current', 'page');
      else a.removeAttribute('aria-current');
    }
    document.title = page.label + ' · MoeFrp 管理后台';
    $title.textContent = page.label;
    $desc.textContent = page.desc;
    $actions.replaceChildren();
    $body.replaceChildren();
    page.render({
      actions: $actions, body: $body, params,
      alive: () => gen === state.gen && state.me !== null,
      setHead: (title, desc) => {
        $title.replaceChildren(title);
        $desc.textContent = desc || '';
        document.title = (typeof title === 'string' ? title : page.label) + ' · MoeFrp 管理后台';
      },
    });
  }

  // ---------- 概览 ----------

  async function pageOverview(p) {
    const reload = () => renderPage();
    p.actions.append(
      button('刷新', { icon: 'refresh', onclick: reload }),
      button('生成邀请码', { kind: 'primary', icon: 'plus', onclick: () => createInvitesDialog() }));
    p.body.append(loadingBox());
    let d;
    try {
      d = await api('GET', '/overview');
    } catch (e) {
      if (p.alive()) p.body.replaceChildren(errorBox(e, reload));
      return;
    }
    if (!p.alive()) return;
    const s = d.stats;
    const stat = (label, iconName, value, sub, warn) => h('div', { class: 'card stat' + (warn ? ' warn' : '') },
      h('div', { class: 'stat-top' }, label, icon(iconName, 18)),
      h('div', { class: 'stat-value' }, String(value)),
      h('div', { class: 'stat-sub' }, sub));

    const normal = Math.max(0, s.users - s.admins - s.vips);
    const bar = (label, n, kind) => h('div', { class: 'bar-row' },
      h('span', { class: 'label' }, label),
      h('div', { class: 'bar' + (kind ? ' ' + kind : '') }, h('i')),
      h('span', { class: 'num' }, String(n)));
    const bars = h('div', { class: 'bars' },
      bar('普通用户', normal), bar('VIP', s.vips, 'violet'), bar('管理员', s.admins, 'accent'), bar('已停用', s.disabledUsers, 'danger'));
    // 宽度用 style 属性设置，CSP 允许（style-src 只限制样式表来源和 <style>，CSSOM 不受影响）
    const counts = [normal, s.vips, s.admins, s.disabledUsers];
    bars.querySelectorAll('.bar > i').forEach((el, i) => {
      el.style.width = s.users ? (counts[i] / s.users * 100).toFixed(1) + '%' : '0';
    });

    const c = d.client || {};
    const link = c.downloadUrl && /^https?:\/\//i.test(c.downloadUrl)
      ? h('a', { href: c.downloadUrl, target: '_blank', rel: 'noopener noreferrer' }, c.downloadUrl)
      : h('span', { class: 'faint' }, '未设置');
    const kv = (pairs) => h('dl', { class: 'kv' }, pairs.map(([k, v]) => [h('dt', null, k), h('dd', null, v)]));
    const unset = (v, text = '未设置') => v || h('span', { class: 'faint' }, text);

    p.body.replaceChildren(
      h('div', { class: 'stats' },
        stat('用户', 'users', s.users, '7 天内新注册 ' + s.registeredWeek),
        stat('已登录用户', 'activity', s.onlineUsers, '24 小时内活跃 ' + s.activeToday),
        stat('可用邀请码', 'ticket', s.invitesUnused, s.invitesUnused === 0 ? '用完了，生成一些吧' : '已使用 ' + s.invitesUsed, s.invitesUnused === 0),
        stat('云端配置', 'layers', s.profiles, '分享 ' + s.activeShares + ' · 订阅 ' + s.subscriptions)),
      h('div', { class: 'grid-2' },
        h('section', { class: 'card' },
          h('div', { class: 'card-head' }, h('h2', null, '用户构成'), h('span', null, '共 ' + s.users + ' 人')),
          h('div', { class: 'card-body' }, bars)),
        h('section', { class: 'card' },
          h('div', { class: 'card-head' }, h('h2', null, '客户端发布'), h('span', null, '在 config.toml 的 [client] 里修改')),
          h('div', { class: 'card-body' }, kv([
            ['服务端版本', h('code', null, d.serverVersion || 'dev')],
            ['最新客户端', c.latestVersion ? h('code', null, c.latestVersion) : unset('')],
            ['最低客户端', c.minVersion ? h('code', null, c.minVersion) : unset('', '不限制')],
            ['下载地址', link],
            ['单设备登录', d.singleSession ? '开启' : '关闭'],
            ['图源', (c.imageSources || []).length + ' 个'],
            ['公告', unset(c.announcement, '无')],
          ])))));
  }

  // ---------- 邀请码 ----------

  function createInvitesDialog(onDone) {
    const count = input({ type: 'number', min: 1, max: 200, value: '1', inputmode: 'numeric', required: true });
    const note = input({ maxlength: 200, placeholder: '例如：给群友小明' });
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button('生成', { kind: 'primary', type: 'submit' });
    const d = openDialog({
      title: '生成邀请码',
      titleIcon: 'ticket',
      body: [field('数量', count, '一次最多 200 个'), field('备注', note, '可选，只有管理员能看到，方便以后对账'), err],
      actions: [button('取消', { onclick: () => d.close() }), submit],
      onSubmit: () => busy(submit, async () => {
        err.classList.remove('show');
        try {
          const n = Number(count.value);
          if (!Number.isInteger(n) || n < 1 || n > 200) throw new Error('数量要在 1 到 200 之间');
          const res = await api('POST', '/invites', { count: n, note: note.value });
          d.close();
          showCodes(res.codes);
          if (onDone) onDone();
        } catch (e) {
          if (e.handled) return;
          err.textContent = errText(e);
          err.classList.add('show');
        }
      }),
    });
    count.select();
  }

  function showCodes(codes) {
    const text = codes.join('\n');
    const d = openDialog({
      title: '已生成 ' + codes.length + ' 个邀请码',
      titleIcon: 'check',
      body: [h('p', { class: 'dialog-text' }, '发给要注册的人即可，每个码只能注册一个账号。之后也可以在列表里找到它们。'), h('div', { class: 'codes' }, text)],
      actions: [button('复制全部', { icon: 'copy', onclick: () => copyText(text, '已复制 ' + codes.length + ' 个邀请码') }), button('完成', { kind: 'primary', onclick: () => d.close() })],
    });
  }

  const INVITE_STATUS = { unused: ['未使用', 'ok'], used: ['已使用', null], revoked: ['已撤销', 'danger'] };

  async function pageInvites(p) {
    let status = 'unused';
    let q = '';
    let list = [];
    const box = h('div', { class: 'card' });

    p.actions.append(
      button('刷新', { icon: 'refresh', onclick: () => load() }),
      button('生成邀请码', { kind: 'primary', icon: 'plus', onclick: () => createInvitesDialog(() => load()) }));
    p.body.append(
      h('div', { class: 'toolbar' },
        segmented([['unused', '未使用'], ['used', '已使用'], ['revoked', '已撤销'], ['all', '全部']], status, (v, set) => {
          status = v;
          set(v);
          load();
        }, { label: '按状态筛选' }),
        searchBox('搜索邀请码、备注、使用者', (v) => { q = v; draw(); })),
      box);

    async function load() {
      box.replaceChildren(loadingBox());
      try {
        const res = await api('GET', '/invites?status=' + status);
        if (!p.alive()) return;
        list = res.invites || [];
        draw();
      } catch (e) {
        if (p.alive()) box.replaceChildren(errorBox(e, load));
      }
    }

    function draw() {
      const rows = list.filter((i) => !q || [i.code, i.note, i.usedBy].some((x) => (x || '').toLowerCase().includes(q)));
      if (!rows.length) {
        box.replaceChildren(list.length
          ? stateBox('search', '没有匹配的邀请码', '换个关键词试试')
          : stateBox('inbox', status === 'unused' ? '没有可用的邀请码' : '这里还是空的', status === 'unused' ? '点右上角「生成邀请码」' : null));
        return;
      }
      box.replaceChildren(
        table([{ label: '邀请码' }, { label: '备注' }, { label: '状态' }, { label: '创建于' }, { label: '使用情况' }, { label: '', cls: 'actions' }],
          rows.map((i) => {
            const [label, kind] = INVITE_STATUS[i.status] || [i.status];
            let usage = h('span', { class: 'faint' }, '—');
            if (i.status === 'used') {
              usage = h('div', null, i.usedBy ? h('div', null, i.usedBy) : h('div', { class: 'faint' }, '（用户已删除）'), h('div', { class: 'cell-sub' }, timeEl(i.usedAt)));
            } else if (i.status === 'revoked') {
              usage = h('div', { class: 'cell-sub' }, '撤销于 ', timeEl(i.revokedAt));
            }
            const revoke = i.status === 'unused'
              ? button('撤销', { small: true, kind: 'danger-ghost', onclick: (e) => revokeInvite(i, e.currentTarget) })
              : null;
            return h('tr', null,
              h('td', null, h('span', { class: 'code-cell' }, h('code', null, i.code), iconButton('copy', '复制', () => copyText(i.code)))),
              h('td', null, i.note || h('span', { class: 'faint' }, '—')),
              h('td', null, badge(label, kind)),
              h('td', null, timeEl(i.createdAt)),
              h('td', null, usage),
              h('td', { class: 'actions' }, revoke));
          })),
        h('div', { class: 'card-foot' }, q ? '匹配 ' + rows.length + ' / ' + list.length + ' 个' : '共 ' + list.length + ' 个'));
    }

    async function revokeInvite(i, btn) {
      const ok = await confirmDialog({ title: '撤销邀请码', message: '撤销后 ' + i.code + ' 不能再用来注册，这个操作不能撤回。', ok: '撤销', danger: true });
      if (!ok) return;
      await busy(btn, async () => {
        try {
          await api('DELETE', '/invites/' + encodeURIComponent(i.code));
          toast('已撤销 ' + i.code);
          load();
        } catch (e) {
          fail(e);
        }
      });
    }

    load();
  }

  // ---------- 用户 ----------

  const ROLES = { admin: ['管理员', 'accent'], vip: ['VIP', 'violet'], user: ['普通用户', null] };

  async function pageUsers(p) {
    let filter = 'all';
    let q = '';
    let list = [];
    let me = '';
    const box = h('div', { class: 'card' });

    p.actions.append(button('刷新', { icon: 'refresh', onclick: () => load() }));
    p.body.append(
      h('div', { class: 'toolbar' },
        segmented([['all', '全部'], ['admin', '管理员'], ['vip', 'VIP'], ['user', '普通用户'], ['disabled', '已停用']], filter, (v, set) => {
          filter = v;
          set(v);
          draw();
        }, { label: '按角色筛选' }),
        searchBox('搜索昵称', (v) => { q = v; draw(); })),
      box);

    async function load() {
      if (!list.length) box.replaceChildren(loadingBox());
      try {
        const res = await api('GET', '/users');
        if (!p.alive()) return;
        list = res.users || [];
        me = res.me;
        draw();
      } catch (e) {
        if (p.alive()) box.replaceChildren(errorBox(e, load));
      }
    }

    function draw() {
      const rows = list.filter((u) => {
        if (filter === 'disabled' ? u.status !== 'disabled' : filter !== 'all' && u.role !== filter) return false;
        return !q || u.nickname.toLowerCase().includes(q);
      });
      if (!rows.length) {
        box.replaceChildren(stateBox(list.length ? 'search' : 'inbox', list.length ? '没有匹配的用户' : '还没有用户', list.length ? null : '生成邀请码发给别人注册'));
        return;
      }
      box.replaceChildren(
        table([{ label: '用户' }, { label: '角色' }, { label: '状态' }, { label: '客户端' }, { label: '最近活跃' }, { label: '云端数据' }, { label: '注册于' }, { label: '', cls: 'actions' }],
          rows.map((u) => {
            const [roleLabel, roleKind] = ROLES[u.role] || [u.role];
            const isMe = u.nickname === me;
            return h('tr', null,
              h('td', null, h('div', { class: 'user-cell' },
                h('div', { class: 'avatar', 'aria-hidden': 'true' }, Array.from(u.nickname)[0] || '?'),
                u.nickname, isMe ? badge('你', 'outline') : null)),
              h('td', null, badge(roleLabel, roleKind)),
              h('td', null, u.status === 'disabled' ? badge('已停用', 'danger') : badge('正常', 'ok')),
              h('td', { class: 'nowrap' }, u.activeSessions > 0
                ? [h('span', { class: 'dot ok' }), u.activeSessions + ' 个会话']
                : h('span', { class: 'faint' }, '未登录')),
              h('td', null, timeEl(u.lastSeen)),
              h('td', { class: 'nowrap muted' }, '配置 ' + u.profiles + ' · 分享 ' + u.shares + ' · 订阅 ' + u.subscriptions),
              h('td', { class: 'nowrap' }, h('time', { datetime: u.createdAt, title: fmtTime(u.createdAt) }, fmtDate(u.createdAt))),
              h('td', { class: 'actions' }, button('管理', { small: true, onclick: () => manageUser(u, isMe, load) })));
          })),
        h('div', { class: 'card-foot' }, rows.length === list.length ? '共 ' + list.length + ' 人' : '显示 ' + rows.length + ' / ' + list.length + ' 人'));
    }

    load();
  }

  function manageUser(u, isMe, reload) {
    const nick = encodeURIComponent(u.nickname);
    let changed = false;

    const setting = (title, desc, control) => h('div', { class: 'setting' },
      h('div', { class: 'setting-text' }, h('div', { class: 'setting-title' }, title), h('div', { class: 'setting-desc' }, desc)), control);

    // 角色
    const roleSeg = segmented([['user', '普通用户'], ['vip', 'VIP'], ['admin', '管理员']], u.role, async (v, set) => {
      let message = '把 ' + u.nickname + ' 的角色改为「' + ROLES[v][0] + '」？';
      if (v === 'admin') message = '管理员可以登录这个后台，管理所有用户和邀请码。确定把 ' + u.nickname + ' 设为管理员？';
      else if (u.role === 'admin') message = '降级后 ' + u.nickname + ' 的管理后台登录会立即失效。确定继续？';
      if (!(await confirmDialog({ title: '修改角色', message, ok: '修改', danger: v === 'admin' || u.role === 'admin' }))) return;
      try {
        await api('PATCH', '/users/' + nick, { role: v });
        u.role = v;
        set(v);
        changed = true;
        toast('已把 ' + u.nickname + ' 改为' + ROLES[v][0]);
      } catch (e) {
        fail(e);
      }
    }, { label: '角色', disabled: isMe });

    // 停用 / 启用
    const statusBtn = h('div');
    const drawStatus = () => {
      const disabled = u.status === 'disabled';
      statusBtn.replaceChildren(button(disabled ? '启用账号' : '停用账号', {
        small: true, kind: disabled ? null : 'danger-ghost', disabled: isMe,
        onclick: (e) => toggleDisabled(e.currentTarget),
      }));
    };
    async function toggleDisabled(btn) {
      const disable = u.status !== 'disabled';
      if (disable && !(await confirmDialog({
        title: '停用账号', danger: true, ok: '停用',
        message: '停用后 ' + u.nickname + ' 的所有登录会立即失效，重新启用之前都不能登录。账号数据会保留。',
      }))) return;
      await busy(btn, async () => {
        try {
          await api('PATCH', '/users/' + nick, { disabled: disable });
          u.status = disable ? 'disabled' : 'active';
          changed = true;
          toast(disable ? '已停用 ' + u.nickname : '已启用 ' + u.nickname);
        } catch (e) {
          fail(e);
        }
      });
      drawStatus();
    }
    drawStatus();

    const kickBtn = button('踢下线', {
      small: true,
      onclick: async (e) => {
        const btn = e.currentTarget;
        await busy(btn, async () => {
          try {
            const res = await api('POST', '/users/' + nick + '/kick');
            changed = true;
            toast(res.revokedSessions ? '已注销 ' + res.revokedSessions + ' 个会话' : u.nickname + ' 当前没有登录的客户端');
          } catch (ex) {
            fail(ex);
          }
        });
      },
    });

    const secretBox = h('div');
    const resetBtn = button('生成重置码', {
      small: true, icon: 'key',
      onclick: async (e) => {
        await busy(e.currentTarget, async () => {
          try {
            const res = await api('POST', '/users/' + nick + '/reset-code');
            secretBox.replaceChildren(h('div', { class: 'secret' },
              h('div', { class: 'secret-row' }, h('code', null, res.code), button('复制', { small: true, icon: 'copy', onclick: () => copyText(res.code) })),
              h('p', null, '把重置码发给 ' + u.nickname + '，在客户端登录页的「忘记密码」里用它设置新密码。', '有效期至 ' + fmtTime(res.expiresAt) + '，只显示这一次；再生成一个会让旧的失效。')));
          } catch (ex) {
            fail(ex);
          }
        });
      },
    });

    const [roleLabel, roleKind] = ROLES[u.role] || [u.role];
    openDialog({
      title: u.nickname,
      wide: true,
      body: [
        h('div', { class: 'chips manage-head' }, badge(roleLabel, roleKind), u.status === 'disabled' ? badge('已停用', 'danger') : badge('正常', 'ok'), h('span', { class: 'cell-sub' }, '注册于 ' + fmtDate(u.createdAt))),
        isMe ? h('div', { class: 'callout info', role: 'note' }, icon('info', 16), '这是你自己的账号。为了防止把自己锁在外面，角色和停用不能在这里改。') : null,
        h('div', null,
          setting('角色', 'VIP 可以使用更多功能，管理员可以登录这个后台', roleSeg),
          setting('账号状态', u.status === 'disabled' ? '账号已停用，不能登录' : '停用后不能登录，数据保留', statusBtn),
          setting('踢下线', '注销 ' + u.nickname + ' 在桌面客户端上的所有登录，不影响账号本身', kickBtn),
          setting('密码重置', '用户忘记密码时，生成一次性重置码发给对方', resetBtn)),
        secretBox,
      ],
      onClose: () => { if (changed) reload(); },
    });
  }

  // ---------- 审计日志 ----------

  const ACTIONS = {
    'auth.register': ['注册', null],
    'auth.login': ['登录', null],
    'auth.login_failed': ['登录失败', 'danger'],
    'auth.change_password': ['修改密码', null],
    'auth.reset_password': ['重置密码', 'warn'],
    'admin.invite_create': ['生成邀请码', 'accent'],
    'admin.invite_revoke': ['撤销邀请码', 'accent'],
    'admin.set_role': ['修改角色', 'accent'],
    'admin.disable': ['停用账号', 'danger'],
    'admin.enable': ['启用账号', 'accent'],
    'admin.reset_code': ['生成重置码', 'accent'],
    'admin.kick': ['踢下线', 'accent'],
    'admin.setup': ['初始化管理员', 'accent'],
    'admin.login': ['登录后台', 'accent'],
    'admin.backup': ['下载备份', 'accent'],
    'share.create': ['创建分享', 'info'],
    'share.republish': ['更新分享', 'info'],
    'share.revoke': ['撤销分享', 'info'],
  };

  const DETAIL_KEYS = {
    reason: '原因', kind: '类型', session: '会话', count: '数量', note: '备注', via: '途径',
    role: '角色', from: '原', to: '新', replaced: '挤下线', revoked: '注销', revokedSessions: '注销会话',
    invite: '邀请码', device: '设备', install: '安装', expiresAt: '有效期至', rehash: '升级哈希',
  };
  const TIME_KEYS = new Set(['expiresAt']);

  async function pageAudit(p) {
    let prefix = '';
    const box = h('div', { class: 'card' });
    p.actions.append(button('刷新', { icon: 'refresh', onclick: () => load() }));
    p.body.append(
      h('div', { class: 'toolbar' },
        segmented([['', '全部'], ['admin.', '管理操作'], ['auth.', '账号'], ['share.', '分享']], prefix, (v, set) => {
          prefix = v;
          set(v);
          load();
        }, { label: '按类型筛选' })),
      box);

    async function load() {
      box.replaceChildren(loadingBox());
      try {
        const res = await api('GET', '/audit?limit=200&action=' + encodeURIComponent(prefix));
        if (!p.alive()) return;
        draw(res.entries || []);
      } catch (e) {
        if (p.alive()) box.replaceChildren(errorBox(e, load));
      }
    }

    function detail(d) {
      const keys = Object.keys(d || {});
      if (!keys.length) return h('span', { class: 'faint' }, '—');
      return h('div', { class: 'chips' }, keys.map((k) => {
        const v = d[k];
        let text = typeof v === 'object' ? JSON.stringify(v) : String(v);
        if (TIME_KEYS.has(k) && typeof v === 'number') text = fmtTime(v * 1000);
        return h('span', { class: 'chip', title: k + ': ' + text }, h('b', null, DETAIL_KEYS[k] || k), text);
      }));
    }

    function draw(list) {
      if (!list.length) {
        box.replaceChildren(stateBox('inbox', '没有记录', null));
        return;
      }
      box.replaceChildren(
        table([{ label: '时间' }, { label: '事件' }, { label: '操作者' }, { label: '对象' }, { label: 'IP' }, { label: '详情' }],
          list.map((e) => {
            let [label, kind] = ACTIONS[e.action] || [e.action, null];
            if (e.action === 'auth.login' && e.detail && e.detail.kind === 'admin_web') [label, kind] = ['登录后台', 'accent'];
            return h('tr', null,
              h('td', { class: 'nowrap' }, h('time', { datetime: e.at, title: relTime(e.at) }, fmtTime(e.at))),
              h('td', { class: 'nowrap' }, badge(label, kind)),
              h('td', null, e.actor || h('span', { class: 'faint' }, '—')),
              h('td', { class: 'nowrap' }, e.target || h('span', { class: 'faint' }, '—')),
              h('td', { class: 'nowrap' }, e.ip ? h('code', null, e.ip) : h('span', { class: 'faint' }, '—')),
              h('td', null, detail(e.detail)));
          })),
        h('div', { class: 'card-foot' }, list.length >= 200 ? '显示最近 200 条' : '共 ' + list.length + ' 条'));
    }

    load();
  }

  // ---------- 备份 ----------

  function pageBackup(p) {
    const dl = button('下载备份', {
      kind: 'primary', icon: 'download',
      onclick: () => busy(dl, async () => {
        try {
          const res = await api('GET', '/backup', undefined, { raw: true });
          const blob = await res.blob();
          const m = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') || '');
          const a = h('a', { href: URL.createObjectURL(blob), download: m ? m[1] : 'moefrp-backup.db' });
          document.body.append(a);
          a.click();
          a.remove();
          setTimeout(() => URL.revokeObjectURL(a.href), 10000);
          toast('备份已下载（' + fmtBytes(blob.size) + '）');
        } catch (e) {
          fail(e);
        }
      }),
    });
    p.body.append(h('div', { class: 'backup' },
      h('section', { class: 'card backup-main' },
        h('div', { class: 'backup-icon' }, icon('database', 22)),
        h('div', null,
          h('h2', null, '下载数据库备份'),
          h('p', null, '生成一份当前数据库的完整快照（SQLite 文件），服务不用停。'),
          h('div', { class: 'callout warn' }, icon('alert', 16), h('span', null, '备份里有所有用户的密码哈希、节点地址和 token，请像对待密码一样保管，不要发到群里。')),
          dl)),
      h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, '怎么恢复')),
        h('div', { class: 'card-body' }, h('ol', { class: 'steps' },
          h('li', null, '停止服务：', h('code', null, 'docker compose stop moefrp')),
          h('li', null, '用备份文件替换数据目录里的数据库文件（Docker 部署是 compose 同目录下的 ', h('code', null, 'data/moefrp.db'), '）'),
          h('li', null, '启动服务：', h('code', null, 'docker compose start moefrp'))))),
      h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, '自动备份')),
        h('div', { class: 'card-body muted' },
          '服务端带 ', h('code', null, '--auto-migrate'), ' 启动且需要升级数据库结构时，会先在数据库旁边自动保存一份 ',
          h('code', null, '*.pre-migrate-时间.db'), '。日常备份建议定期在这里下载，或者备份整个数据卷。'))));
  }

  // ---------- 二次验证 ----------

  // reauthDialog 让管理员重新输入密码，15 分钟内的敏感操作不用再输。返回是否成功。
  function reauthDialog() {
    return new Promise((resolve) => {
      let ok = false;
      const pw = input({ type: 'password', autocomplete: 'current-password', required: true });
      const err = h('div', { class: 'form-error', role: 'alert' });
      const submit = button('确认', { kind: 'primary', type: 'submit' });
      const d = openDialog({
        title: '确认身份',
        titleIcon: 'lock',
        body: [h('p', { class: 'dialog-text' }, '这是敏感操作（执行命令、卸载、删除、重置令牌），请重新输入密码。之后 15 分钟内不用再输。'),
          h('div', { class: 'gap' }), field('密码', pw), err],
        actions: [button('取消', { onclick: () => d.close() }), submit],
        onSubmit: () => busy(submit, async () => {
          err.classList.remove('show');
          try {
            await api('POST', '/elevate', { password: pw.value }, { noRedirect: true, noReauth: true });
            ok = true;
            d.close();
          } catch (e) {
            err.textContent = errText(e);
            err.classList.add('show');
            pw.select();
          }
        }),
        onClose: () => resolve(ok),
      });
    });
  }

  // ---------- 节点 ----------

  const NODE_STATUS = { online: ['在线', 'ok'], offline: ['离线', 'danger'], pending: ['待安装', 'warn'] };
  const FRPS_STATE = { running: ['运行中', 'ok'], stopped: ['未启动', null], error: ['出错', 'danger'] };

  function nodeStatusBadge(n) {
    if (n.pendingUninstall) return badge('待卸载', 'warn');
    const [label, kind] = NODE_STATUS[n.status] || [n.status];
    return h('span', { class: 'badge ' + (kind || '') }, n.status === 'online' ? h('span', { class: 'dot ok' }) : null, label);
  }

  function meter(used, total) {
    const pct = total > 0 ? Math.min(100, (used / total) * 100) : 0;
    const el = h('div', { class: 'meter' + (pct > 90 ? ' danger' : pct > 75 ? ' warn' : '') }, h('i'));
    el.firstChild.style.width = pct.toFixed(1) + '%';
    return el;
  }

  function meterCell(label, used, total, text) {
    return h('div', { class: 'meter-row' }, h('span', { class: 'meter-label' }, label), meter(used, total), h('span', { class: 'meter-text' }, text));
  }

  function copyBox(text, label = '已复制') {
    return h('div', { class: 'copybox' }, h('code', null, text), button('复制', { small: true, icon: 'copy', onclick: () => copyText(text, label) }));
  }

  function installBlock(res) {
    const parts = [];
    if (res.installCommand) {
      parts.push(h('p', { class: 'dialog-text' }, '在节点上以 root 执行下面的命令（Debian / Ubuntu / 红帽系等 systemd 系统，amd64 或 arm64）：'));
      parts.push(copyBox(res.installCommand, '安装命令已复制'));
    } else {
      parts.push(h('p', { class: 'dialog-text' }, '无法确定面板地址，请在服务端配置 public_url。安装码：'), copyBox(res.enrollCode));
    }
    parts.push(h('ul', { class: 'hints' },
      h('li', null, '安装码 ', h('time', { datetime: res.expiresAt, title: fmtTime(res.expiresAt) }, relTime(res.expiresAt)), '失效，只能用一次。'),
      h('li', null, '不想让面板在这台机器上执行命令：在命令末尾加 ', h('code', null, '--no-exec'), '。'),
      h('li', null, '卸载：在面板上点"卸载"，或者在节点上执行 ', h('code', null, 'sudo moefrp-agent uninstall'), '。')));
    if (res.warning) parts.push(h('div', { class: 'callout warn' }, icon('alert', 16), h('span', null, res.warning)));
    return parts;
  }

  function createNodeDialog(onDone) {
    const name = input({ maxlength: 40, placeholder: '例如：东京 1', required: true });
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button('添加', { kind: 'primary', type: 'submit' });
    const d = openDialog({
      title: '添加节点',
      titleIcon: 'server',
      body: [field('名称', name, '只在面板上显示，之后可以改'), err],
      actions: [button('取消', { onclick: () => d.close() }), submit],
      onSubmit: () => busy(submit, async () => {
        err.classList.remove('show');
        try {
          const res = await api('POST', '/nodes', { name: name.value });
          d.close();
          showInstall(res, '节点「' + res.node.name + '」已添加');
          if (onDone) onDone(res.node);
        } catch (e) {
          if (e.handled) return;
          err.textContent = errText(e);
          err.classList.add('show');
        }
      }),
    });
  }

  function showInstall(res, title) {
    const d = openDialog({
      title,
      titleIcon: 'check',
      wide: true,
      body: installBlock(res),
      actions: [button('完成', { kind: 'primary', onclick: () => d.close() })],
    });
  }

  async function pageNodes(p) {
    if (p.params[0]) return pageNode(p, p.params[0]);
    const box = h('div', { class: 'card' });
    const notice = h('div');
    let data = null;
    p.actions.append(
      button('刷新', { icon: 'refresh', onclick: () => load() }),
      button('执行命令', { icon: 'terminal', onclick: () => data && execDialog(data.nodes) }),
      button('添加节点', { kind: 'primary', icon: 'plus', onclick: () => createNodeDialog(() => load()) }));
    p.body.append(notice, box);
    box.append(loadingBox());

    async function load(quiet) {
      try {
        const res = await api('GET', '/nodes');
        if (!p.alive()) return;
        data = res;
        draw();
      } catch (e) {
        if (!quiet && p.alive()) box.replaceChildren(errorBox(e, () => load()));
      }
    }

    function draw() {
      notice.replaceChildren(...(data.agent ? [] : [h('div', { class: 'callout warn mb' }, icon('alert', 16),
        h('span', null, '这个面板没有打包 agent 程序，安装和升级不可用。正式部署请使用 Docker 镜像。'))]));
      const list = data.nodes || [];
      if (!list.length) {
        box.replaceChildren(stateBox('server', '还没有节点', '添加一台 VPS，面板会在上面运行 frps，并负责配置、监控、升级和卸载。',
          button('添加节点', { kind: 'primary', icon: 'plus', onclick: () => createNodeDialog(() => load()) })));
        return;
      }
      box.replaceChildren(
        table([{ label: '节点' }, { label: '状态' }, { label: 'frps' }, { label: '资源' }, { label: '网络' }, { label: 'agent' }, { label: '', cls: 'actions' }],
          list.map((n) => {
            const r = n.report;
            const info = n.info || {};
            const fs = r && r.frps;
            const [fl, fk] = fs ? (FRPS_STATE[fs.state] || [fs.state]) : [null];
            const href = '#/nodes/' + encodeURIComponent(n.id);
            return h('tr', null,
              h('td', null, h('a', { class: 'node-link', href }, n.name),
                h('div', { class: 'cell-sub' }, [info.hostname, n.client && n.client.addr].filter(Boolean).join(' · ') || '—')),
              h('td', null, nodeStatusBadge(n),
                h('div', { class: 'cell-sub' }, n.status === 'online' ? '' : n.lastSeen ? ['最后在线 ', timeEl(n.lastSeen)] : '')),
              h('td', null, fs ? [badge(fl, fk), h('div', { class: 'cell-sub' }, fs.state === 'running' ? ':' + fs.bindPort + ' · ' + fs.clients + ' 个客户端' : (fs.error || ''))]
                : h('span', { class: 'faint' }, '—')),
              h('td', { class: 'res-cell' }, r ? [meterCell('CPU', r.cpu, 100, r.cpu.toFixed(0) + '%'), meterCell('内存', r.memUsed, r.memTotal, fmtBytes(r.memUsed))]
                : h('span', { class: 'faint' }, '—')),
              h('td', { class: 'nowrap' }, r ? h('div', { class: 'cell-sub' }, '↓ ' + fmtRate(n.netRxRate), h('br'), '↑ ' + fmtRate(n.netTxRate)) : h('span', { class: 'faint' }, '—')),
              h('td', { class: 'nowrap' }, info.agentVersion ? h('code', null, info.agentVersion) : h('span', { class: 'faint' }, '—'),
                n.upgradeTo ? h('div', null, badge('可升级', 'info')) : null),
              h('td', { class: 'actions' }, h('a', { class: 'btn btn-sm', href }, '管理')));
          })),
        h('div', { class: 'card-foot' }, '共 ' + list.length + ' 个节点，' + list.filter((n) => n.status === 'online').length + ' 个在线 · 每 10 秒自动刷新'));
    }

    await load();
    const timer = setInterval(() => (p.alive() ? load(true) : clearInterval(timer)), 10000);
  }

  // execDialog 在选中的节点上执行命令（批量），结果逐个显示。
  function execDialog(nodes, preselect) {
    const online = nodes.filter((n) => n.status === 'online');
    if (!online.length) {
      toast('没有在线的节点', 'error');
      return;
    }
    const checks = online.map((n) => {
      const cb = h('input', { type: 'checkbox', value: n.id });
      cb.checked = preselect ? preselect === n.id : online.length === 1;
      if (n.info && n.info.execEnabled === false) {
        cb.disabled = true;
        cb.checked = false;
      }
      return h('label', { class: 'check' }, cb, n.name, n.info && n.info.execEnabled === false ? h('span', { class: 'faint' }, '（已关闭远程命令）') : null);
    });
    const cmd = h('textarea', { class: 'input mono textarea', rows: 4, placeholder: '例如：df -h; systemctl status nginx', spellcheck: 'false' });
    const timeout = h('select', { class: 'input select' }, [['60', '1 分钟'], ['300', '5 分钟'], ['1800', '30 分钟'], ['3600', '1 小时']].map(([v, t]) => h('option', { value: v }, t)));
    const results = h('div', { class: 'exec-results' });
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button('执行', { kind: 'primary', type: 'submit', icon: 'play' });
    const d = openDialog({
      title: '执行命令',
      titleIcon: 'terminal',
      wide: true,
      body: [
        h('div', { class: 'callout warn' }, icon('alert', 16), h('span', null, '命令以 root 身份在节点上执行，会写入审计日志。')),
        h('div', { class: 'gap' }),
        online.length > 1 ? field('节点', h('div', { class: 'checks' }, checks)) : h('div', { class: 'checks', hidden: true }, checks),
        field('命令', cmd),
        field('超时', timeout),
        err, results,
      ],
      actions: [button('关闭', { onclick: () => d.close() }), submit],
      onSubmit: () => busy(submit, async () => {
        err.classList.remove('show');
        const ids = checks.map((c) => c.querySelector('input')).filter((c) => c.checked).map((c) => c.value);
        if (!ids.length) {
          err.textContent = '请至少选择一个节点';
          err.classList.add('show');
          return;
        }
        try {
          const res = await api('POST', '/nodes/exec', { nodes: ids, command: cmd.value, timeoutSec: Number(timeout.value) });
          results.replaceChildren(...res.tasks.map((t) => taskResult(t, nodes.find((n) => n.id === t.nodeId))));
        } catch (e) {
          if (e.handled) return;
          err.textContent = errText(e);
          err.classList.add('show');
        }
      }),
    });
    cmd.focus();
  }

  // taskResult 显示一个任务的结果，任务还在运行时每秒查一次。
  function taskResult(t, node) {
    const status = h('span');
    const out = h('pre', { class: 'term' });
    const el = h('div', { class: 'task' }, h('div', { class: 'task-head' }, h('b', null, node ? node.name : t.nodeId), status), out);
    const draw = (t) => {
      const st = t.status === 'running' ? badge('运行中', 'info') : t.status === 'done' ? badge('完成 · 退出码 0', 'ok')
        : badge('失败' + (t.exitCode != null ? ' · 退出码 ' + t.exitCode : ''), 'danger');
      status.replaceChildren(st);
      out.textContent = [t.output, t.error].filter(Boolean).join('\n') || (t.status === 'running' ? '…' : '（没有输出）');
    };
    draw(t);
    (async () => {
      for (let i = 0; i < 7200 && t.status === 'running'; i++) {
        await new Promise((r) => setTimeout(r, i < 10 ? 500 : 1500));
        if (!el.isConnected) return; // 对话框关了或者换了页面
        try {
          t = await api('GET', '/tasks/' + encodeURIComponent(t.id));
          draw(t);
        } catch (e) {
          return;
        }
      }
    })();
    return el;
  }

  async function pageNode(p, id) {
    let n = null;
    let tab = 'overview';
    const tabs = segmented([['overview', '概况'], ['config', 'frps 配置'], ['proxies', '代理'], ['logs', '日志'], ['exec', '命令'], ['settings', '设置']], tab, (v, set) => {
      tab = v;
      set(v);
      drawTab();
    }, { label: '节点页面' });
    const content = h('div');
    p.actions.append(button('刷新', { icon: 'refresh', onclick: () => load() }));
    p.body.append(h('a', { class: 'back', href: '#/nodes' }, icon('back', 16), '全部节点'), loadingBox());

    async function load(quiet) {
      try {
        const v = await api('GET', '/nodes/' + encodeURIComponent(id));
        if (!p.alive()) return;
        const first = !n;
        n = v;
        p.setHead(h('span', { class: 'title-row' }, n.name, nodeStatusBadge(n)), [n.info && n.info.hostname, n.client && n.client.addr].filter(Boolean).join(' · '));
        if (first) {
          p.body.replaceChildren(h('a', { class: 'back', href: '#/nodes' }, icon('back', 16), '全部节点'), h('div', { class: 'toolbar' }, tabs), content);
          drawTab();
        } else if (tab === 'overview') {
          drawTab();
        }
      } catch (e) {
        if (!quiet && p.alive()) p.body.replaceChildren(h('a', { class: 'back', href: '#/nodes' }, icon('back', 16), '全部节点'), errorBox(e, () => load()));
      }
    }

    function drawTab() {
      const fn = { overview: tabOverview, config: tabConfig, proxies: tabProxies, logs: tabLogs, exec: tabExec, settings: tabSettings }[tab];
      content.replaceChildren(fn());
    }

    const offlineBox = () => stateBox('server', '节点不在线', n.status === 'pending' ? '还没有安装 agent。' : '节点上线后才能查看。');

    function tabOverview() {
      const info = n.info || {};
      const r = n.report;
      const kv = (pairs) => h('dl', { class: 'kv' }, pairs.filter(Boolean).map(([k, v]) => [h('dt', null, k), h('dd', null, v == null || v === '' ? h('span', { class: 'faint' }, '—') : v)]));
      const cards = [];
      if (n.status === 'pending') {
        cards.push(h('section', { class: 'card span-2' }, h('div', { class: 'card-head' }, h('h2', null, '安装 agent')),
          h('div', { class: 'card-body' }, h('p', { class: 'muted' }, '这个节点还没有安装。添加节点时给出的安装命令 24 小时内有效；找不到或过期了就重新生成。'),
            h('div', { class: 'gap' }), button('重新生成安装命令', { kind: 'primary', onclick: (e) => resetToken(e.currentTarget, false) }))));
      }
      if (n.pendingUninstall) {
        cards.push(h('div', { class: 'callout warn span-2' }, icon('alert', 16), h('span', null, '已标记卸载：节点下次上线时会自动卸载并从面板删除。'),
          button('取消卸载', { small: true, onclick: async () => { await api('POST', '/nodes/' + encodeURIComponent(id) + '/cancel-uninstall').catch(fail); load(); } })));
      }
      if (n.client) {
        cards.push(h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, '客户端连接信息'), h('span', null, '在 MoeFrp 客户端的"节点"里填这些')),
          h('div', { class: 'card-body' }, kv([
            ['地址', n.client.addr ? copyBox(n.client.addr) : h('span', { class: 'faint' }, '节点上线后显示，或在"设置"里填域名')],
            ['端口', String(n.client.port)],
            ['token', n.client.token ? secretBox(n.client.token) : h('span', { class: 'faint' }, '未设置（任何人都能连）')],
          ]))));
      }
      const fs = r && r.frps;
      const [fl, fk] = fs ? (FRPS_STATE[fs.state] || [fs.state]) : ['—'];
      const sync = n.configRev === n.appliedRev ? badge('已同步', 'ok') : badge('未同步（面板 v' + n.configRev + ' / 节点 v' + n.appliedRev + '）', 'warn');
      cards.push(h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'frps')),
        h('div', { class: 'card-body' }, kv([
          ['状态', fs ? badge(fl, fk) : null],
          fs && fs.error ? ['错误', h('span', { class: 'text-danger' }, fs.error)] : null,
          ['配置', sync],
          n.applyError ? ['上次下发', h('span', { class: 'text-danger' }, n.applyError)] : null,
          ['监听端口', fs && fs.bindPort ? String(fs.bindPort) : null],
          ['客户端 / 代理 / 连接', fs ? fs.clients + ' / ' + fs.proxies + ' / ' + fs.conns : null],
          ['今日流量', fs ? '↓ ' + fmtBytes(fs.trafficIn) + ' · ↑ ' + fmtBytes(fs.trafficOut) : null],
          ['frp 版本', info.frpVersion ? h('code', null, info.frpVersion) : null],
        ]))));
      cards.push(h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, '资源')),
        h('div', { class: 'card-body' }, r ? [
          meterCell('CPU', r.cpu, 100, r.cpu.toFixed(1) + '%'),
          meterCell('内存', r.memUsed, r.memTotal, fmtBytes(r.memUsed) + ' / ' + fmtBytes(r.memTotal)),
          meterCell('磁盘', r.diskUsed, r.diskTotal, fmtBytes(r.diskUsed) + ' / ' + fmtBytes(r.diskTotal)),
          h('div', { class: 'gap' }),
          kv([['负载', r.load.map((x) => x.toFixed(2)).join(' / ')], ['网络', '↓ ' + fmtRate(n.netRxRate) + ' · ↑ ' + fmtRate(n.netTxRate)], ['运行时间', fmtUptime(r.uptime)]]),
        ] : h('p', { class: 'muted' }, '节点在线后显示'))));
      cards.push(h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, '系统')),
        h('div', { class: 'card-body' }, kv([
          ['系统', info.distro], ['内核', info.kernel], ['架构', info.arch ? info.os + '/' + info.arch : null], ['主机名', info.hostname],
          ['IP', n.lastIp], ['agent', info.agentVersion ? [h('code', null, info.agentVersion), n.upgradeTo ? [' ', badge('可升级到 ' + n.upgradeTo, 'info')] : null] : null],
          ['远程命令', info.agentVersion ? (info.execEnabled ? '允许' : '已关闭（--no-exec）') : null],
          ['上线时间', n.connectedAt ? timeEl(n.connectedAt) : null], ['最后在线', n.lastSeen ? timeEl(n.lastSeen) : null], ['添加于', fmtTime(n.createdAt)],
        ]))));
      return h('div', { class: 'grid-2' }, cards);
    }

    function secretBox(text) {
      const code = h('code', null, '••••••••••••');
      let shown = false;
      return h('div', { class: 'copybox' }, code,
        iconButton('eye', '显示', () => { shown = !shown; code.textContent = shown ? text : '••••••••••••'; }),
        button('复制', { small: true, icon: 'copy', onclick: () => copyText(text, 'token 已复制') }));
    }

    function tabConfig() {
      const ta = h('textarea', { class: 'input mono textarea code-editor', rows: 22, spellcheck: 'false', wrap: 'off' });
      ta.value = n.frpsConfig || '';
      const result = h('div');
      const save = button(n.status === 'online' ? '保存并应用' : '保存', {
        kind: 'primary',
        onclick: () => busy(save, async () => {
          try {
            const res = await api('PUT', '/nodes/' + encodeURIComponent(id) + '/config', { config: ta.value });
            const r = res.result;
            if (!res.pushed) {
              result.replaceChildren(h('div', { class: 'callout info' }, icon('info', 16), h('span', null, '已保存（v' + res.rev + '）。节点不在线，上线后自动下发。')));
            } else if (r.applied) {
              result.replaceChildren(h('div', { class: 'callout ok' }, icon('check', 16), h('span', null, '已生效（v' + res.rev + '），frps 已用新配置重启。' + (r.warning ? ' 提示：' + r.warning : ''))));
              toast('frps 配置已生效');
            } else {
              result.replaceChildren(h('div', { class: 'callout danger' }, icon('alert', 16),
                h('span', null, r.error + (r.rolledBack ? '。frps 已恢复为之前的配置继续运行。' : ''))));
            }
            const v = await api('GET', '/nodes/' + encodeURIComponent(id));
            n = v;
          } catch (e) {
            fail(e);
          }
        }),
      });
      return h('div', { class: 'card' }, h('div', { class: 'card-body' },
        h('p', { class: 'muted' }, 'frps 原生 TOML 配置。保存后 agent 先校验，再重启 frps；新配置启动失败（例如端口被占用）会自动恢复旧配置。不支持 webServer（dashboard），状态在面板上看。'),
        h('div', { class: 'gap' }), ta, h('div', { class: 'gap' }), result,
        h('div', { class: 'row-end' }, button('还原', { onclick: () => { ta.value = n.frpsConfig || ''; result.replaceChildren(); } }), save)));
    }

    function tabProxies() {
      if (n.status !== 'online') return offlineBox();
      const box = h('div', { class: 'card' }, loadingBox());
      (async () => {
        try {
          const res = await api('GET', '/nodes/' + encodeURIComponent(id) + '/proxies');
          const list = (res.proxies || []).sort((a, b) => (b.online - a.online) || a.name.localeCompare(b.name));
          if (!list.length) {
            box.replaceChildren(stateBox('inbox', '还没有代理', '客户端连上这个节点并开启代理后显示在这里。'));
            return;
          }
          box.replaceChildren(table([{ label: '名称' }, { label: '类型' }, { label: '用户' }, { label: '状态' }, { label: '连接' }, { label: '今日流量' }, { label: '上线时间' }],
            list.map((x) => h('tr', null, h('td', null, h('code', null, x.name)), h('td', null, x.type), h('td', null, x.user || h('span', { class: 'faint' }, '—')),
              h('td', null, x.online ? badge('在线', 'ok') : badge('离线')), h('td', null, String(x.conns)),
              h('td', { class: 'nowrap' }, '↓ ' + fmtBytes(x.trafficIn) + ' · ↑ ' + fmtBytes(x.trafficOut)),
              h('td', { class: 'nowrap cell-sub' }, x.lastStart || '—')))),
          h('div', { class: 'card-foot' }, '在线 ' + list.filter((x) => x.online).length + ' / 共 ' + list.length + ' 个（包括最近下线的）'));
        } catch (e) {
          box.replaceChildren(errorBox(e));
        }
      })();
      return box;
    }

    function tabLogs() {
      if (n.status !== 'online') return offlineBox();
      const pre = h('pre', { class: 'term term-tall' }, '加载中…');
      const load = async () => {
        try {
          const res = await api('GET', '/nodes/' + encodeURIComponent(id) + '/logs?lines=500');
          pre.textContent = (res.lines || []).join('\n') || '（没有日志）';
          pre.scrollTop = pre.scrollHeight;
        } catch (e) {
          pre.textContent = errText(e);
        }
      };
      load();
      return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, '最近 500 行'), button('刷新', { small: true, icon: 'refresh', onclick: load })),
        h('div', { class: 'card-body' }, pre, h('p', { class: 'cell-sub' }, '完整日志在节点上：journalctl -u moefrp-agent')));
    }

    function tabExec() {
      const hist = h('div');
      const loadHist = async () => {
        try {
          const res = await api('GET', '/nodes/' + encodeURIComponent(id) + '/tasks');
          const list = res.tasks || [];
          hist.replaceChildren(list.length ? table([{ label: '时间' }, { label: '类型' }, { label: '内容' }, { label: '结果' }, { label: '', cls: 'actions' }],
            list.map((t) => h('tr', null, h('td', { class: 'nowrap' }, timeEl(t.createdAt)), h('td', null, t.kind === 'exec' ? '命令' : '升级'),
              h('td', null, h('code', { class: 'clip' }, t.command)),
              h('td', { class: 'nowrap' }, t.status === 'running' ? badge('运行中', 'info') : t.status === 'done' ? badge('成功', 'ok') : badge('失败', 'danger')),
              h('td', { class: 'actions' }, button('查看', { small: true, onclick: async () => {
                try {
                  const full = await api('GET', '/tasks/' + encodeURIComponent(t.id));
                  openDialog({ title: t.kind === 'exec' ? '命令结果' : '升级结果', wide: true, body: [h('pre', { class: 'term' }, '$ ' + full.command), taskResult(full, n)] });
                } catch (e) {
                  fail(e);
                }
              } }))))) : stateBox('inbox', '还没有执行过命令', null));
        } catch (e) {
          hist.replaceChildren(errorBox(e));
        }
      };
      loadHist();
      const disabled = n.info && n.info.execEnabled === false;
      return h('div', null,
        h('div', { class: 'card mb' }, h('div', { class: 'card-body row-between' },
          h('div', null, h('div', { class: 'setting-title' }, '以 root 执行命令'),
            h('div', { class: 'setting-desc' }, disabled ? '这台节点安装时加了 --no-exec，已关闭远程命令。' : '需要重新输入密码确认身份，命令全文写入审计日志。')),
          button('执行命令', { kind: 'primary', icon: 'terminal', disabled: disabled || n.status !== 'online', onclick: () => execDialog([n], n.id) }))),
        h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, '历史'), button('刷新', { small: true, icon: 'refresh', onclick: loadHist })), hist));
    }

    async function resetToken(btn, confirmFirst) {
      if (confirmFirst && !(await confirmDialog({ title: '重置令牌', danger: true, ok: '重置',
        message: '节点当前的令牌会立即失效并断开连接，需要用新的安装命令在节点上重新安装（加 --force）。用于令牌泄露或机器重装。' }))) return;
      await busy(btn, async () => {
        try {
          const res = await api('POST', '/nodes/' + encodeURIComponent(id) + '/reset-token');
          if (confirmFirst && res.installCommand) res.installCommand += ' --force';
          showInstall(res, '新的安装命令');
          load(true);
        } catch (e) {
          fail(e);
        }
      });
    }

    function tabSettings() {
      const name = input({ value: n.name, maxlength: 40 });
      const addr = input({ value: n.publicAddr, placeholder: n.lastIp || 'frp.example.com', maxlength: 253 });
      const save = button('保存', {
        kind: 'primary',
        onclick: () => busy(save, async () => {
          try {
            await api('PATCH', '/nodes/' + encodeURIComponent(id), { name: name.value, publicAddr: addr.value });
            toast('已保存');
            load(true);
          } catch (e) {
            fail(e);
          }
        }),
      });
      const setting = (title, desc, control) => h('div', { class: 'setting' },
        h('div', { class: 'setting-text' }, h('div', { class: 'setting-title' }, title), h('div', { class: 'setting-desc' }, desc)), control);
      const online = n.status === 'online';
      const op = (label, path, msg, opts = {}) => button(label, Object.assign({ small: true, disabled: !online }, opts, {
        onclick: async (e) => {
          const b = e.currentTarget;
          if (opts.confirm && !(await confirmDialog(opts.confirm))) return;
          await busy(b, async () => {
            try {
              await api('POST', '/nodes/' + encodeURIComponent(id) + path);
              toast(msg);
              load(true);
            } catch (ex) {
              fail(ex);
            }
          });
        },
      }));
      const uninstall = button('卸载', {
        small: true, kind: 'danger-ghost',
        onclick: async (e) => {
          const b = e.currentTarget;
          if (!(await confirmDialog({ title: '卸载节点', danger: true, ok: '卸载',
            message: online ? '节点会停止 frps，删除 agent 的程序、配置、systemd 服务和所有数据，然后从面板移除。连在这个节点上的客户端会断开。'
              : '节点不在线，会先标记，下次上线时自动卸载并从面板移除。' }))) return;
          await busy(b, async () => {
            try {
              const res = await api('POST', '/nodes/' + encodeURIComponent(id) + '/uninstall');
              if (res.done) {
                toast('节点正在卸载，已从面板移除');
                location.hash = '#/nodes';
              } else {
                toast('已标记，节点上线后卸载');
                load(true);
              }
            } catch (ex) {
              fail(ex);
            }
          });
        },
      });
      const del = button('删除', {
        small: true, kind: 'danger-ghost',
        onclick: async (e) => {
          const b = e.currentTarget;
          if (!(await confirmDialog({ title: '只从面板删除', danger: true, ok: '删除',
            message: '节点上的 agent 和 frps 不会被卸载，会继续按最后的配置运行，只是面板不再管理它。一般应该用"卸载"。' }))) return;
          await busy(b, async () => {
            try {
              await api('DELETE', '/nodes/' + encodeURIComponent(id));
              toast('已删除');
              location.hash = '#/nodes';
            } catch (ex) {
              fail(ex);
            }
          });
        },
      });
      return h('div', null,
        h('section', { class: 'card mb' }, h('div', { class: 'card-head' }, h('h2', null, '基本信息')),
          h('div', { class: 'card-body' }, h('div', { class: 'form-grid' },
            field('名称', name), field('客户端连接地址', addr, '填域名或 IP；留空时用节点上报的 IP')), h('div', { class: 'row-end' }, save))),
        h('section', { class: 'card mb' }, h('div', { class: 'card-head' }, h('h2', null, '维护')), h('div', { class: 'card-body' },
          setting('重启 frps', '用当前配置重启，客户端会短暂断开', op('重启', '/restart', 'frps 已重启')),
          setting('升级 agent', n.upgradeTo ? '当前 ' + n.info.agentVersion + '，可升级到 ' + n.upgradeTo + '。升级失败会自动回滚。' : '已是面板打包的版本',
            op('升级', '/upgrade', '已开始升级，完成后在"命令"页的历史里查看结果', { disabled: !online || !n.upgradeTo, icon: 'upgrade',
              confirm: { title: '升级 agent', ok: '升级', message: 'agent 会下载新版本并重启，frps 会断开几秒。' } })),
          setting('重置令牌', '令牌泄露或机器重装时使用，需要在节点上重新安装', button('重置', { small: true, onclick: (e) => resetToken(e.currentTarget, true) })))),
        h('section', { class: 'card danger-zone' }, h('div', { class: 'card-head' }, h('h2', null, '危险操作')), h('div', { class: 'card-body' },
          setting('卸载', '停止 frps，删除节点上 agent 的所有文件，并从面板移除', uninstall),
          setting('只从面板删除', '不卸载节点上的程序', del))));
    }

    await load();
    const timer = setInterval(() => (p.alive() ? load(true) : clearInterval(timer)), 10000);
  }

  boot();
})();
