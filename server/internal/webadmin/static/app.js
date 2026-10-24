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
      else if (k.startsWith('on')) listen(el, k.slice(2), v);
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

  // listen 绑定事件。处理函数放在 el._on 上，morph 合并新内容时换成新的函数，不用重建元素。
  function listen(el, type, fn) {
    el._on = el._on || {};
    el._on[type] = fn;
    wire(el, type);
  }

  function wire(el, type) {
    el._wired = el._wired || new Set();
    if (el._wired.has(type)) return;
    el._wired.add(type);
    el.addEventListener(type, (e) => {
      const fn = el._on && el._on[type];
      if (fn) fn(e);
    });
  }

  // morph 把新画出来的 to 合并进页面上的 from：只改变化的属性和文字，元素本身保留。
  // 自动刷新用它，滚动位置、焦点、正在执行的按钮都不受影响，进度条宽度的变化还能有过渡动画。
  function morph(from, to) {
    if (from.nodeType !== to.nodeType || from.nodeName !== to.nodeName) {
      from.replaceWith(to);
      return;
    }
    if (from.nodeType === Node.TEXT_NODE) {
      if (from.data !== to.data) from.data = to.data;
      return;
    }
    if (from.nodeType !== Node.ELEMENT_NODE) {
      from.replaceWith(to);
      return;
    }
    // 正在执行的按钮（busy）保持原样，结束后它自己会恢复
    if (from.classList.contains('loading')) return;
    for (const a of Array.from(from.attributes)) {
      if (!to.hasAttribute(a.name)) from.removeAttribute(a.name);
    }
    for (const a of Array.from(to.attributes)) {
      if (from.getAttribute(a.name) !== a.value) from.setAttribute(a.name, a.value);
    }
    from._on = to._on;
    for (const t of Object.keys(to._on || {})) wire(from, t);
    morphChildren(from, Array.from(to.childNodes));
  }

  // morphChildren 用 next 更新 parent 的子节点，按位置一一合并
  function morphChildren(parent, next) {
    const cur = Array.from(parent.childNodes);
    next.forEach((n, i) => {
      if (i < cur.length) morph(cur[i], n);
      else parent.append(n);
    });
    for (let i = next.length; i < cur.length; i++) cur[i].remove();
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
    down: 'M12 5v14M19 12l-7 7-7-7',
    sort: 'M7 20V4M3 8l4-4 4 4M17 4v16M13 16l4 4 4-4',
    grip: 'M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01',
    trash: 'M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6',
    play: 'M6 4l14 8-14 8z',
    eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
    lock: 'M5 11h14v10H5zM8 11V7a4 4 0 0 1 8 0v4',
    chevron: 'M9 6l6 6-6 6',
    sun: 'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
    moon: 'M20.5 14.1A8.5 8.5 0 1 1 9.9 3.5a6.8 6.8 0 0 0 10.6 10.6z',
    power: 'M12 3v8M6.4 6.4a8 8 0 1 0 11.2 0',
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
    // 带图标的按钮在窄屏的页头里只显示图标，所以把文字放进 title
    return h('button', {
      class: cls.join(' '), type: opts.type || 'button', onclick: opts.onclick,
      disabled: opts.disabled, title: opts.title || (opts.icon ? label : null),
    }, opts.icon ? icon(opts.icon, 16) : null, h('span', { class: 'btn-label' }, label));
  }

  function iconButton(name, label, onclick, kind) {
    return h('button', { class: 'icon-btn' + (kind ? ' ' + kind : ''), type: 'button', title: label, 'aria-label': label, onclick }, icon(name, 16));
  }

  // opButton 是带一行说明的操作按钮，放在 .ops 网格里
  function opButton(label, sub, opts = {}) {
    return h('button', {
      class: 'btn op' + (opts.danger ? ' danger' : ''), type: 'button', onclick: opts.onclick, disabled: opts.disabled,
    }, h('span', { class: 'op-title' }, opts.icon ? icon(opts.icon, 15) : null, label), h('small', null, sub));
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
    return h('div', { class: 'scroll' }, h('table', null,
      h('thead', null, h('tr', null, columns.map((c) => h('th', { class: c.cls }, c.label)))),
      h('tbody', null, rows)));
  }

  // 宽屏显示表格，窄屏显示成一条两三行的列表（不会横向滚动）。宽度跨过断点时页面通过 onLayout 重画。
  const wideMq = matchMedia('(min-width: 1100px)');
  wideMq.addEventListener('change', () => { if (state.relayout) state.relayout(); });

  // dataList：cells(x) 返回表格一行的 td；row(x) 返回窄屏的
  // { lead, title, meta: [...], extra, trail, href, onclick }，title 里的字符串会单行省略。
  function dataList(columns, items, cells, row) {
    if (wideMq.matches) {
      return table(columns, items.map((x) => {
        const r = row(x);
        const tr = h('tr', { class: r.onclick ? 'clickable' : null }, cells(x));
        if (r.onclick) listen(tr, 'click', (e) => { if (!e.target.closest('button, a, label, input')) r.onclick(e); });
        return tr;
      }));
    }
    return h('div', { class: 'scroll rows' }, items.map((x) => {
      const r = row(x);
      const meta = (r.meta || []).filter((m) => m != null && m !== false && m !== '');
      const title = [r.title].flat(Infinity).filter((t) => t != null && t !== false).map((t) => (typeof t === 'string' ? h('span', { class: 't' }, t) : t));
      const el = h(r.href ? 'a' : 'div', { class: 'row' + (r.href || r.onclick ? ' tappable' : ''), href: r.href },
        r.lead ? h('div', { class: 'row-lead' }, r.lead) : null,
        h('div', { class: 'row-main' },
          h('div', { class: 'row-title' }, title),
          // 项之间留空格作为换行点
          meta.length ? h('div', { class: 'row-meta' }, meta.map((m, i) => [i ? ' ' : null, h('span', null, m)])) : null,
          r.extra ? h('div', { class: 'row-extra' }, r.extra) : null),
        r.trail ? h('div', { class: 'row-trail' }, r.trail) : null);
      if (r.onclick) {
        el.tabIndex = 0;
        listen(el, 'click', (e) => { if (!e.target.closest('button, label, input')) r.onclick(e); });
        listen(el, 'keydown', (e) => { if (e.key === 'Enter' && e.target === el) r.onclick(e); });
      }
      return el;
    }));
  }

  // listCard 是列表页的卡片：内容区滚动，页脚固定
  function listCard() {
    return h('div', { class: 'card list-card grow' });
  }

  // fillList 更新列表卡片的内容：合并进已有元素（见 morph），自动刷新时不跳回顶部、不闪
  function fillList(box, ...children) {
    morphChildren(box, children.flat(Infinity).filter((c) => c != null && c !== false));
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
    // 速率是字节差除以秒数，会带小数；先取整到字节，不足 1 KB 时按整数显示
    n = Math.round(Number(n) || 0);
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
  function openDialog({ title, titleIcon, body, actions, wide, xwide, onSubmit, onClose }) {
    const dlg = h('dialog', { class: 'dialog' + (wide ? ' wide' : '') + (xwide ? ' xwide' : '') });
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

  // ---------- 主题 ----------
  // 浅色 / 深色 / 跟随系统。选择存在 localStorage，theme.js 在样式生效前先套用，避免闪一下。
  // 切换时从按钮位置圆形展开新主题（View Transitions）；不支持时整体渐变；系统要求减少动画时直接切换。

  const THEME_KEY = 'moefrp-admin-theme';
  const darkMq = matchMedia('(prefers-color-scheme: dark)');
  const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)');

  function themePref() {
    try {
      const t = localStorage.getItem(THEME_KEY);
      return t === 'light' || t === 'dark' ? t : 'system';
    } catch (e) {
      return 'system';
    }
  }

  const isDark = (pref) => pref === 'dark' || (pref === 'system' && darkMq.matches);

  function applyTheme(pref) {
    const root = document.documentElement;
    if (pref === 'system') delete root.dataset.theme;
    else root.dataset.theme = pref;
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.content = getComputedStyle(root).getPropertyValue('--bg').trim();
    document.querySelectorAll('.theme-btn').forEach(drawThemeButton);
  }

  // fadeTheme 让颜色在 0.35 秒内渐变过去（不支持 View Transitions 时，和跟随系统切换时用）
  function fadeTheme(fn) {
    const root = document.documentElement;
    root.classList.add('theme-fade');
    fn();
    clearTimeout(fadeTheme.timer);
    fadeTheme.timer = setTimeout(() => root.classList.remove('theme-fade'), 400);
  }

  function setTheme(pref, origin) {
    try {
      if (pref === 'system') localStorage.removeItem(THEME_KEY);
      else localStorage.setItem(THEME_KEY, pref);
    } catch (e) { /* 隐私模式下存不了，只对这次有效 */ }
    if (reduceMotion.matches) return applyTheme(pref);
    if (!document.startViewTransition || !origin) return fadeTheme(() => applyTheme(pref));
    const b = origin.getBoundingClientRect();
    const x = b.left + b.width / 2;
    const y = b.top + b.height / 2;
    const r = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));
    const t = document.startViewTransition(() => applyTheme(pref));
    t.ready.then(() => document.documentElement.animate(
      { clipPath: ['circle(0px at ' + x + 'px ' + y + 'px)', 'circle(' + r + 'px at ' + x + 'px ' + y + 'px)'] },
      { duration: 520, easing: 'cubic-bezier(0.4, 0, 0.2, 1)', pseudoElement: '::view-transition-new(root)' })).catch(() => {});
  }

  // 点一下总是切到相反的颜色；如果相反的颜色正好是系统当前的颜色，就回到"跟随系统"
  function toggleTheme(e) {
    const next = isDark(themePref()) ? 'light' : 'dark';
    setTheme(next === (darkMq.matches ? 'dark' : 'light') ? 'system' : next, e.currentTarget);
  }

  function drawThemeButton(btn) {
    const pref = themePref();
    const dark = isDark(pref);
    const label = (dark ? '深色' : '浅色') + (pref === 'system' ? '（跟随系统）' : '') + '，点击切换到' + (dark ? '浅色' : '深色');
    btn.title = label;
    btn.setAttribute('aria-label', label);
    btn.replaceChildren(icon(dark ? 'moon' : 'sun', 17));
  }

  function themeButton() {
    const btn = h('button', { class: 'icon-btn theme-btn', type: 'button', onclick: toggleTheme });
    drawThemeButton(btn);
    return btn;
  }

  darkMq.addEventListener('change', () => {
    if (themePref() === 'system') fadeTheme(() => applyTheme('system'));
  });
  applyTheme(themePref());

  // ---------- 状态与路由 ----------

  const state = { me: null, page: null, gen: 0, relayout: null };

  const PAGES = [
    { id: 'overview', label: '概览', icon: 'grid', desc: '用户、邀请码和客户端发布情况', render: pageOverview },
    { id: 'nodes', label: '节点', icon: 'server', desc: '在面板上管理 frps 节点：配置、监控、升级、执行命令、卸载', render: pageNodes },
    { id: 'invites', label: '邀请码', icon: 'ticket', desc: '注册需要邀请码，每个码只能用一次', render: pageInvites },
    { id: 'users', label: '用户', icon: 'users', desc: '调整角色、停用账号、踢下线、生成密码重置码', render: pageUsers },
    { id: 'clients', label: '客户端', icon: 'activity', desc: '在线设备、推送通知、远程控制隧道、客户端版本和公告', render: pageClients },
    { id: 'audit', label: '审计日志', short: '审计', icon: 'list', desc: '登录、注册、分享和管理操作的记录', render: pageAudit },
    { id: 'settings', label: '设置', icon: 'layers', desc: '客户端版本、公告和图源，登录策略，节点安装地址；改了立即生效', render: pageSettings },
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
    $app.replaceChildren(h('main', { class: 'auth' }, h('div', { class: 'auth-theme' }, themeButton()), h('div', { class: 'auth-card' }, brand(), h('h1', null, title), h('p', null, sub), note, form)));
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
      field('管理员昵称', nick, '3–20 位字母、数字或下划线，之后也用它登录桌面客户端'),
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

  let $nav, $back, $title, $desc, $actions, $body;

  // 框架：宽屏左侧侧栏，中屏图标侧栏，窄屏底部标签栏（见 app.css）。页头固定，页面内容在 .page-body 里。
  function showShell() {
    const me = state.me;
    $nav = h('nav', { class: 'nav', 'aria-label': '主导航' }, PAGES.map((p) => h('a', { href: '#/' + p.id, title: p.label },
      icon(p.icon, 19), h('span', { class: 'nav-label' }, p.label), h('span', { class: 'nav-short' }, p.short || p.label))));
    const out = iconButton('logout', '退出登录', () => logout(out));
    const sidebar = h('aside', { class: 'sidebar' },
      brand(), $nav,
      h('div', { class: 'sidebar-foot' },
        h('div', { class: 'avatar', title: me.nickname, 'aria-hidden': 'true' }, Array.from(me.nickname)[0] || '?'),
        h('div', { class: 'who' }, h('div', { class: 'who-name', title: me.nickname }, me.nickname), h('div', { class: 'who-sub' }, '管理员 · ' + (me.version || 'dev'))),
        themeButton(), out));
    // 窄屏没有侧栏，主题和退出按钮放在页头
    const mOut = iconButton('logout', '退出登录（' + me.nickname + '）', () => logout(mOut));
    mOut.classList.add('m-only');
    const mTheme = themeButton();
    mTheme.classList.add('m-only');
    $back = h('a', { class: 'icon-btn head-back', 'aria-label': '返回', title: '返回', hidden: true }, icon('back', 18));
    $title = h('h1');
    $desc = h('p');
    $actions = h('div', { class: 'page-actions' });
    $body = h('div', { class: 'page-body' });
    const main = h('main', { class: 'main' },
      h('header', { class: 'page-head' }, $back, h('div', { class: 'head-text' }, $title, $desc), $actions, mTheme, mOut), $body);
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
    $title.removeAttribute('title');
    $desc.textContent = page.desc;
    $back.hidden = true;
    $actions.replaceChildren();
    $body.replaceChildren();
    $body.scrollTop = 0;
    state.relayout = null;
    const alive = () => gen === state.gen && state.me !== null;
    page.render({
      actions: $actions, body: $body, params, alive,
      // setHead 换标题（可以是元素）和副标题；back 是返回按钮的链接
      setHead: (title, desc, back) => {
        morphChildren($title, [typeof title === 'string' ? document.createTextNode(title) : title]);
        $title.title = typeof title === 'string' ? title : title.textContent;
        $desc.textContent = desc || '';
        if (back) {
          $back.href = back;
          $back.hidden = false;
        }
        document.title = (typeof title === 'string' ? title : page.label) + ' · MoeFrp 管理后台';
      },
      // onLayout 登记宽窄屏切换时的重画函数（表格和列表互换）
      onLayout: (fn) => { state.relayout = () => alive() && fn(); },
    });
  }

  // autoRefresh 每 3 秒（和 agent 上报同频）调一次 fn，结果用 morph 合并进页面。
  // 页面在后台时暂停，回到前台马上刷新一次；上一次还没返回时跳过；离开页面后停止。
  const REFRESH_MS = 3000;
  function autoRefresh(p, fn) {
    let running = false;
    const stop = () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
    const tick = async () => {
      if (!p.alive()) return stop();
      if (document.hidden || running) return;
      running = true;
      try {
        await fn();
      } finally {
        running = false;
      }
    };
    const onVisible = () => { if (!document.hidden) tick(); };
    const timer = setInterval(tick, REFRESH_MS);
    document.addEventListener('visibilitychange', onVisible);
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

    // 用户构成：一条分段比例条 + 图例（三类互不重叠：停用的算在"已停用"里）
    const disabled = s.disabledUsers || 0;
    const admins = s.admins || 0;
    const parts = [['普通用户', Math.max(0, s.users - admins - disabled), ''], ['管理员', admins, 'accent'], ['已停用', disabled, 'danger']];
    const segbar = h('div', { class: 'segbar', role: 'img', 'aria-label': parts.map(([l, n]) => l + ' ' + n).join('，') },
      parts.map(([, n, kind]) => {
        const el = h('i', { class: kind || null });
        // 宽度用 style 属性设置，CSP 允许（style-src 只限制样式表来源和 <style>，CSSOM 不受影响）
        el.style.flexGrow = String(n);
        el.hidden = n === 0;
        return el;
      }));
    const legend = h('div', { class: 'legend' }, parts.map(([label, n, kind]) =>
      h('span', null, h('i', { class: 'swatch ' + (kind || '') }), label, h('b', null, String(n)))));

    const c = d.client || {};
    const link = c.downloadUrl && /^https?:\/\//i.test(c.downloadUrl)
      ? h('a', { href: c.downloadUrl, target: '_blank', rel: 'noopener noreferrer', title: c.downloadUrl }, c.downloadUrl)
      : null;
    const none = (text) => h('span', { class: 'faint' }, text);

    p.body.replaceChildren(h('div', { class: 'stack' },
      h('div', { class: 'stats' },
        stat('用户', 'users', s.users, '7 天内新注册 ' + s.registeredWeek),
        stat('已登录用户', 'activity', s.onlineUsers, '24 小时内活跃 ' + s.activeToday),
        stat('可用邀请码', 'ticket', s.invitesUnused, s.invitesUnused === 0 ? '用完了，生成一些吧' : '已使用 ' + s.invitesUsed, s.invitesUnused === 0),
        stat('云端配置', 'layers', s.profiles, '分享 ' + s.activeShares + ' · 订阅 ' + s.subscriptions)),
      h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, '用户构成'), h('span', null, '共 ' + s.users + ' 人')),
        h('div', { class: 'card-body' }, segbar, legend)),
      h('section', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, '客户端发布'), h('span', null, '在 config.toml 的 [client] 里修改')),
        h('div', { class: 'card-body' }, h('div', { class: 'tiles' },
          infoTile('服务端版本', h('code', null, d.serverVersion || 'dev')),
          infoTile('最新客户端', c.latestVersion ? h('code', null, c.latestVersion) : none('未设置')),
          infoTile('最低客户端', c.minVersion ? h('code', null, c.minVersion) : none('不限制')),
          infoTile('单设备登录', d.singleSession ? '开启' : '关闭'),
          infoTile('图源', (c.imageSources || []).length + ' 个'),
          infoTile('下载地址', link || none('未设置')),
          infoTile('公告', c.announcement || none('无'), null, 'span-all'))))));
  }

  // infoTile 是指标格里的一格：小标题 + 值（单行省略），可选一行小字。
  function infoTile(label, value, sub, cls) {
    return h('div', { class: 'tile' + (cls ? ' ' + cls : '') },
      h('div', { class: 'tile-label' }, label),
      h('div', { class: 'tile-value', title: typeof value === 'string' ? value : null }, value == null || value === '' ? h('span', { class: 'faint' }, '—') : value),
      sub ? h('div', { class: 'tile-sub', title: sub }, sub) : null);
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
    const box = listCard();

    p.actions.append(
      button('刷新', { icon: 'refresh', onclick: () => load() }),
      button('生成邀请码', { kind: 'primary', icon: 'plus', onclick: () => createInvitesDialog(() => load()) }));
    p.body.append(h('div', { class: 'fill' },
      h('div', { class: 'toolbar' },
        segmented([['unused', '未使用'], ['used', '已使用'], ['revoked', '已撤销'], ['all', '全部']], status, (v, set) => {
          status = v;
          set(v);
          load();
        }, { label: '按状态筛选' }),
        searchBox('搜索邀请码、备注、使用者', (v) => { q = v; draw(); })),
      box));
    p.onLayout(() => { if (!box.querySelector('.state')) draw(); });

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
      fillList(box,
        dataList([{ label: '邀请码' }, { label: '备注' }, { label: '状态' }, { label: '创建于' }, { label: '使用情况' }, { label: '', cls: 'actions' }], rows,
          (i) => {
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
            return [
              h('td', null, h('span', { class: 'code-cell' }, h('code', null, i.code), iconButton('copy', '复制', () => copyText(i.code)))),
              h('td', null, i.note || h('span', { class: 'faint' }, '—')),
              h('td', null, badge(label, kind)),
              h('td', null, timeEl(i.createdAt)),
              h('td', null, usage),
              h('td', { class: 'actions' }, revoke)];
          },
          (i) => {
            const [label, kind] = INVITE_STATUS[i.status] || [i.status];
            return {
              title: [h('code', { class: 't' }, i.code), badge(label, kind)],
              meta: [
                i.note,
                i.status === 'used' ? [(i.usedBy || '（用户已删除）') + ' 使用于 ', timeEl(i.usedAt)] : null,
                i.status === 'revoked' ? ['撤销于 ', timeEl(i.revokedAt)] : null,
                ['创建于 ', timeEl(i.createdAt)],
              ],
              trail: [iconButton('copy', '复制', () => copyText(i.code)),
                i.status === 'unused' ? iconButton('x', '撤销', (e) => revokeInvite(i, e.currentTarget), 'danger') : null],
            };
          }),
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

  const ROLES = { admin: ['管理员', 'accent'], user: ['普通用户', null] };

  async function pageUsers(p) {
    let filter = 'all';
    let q = '';
    let list = [];
    let me = '';
    const box = listCard();

    p.actions.append(button('刷新', { icon: 'refresh', onclick: () => load() }));
    p.body.append(h('div', { class: 'fill' },
      h('div', { class: 'toolbar' },
        segmented([['all', '全部'], ['user', '普通用户'], ['admin', '管理员'], ['disabled', '已停用']], filter, (v, set) => {
          filter = v;
          set(v);
          draw();
        }, { label: '按角色筛选' }),
        searchBox('搜索昵称', (v) => { q = v; draw(); })),
      box));
    p.onLayout(() => { if (list.length) draw(); });

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
      const avatar = (u) => h('div', { class: 'avatar', 'aria-hidden': 'true' }, Array.from(u.nickname)[0] || '?');
      const roleBadges = (u) => {
        const [roleLabel, roleKind] = ROLES[u.role] || [u.role];
        return [badge(roleLabel, roleKind), u.status === 'disabled' ? badge('已停用', 'danger') : null];
      };
      fillList(box,
        dataList([{ label: '用户' }, { label: '角色' }, { label: '客户端' }, { label: '最近活跃' }, { label: '云端数据' }, { label: '注册于' }, { label: '', cls: 'actions' }], rows,
          (u) => [
            h('td', null, h('div', { class: 'user-cell' }, avatar(u), u.nickname, u.nickname === me ? badge('你', 'outline') : null)),
            h('td', { class: 'nowrap' }, roleBadges(u)),
            h('td', { class: 'nowrap' }, u.activeSessions > 0
              ? [h('span', { class: 'dot ok' }), u.activeSessions + ' 个会话']
              : h('span', { class: 'faint' }, '未登录')),
            h('td', null, timeEl(u.lastSeen)),
            h('td', { class: 'nowrap muted' }, '配置 ' + u.profiles + ' · 分享 ' + u.shares + ' · 订阅 ' + u.subscriptions),
            h('td', { class: 'nowrap' }, h('time', { datetime: u.createdAt, title: fmtTime(u.createdAt) }, fmtDate(u.createdAt))),
            h('td', { class: 'actions' }, button('管理', { small: true, onclick: () => manageUser(u, u.nickname === me, load) }))],
          (u) => ({
            lead: avatar(u),
            title: [u.nickname, u.nickname === me ? badge('你', 'outline') : null, roleBadges(u)],
            meta: [
              u.activeSessions > 0 ? [h('span', { class: 'dot ok' }), u.activeSessions + ' 个会话'] : '未登录',
              u.lastSeen ? ['活跃 ', timeEl(u.lastSeen)] : null,
              '配置 ' + u.profiles + ' · 分享 ' + u.shares + ' · 订阅 ' + u.subscriptions,
            ],
            trail: icon('chevron', 16),
            onclick: () => manageUser(u, u.nickname === me, load),
          })),
        h('div', { class: 'card-foot' }, rows.length === list.length ? '共 ' + list.length + ' 人' : '显示 ' + rows.length + ' / ' + list.length + ' 人'));
    }

    load();
  }

  function manageUser(u, isMe, reload) {
    const nick = encodeURIComponent(u.nickname);
    let changed = false;

    // 角色
    const roleSeg = segmented([['user', '普通用户'], ['admin', '管理员']], u.role, async (v, set) => {
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
    const statusBtn = h('div', { class: 'contents' });
    const drawStatus = () => {
      const disabled = u.status === 'disabled';
      statusBtn.replaceChildren(opButton(disabled ? '启用账号' : '停用账号', disabled ? '恢复登录' : '所有登录立即失效，数据保留', {
        icon: 'power', danger: !disabled, disabled: isMe,
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

    const kickBtn = opButton('踢下线', '注销所有客户端登录', {
      icon: 'logout',
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
    const resetBtn = opButton('密码重置码', '用户忘记密码时生成', {
      icon: 'key',
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
        h('div', { class: 'role-row' }, h('span', { class: 'field-label' }, '角色'), roleSeg),
        h('div', { class: 'ops' },
          statusBtn, kickBtn, resetBtn,
          opButton('节点权限', '官方节点、限额、节点凭证', { icon: 'server', onclick: () => userNodesDialog(u) }),
          opButton('登录设备', '设备指纹、系统和硬件、IP', { icon: 'activity', onclick: () => userDevicesDialog(u) })),
        secretBox,
      ],
      onClose: () => { if (changed) reload(); },
    });
  }

  // ---------- 客户端 ----------
  // 在线设备（和面板保持实时通道的客户端）、推送通知、远程停止 / 重启隧道、让设备下线，
  // 以及客户端版本、最低版本（强制更新）和公告，改了马上推给所有在线客户端（docs/client-live.md）。

  const TUNNEL_STATES = {
    idle: ['未运行', ''], connecting: ['连接中', 'info'], running: ['运行中', 'ok'],
    reconnecting: ['重连中', 'warn'], stopping: ['停止中', ''], error: ['出错', 'danger'],
  };
  const PROFILE_KINDS = { local: '本地配置', cloud: '云端配置', subscription: '订阅' };
  const NOTICE_LEVELS = [['info', '信息'], ['success', '成功'], ['warning', '警告'], ['error', '错误']];

  // wide 让表单项在两列的 .form-grid 里占满一行
  function wide(el) {
    el.classList.add('span-all');
    return el;
  }

  // cmpVer 比较 a.b.c 形式的版本号（预发布后缀忽略）
  function cmpVer(a, b) {
    const pa = String(a || '').split(/[-+]/)[0].split('.').map(Number);
    const pb = String(b || '').split(/[-+]/)[0].split('.').map(Number);
    for (let i = 0; i < 3; i++) {
      const d = (pa[i] || 0) - (pb[i] || 0);
      if (d) return d;
    }
    return 0;
  }

  async function pageClients(p) {
    let list = [];
    let latest = '';
    let q = '';
    let filter = 'all';
    let loaded = false;
    const selected = new Set();
    const box = listCard();
    const bulk = h('div', { class: 'bulk', hidden: true });

    p.actions.append(
      button('版本与公告', { icon: 'upgrade', onclick: () => { location.hash = '#/settings/client'; } }),
      button('推送通知', { icon: 'inbox', kind: 'primary', onclick: () => noticeDialog(selected.size ? { sessions: [...selected] } : null, list) }));
    p.body.append(h('div', { class: 'fill' },
      h('div', { class: 'toolbar' },
        segmented([['all', '全部'], ['running', '隧道运行中'], ['outdated', '需要更新']], filter, (v, set) => {
          filter = v;
          set(v);
          draw();
        }, { label: '筛选' }),
        searchBox('搜索昵称、设备、IP', (v) => { q = v; draw(); })),
      bulk, box));
    box.append(loadingBox());
    p.onLayout(() => { if (loaded) draw(); });

    async function load() {
      try {
        const res = await api('GET', '/clients');
        if (!p.alive()) return;
        list = res.clients || [];
        latest = res.latestVersion || '';
        loaded = true;
        for (const id of [...selected]) if (!list.some((c) => c.sessionId === id)) selected.delete(id);
        draw();
      } catch (e) {
        if (p.alive() && !loaded) box.replaceChildren(errorBox(e, load));
      }
    }

    const outdated = (c) => latest && c.version && cmpVer(c.version, latest) < 0;
    const tunnelOf = (c) => c.tunnel || {};
    const whoOf = (c) => c.nickname + ' 的 ' + (c.hostname || '设备');

    function draw() {
      const rows = list.filter((c) => {
        if (filter === 'running' && tunnelOf(c).state !== 'running') return false;
        if (filter === 'outdated' && !outdated(c)) return false;
        if (!q) return true;
        return [c.nickname, c.hostname, c.ip, c.os].some((v) => String(v || '').toLowerCase().includes(q));
      });
      drawBulk();
      if (!rows.length) {
        fillList(box, stateBox(list.length ? 'search' : 'activity', list.length ? '没有匹配的设备' : '没有在线的客户端',
          list.length ? null : '用户登录客户端后会出现在这里'));
        return;
      }
      const check = (c) => {
        const cb = h('input', { type: 'checkbox', 'aria-label': '选择 ' + whoOf(c) });
        cb.checked = selected.has(c.sessionId);
        cb.addEventListener('change', () => {
          if (cb.checked) selected.add(c.sessionId);
          else selected.delete(c.sessionId);
          drawBulk();
        });
        return h('label', { class: 'check' }, cb);
      };
      const device = (c) => h('span', { class: 'nowrap' }, c.hostname || '未知设备', c.os ? h('span', { class: 'faint' }, ' · ' + c.os) : null);
      const version = (c) => h('span', { class: 'nowrap' }, c.version || '—', outdated(c) ? [' ', badge('可更新', 'warn')] : null);
      const tunnel = (c) => {
        const t = tunnelOf(c);
        const [label, kind] = TUNNEL_STATES[t.state] || ['未知', ''];
        const where = [PROFILE_KINDS[t.profileKind], t.nodeName].filter(Boolean).join(' · ');
        return h('span', { class: 'tunnel-cell', title: t.error || null },
          badge(label, kind),
          t.state && t.state !== 'idle' ? h('span', { class: 'muted' }, (where ? where + ' · ' : '') + (t.running || 0) + '/' + (t.proxies || 0) + ' 代理') : null);
      };
      const ops = (c) => h('div', { class: 'row-ops' },
        iconButton('inbox', '推送通知', () => noticeDialog({ sessions: [c.sessionId] }, list)),
        iconButton('refresh', '重启隧道', () => tunnelAction({ sessions: [c.sessionId] }, 'restart', whoOf(c))),
        iconButton('power', '停止隧道', () => tunnelAction({ sessions: [c.sessionId] }, 'stop', whoOf(c))),
        iconButton('logout', '让这台设备下线', () => kick([c.sessionId], whoOf(c)), 'danger'));
      fillList(box,
        dataList([{ label: '', cls: 'sel' }, { label: '用户' }, { label: '设备' }, { label: '版本' }, { label: '隧道' }, { label: 'IP' }, { label: '上线' }, { label: '', cls: 'actions' }], rows,
          (c) => [
            h('td', { class: 'sel' }, check(c)),
            h('td', { class: 'nowrap' }, h('div', { class: 'user-cell' }, h('div', { class: 'avatar', 'aria-hidden': 'true' }, Array.from(c.nickname)[0] || '?'), c.nickname)),
            h('td', null, device(c)),
            h('td', null, version(c)),
            h('td', null, tunnel(c)),
            h('td', { class: 'nowrap mono' }, c.ip || '—'),
            h('td', null, timeEl(c.connectedAt)),
            h('td', { class: 'actions' }, ops(c))],
          (c) => ({
            lead: check(c),
            title: [c.nickname, device(c)],
            meta: [tunnel(c), version(c), c.ip],
            extra: ops(c),
            // 点一行看这台设备的详细信息（和"用户 → 登录设备"同一个窗口，这台排在最前面并高亮）
            onclick: () => userDevicesDialog({ nickname: c.nickname }, c.sessionId),
          })),
        h('div', { class: 'card-foot' }, rows.length === list.length ? '在线 ' + list.length + ' 台' : '显示 ' + rows.length + ' / ' + list.length + ' 台'));
    }

    function drawBulk() {
      const n = selected.size;
      bulk.hidden = !n;
      if (!n) {
        bulk.replaceChildren();
        return;
      }
      const target = () => ({ sessions: [...selected] });
      bulk.replaceChildren(
        h('span', { class: 'bulk-n' }, '已选 ' + n + ' 台'),
        button('推送通知', { small: true, icon: 'inbox', onclick: () => noticeDialog(target(), list) }),
        button('重启隧道', { small: true, icon: 'refresh', onclick: () => tunnelAction(target(), 'restart', n + ' 台设备') }),
        button('停止隧道', { small: true, icon: 'power', onclick: () => tunnelAction(target(), 'stop', n + ' 台设备') }),
        button('下线', { small: true, icon: 'logout', kind: 'danger', onclick: () => kick([...selected], n + ' 台设备') }),
        button('取消选择', { small: true, onclick: () => { selected.clear(); draw(); } }));
    }

    function tunnelAction(target, action, who) {
      const stop = action === 'stop';
      const reason = input({ placeholder: '可以不填，会显示给用户', maxlength: 200 });
      const ok = button(stop ? '停止隧道' : '重启隧道', { kind: stop ? 'danger' : 'primary', type: 'submit' });
      const d = openDialog({
        title: (stop ? '停止 ' : '重启 ') + who + ' 的隧道',
        titleIcon: stop ? 'power' : 'refresh',
        body: [h('p', { class: 'dialog-text' }, stop
          ? '客户端上正在运行的隧道马上停止，用户可以自己再点启动。'
          : '客户端上正在运行的隧道马上断开重连（例如改了节点配置后让它立即生效）。没在运行的不受影响。'),
        h('div', { class: 'gap' }), field('原因', reason)],
        actions: [button('取消', { onclick: () => d.close() }), ok],
        onSubmit: () => busy(ok, async () => {
          try {
            const res = await api('POST', '/clients/tunnel', { target, action, reason: reason.value });
            toast('已发送给 ' + res.sent + ' 台设备');
            d.close();
          } catch (e) {
            fail(e);
          }
        }),
      });
    }

    async function kick(sessions, who) {
      if (!(await confirmDialog({ title: '让 ' + who + ' 下线', danger: true, ok: '下线',
        message: '这些设备上的登录马上失效，客户端回到未登录状态（本地配置不受影响），用户可以重新登录。要禁止登录请到“用户”页停用账号。' }))) return;
      try {
        const res = await api('POST', '/clients/kick', { sessions });
        for (const id of sessions) selected.delete(id);
        toast('已让 ' + res.revoked + ' 台设备下线');
        load();
      } catch (e) {
        fail(e);
      }
    }

    load();
    autoRefresh(p, load);
  }

  // noticeDialog 推送通知。target 为空时在弹窗里选（全部在线 / 按角色）；有值时发给这些会话。
  function noticeDialog(target, clients) {
    const fixed = !!target;
    let mode = 'all';
    let level = 'info';
    let display = 'toast';
    const title = input({ placeholder: '例如：今晚 23:00 维护', maxlength: 80, required: true });
    const content = h('textarea', { class: 'input textarea', rows: 4, maxlength: 2000, placeholder: '可以不填' });
    const linkText = input({ placeholder: '例如：查看详情', maxlength: 20 });
    const linkUrl = input({ placeholder: 'https://…', type: 'url' });
    const stickyCb = h('input', { type: 'checkbox' });
    const sticky = h('label', { class: 'check' }, stickyCb, '不自动消失（要用户手动关）');
    const preview = h('div', { class: 'notice-preview' });
    const err = h('div', { class: 'form-error', role: 'alert' });
    const sendBtn = button('发送', { kind: 'primary', type: 'submit' });

    const who = () => {
      if (fixed) {
        const n = target.sessions.length;
        if (n === 1) {
          const c = clients.find((x) => x.sessionId === target.sessions[0]);
          return c ? c.nickname + ' 的 ' + (c.hostname || '设备') : '1 台设备';
        }
        return '选中的 ' + n + ' 台设备';
      }
      return { all: '所有在线客户端', user: '在线的普通用户', admin: '在线的管理员' }[mode];
    };
    const drawPreview = () => {
      const lv = NOTICE_LEVELS.find((x) => x[0] === level)[1];
      preview.className = 'notice-preview ' + level;
      preview.replaceChildren(...[
        h('div', { class: 'np-head' }, h('b', null, title.value.trim() || '标题'),
          h('span', { class: 'np-tag' }, lv + (display === 'dialog' ? ' · 弹窗' : stickyCb.checked ? ' · 常驻' : ''))),
        content.value.trim() ? h('div', { class: 'np-body' }, content.value.trim()) : null,
        linkUrl.value.trim() ? h('div', { class: 'np-link' }, linkText.value.trim() || '打开链接') : null,
      ].filter(Boolean));
      sticky.hidden = display === 'dialog';
      sendBtn.querySelector('.btn-label').textContent = '发送给' + who();
    };
    for (const el of [title, content, linkText, linkUrl]) el.addEventListener('input', drawPreview);
    stickyCb.addEventListener('change', drawPreview);

    const d = openDialog({
      title: '推送通知',
      titleIcon: 'inbox',
      wide: true,
      body: [
        h('div', { class: 'form-grid' },
          wide(fixed ? field('发给', h('div', { class: 'static' }, who()))
            : field('发给', segmented([['all', '全部在线'], ['user', '普通用户'], ['admin', '管理员']], mode, (v, set) => { mode = v; set(v); drawPreview(); }, { label: '发给' }))),
          field('级别', segmented(NOTICE_LEVELS, level, (v, set) => { level = v; set(v); drawPreview(); }, { label: '级别' })),
          field('显示方式', segmented([['toast', '通知'], ['dialog', '弹窗']], display, (v, set) => { display = v; set(v); drawPreview(); }, { label: '显示方式' })),
          wide(field('标题', title)),
          wide(field('内容', content)),
          field('按钮文字', linkText),
          field('按钮链接', linkUrl, '在浏览器里打开，只能是 http(s)'),
          wide(sticky),
          wide(field('预览', preview))),
        err],
      actions: [button('取消', { onclick: () => d.close() }), sendBtn],
      onSubmit: () => busy(sendBtn, async () => {
        err.classList.remove('show');
        const t = fixed ? target : mode === 'all' ? { all: true } : { roles: [mode] };
        try {
          const res = await api('POST', '/clients/notice', {
            target: t,
            notice: { title: title.value, content: content.value, level, display, linkText: linkText.value, linkUrl: linkUrl.value, sticky: stickyCb.checked },
          });
          toast(res.sent ? '已发送给 ' + res.sent + ' 台设备' : '没有在线的客户端收到', res.sent ? undefined : 'error');
          d.close();
        } catch (e) {
          err.textContent = errText(e);
          err.classList.add('show');
        }
      }),
    });
    drawPreview();
    title.focus();
  }

  // ---------- 设置 ----------
  // 面板运行中就能改的配置：存在数据库里，改了立即生效，优先于配置文件；每一组都能恢复为配置文件。
  // 部署相关、改了要重启的只读显示（docs/server-ops.md"后台设置"一节）。
  // 按分组切换，一次显示一组：整页一屏，放不下时在卡片里滚动（手机上也一样）。分组记在地址里 #/settings/<分组>。
  const SETTING_TABS = [['client', '客户端'], ['images', '图源'], ['auth', '登录'], ['nodes', '节点'], ['deploy', '部署']];

  async function pageSettings(p) {
    let tab = SETTING_TABS.some((t) => t[0] === p.params[0]) ? p.params[0] : 'client';
    const host = h('div', { class: 'set-host' }, loadingBox());
    p.body.append(h('div', { class: 'fill' },
      h('div', { class: 'toolbar' }, segmented(SETTING_TABS, tab, (v, set) => {
        tab = v;
        set(v);
        history.replaceState(null, '', '#/settings/' + v);
        draw();
      }, { label: '设置分组' })),
      host));
    let data;
    try {
      data = await api('GET', '/panel-settings');
    } catch (e) {
      if (p.alive()) host.replaceChildren(errorBox(e, () => renderPage()));
      return;
    }
    if (!p.alive()) return;
    const reload = (res) => { data = res; draw(); };
    draw();

    function draw() {
      const make = { client: clientCard, images: imagesCard, auth: authCard, nodes: nodesCard, deploy: deployCard }[tab];
      host.replaceChildren(make());
    }

    // 卡片：标题、来源标签、按钮在顶部固定；内容放不下时在卡片里滚动
    function card(title, fromPanel, body, actions) {
      return h('section', { class: 'card list-card set-card' },
        h('div', { class: 'card-head' },
          h('h2', null, title),
          fromPanel === undefined ? null : badge(fromPanel ? '后台设置' : '来自配置文件', fromPanel ? 'accent' : 'outline'),
          actions ? h('div', { class: 'head-btns' }, actions) : null),
        h('div', { class: 'card-body card-scroll' }, body));
    }

    // wideM：手机上的两列表单里占满一行
    function wideM(el) {
      el.classList.add('m-span');
      return el;
    }

    // 客户端配置是一份：版本那一组和图源那一组各自保存时，带上另一组现在的值
    async function saveClient(patch) {
      const c = data.client.config;
      await api('PUT', '/client-config', Object.assign({
        latestVersion: c.latestVersion, minVersion: c.minVersion, downloadUrl: c.downloadUrl, sha256: c.sha256,
        releaseNotes: c.releaseNotes, announcement: c.announcement, imageSources: c.imageSources || [],
      }, patch));
      reload(await api('GET', '/panel-settings'));
    }
    function resetClientButton() {
      const reset = button('恢复为配置文件', {
        small: true, disabled: !data.client.fromPanel,
        onclick: () => busy(reset, async () => {
          try {
            await api('DELETE', '/client-config');
            toast('已恢复为配置文件里的设置，并推送给在线客户端');
            reload(await api('GET', '/panel-settings'));
          } catch (e) {
            fail(e);
          }
        }),
      });
      return reset;
    }
    function formError() {
      return h('div', { class: 'form-error', role: 'alert' });
    }
    function showErr(el, e) {
      el.textContent = errText(e);
      el.classList.add('show');
    }

    // ---- 客户端：版本、公告、图源 ----
    function clientCard() {
      const c = data.client.config;
      const latest = input({ value: c.latestVersion, placeholder: '例如 3.1.0' });
      const min = input({ value: c.minVersion, placeholder: '不限制' });
      const url = input({ value: c.downloadUrl, placeholder: 'https://…', type: 'url' });
      const sha = input({ value: c.sha256, placeholder: '可以不填', class: 'input mono' });
      const notes = h('textarea', { class: 'input textarea', rows: 2, placeholder: '有新版本时显示给用户' });
      notes.value = c.releaseNotes || '';
      const ann = h('textarea', { class: 'input textarea', rows: 2, placeholder: '每台客户端显示一次，内容变了再显示' });
      ann.value = c.announcement || '';
      const err = formError();
      const save = button('保存并推送', {
        kind: 'primary', small: true,
        onclick: () => busy(save, async () => {
          err.classList.remove('show');
          try {
            await saveClient({
              latestVersion: latest.value, minVersion: min.value, downloadUrl: url.value, sha256: sha.value,
              releaseNotes: notes.value, announcement: ann.value,
            });
            toast('已保存，并推送给 ' + data.online + ' 台在线客户端');
          } catch (e) {
            showErr(err, e);
          }
        }),
      });
      return card('版本与公告', data.client.fromPanel, [
        h('p', { class: 'muted card-note' }, '保存后马上推给在线的客户端（现在 ' + data.online + ' 台），其余的下次启动时拿到。'),
        h('div', { class: 'form-grid settings-grid' },
          field('最新版本', latest, '比它旧的客户端提示更新'),
          field('最低版本（强制更新）', min, '比它旧的客户端立即停用账号功能，直到更新'),
          wideM(field('下载地址', url)),
          wideM(field('SHA-256', sha, '客户端下载新版本后核对')),
          wideM(field('更新说明', notes)),
          wideM(field('公告', ann))),
        err], [resetClientButton(), save]);
    }

    // 图源：图片面板的图源，只给登录的用户；保存后马上推给在线客户端
    function imagesCard() {
      const images = imageEditor(data.client.config.imageSources || []);
      const err = formError();
      const save = button('保存并推送', {
        kind: 'primary', small: true,
        onclick: () => busy(save, async () => {
          err.classList.remove('show');
          try {
            await saveClient({ imageSources: images.value() });
            toast('已保存，客户端的图片面板马上换用新图源');
          } catch (e) {
            showErr(err, e);
          }
        }),
      });
      return card('图源', data.client.fromPanel, [images.el, err], [resetClientButton(), save]);
    }

    // 图源编辑：每行地址、类型（直接返回图片 / JSON 接口）、JSON 路径、权重
    function imageEditor(initial) {
      const rows = h('div', { class: 'img-rows' });
      const count = h('span', { class: 'muted' });
      const empty = h('div', { class: 'faint img-empty' }, '还没有图源：客户端的图片面板会显示“暂时没有可用的图源”。');
      const recount = () => {
        const n = rows.children.length;
        count.textContent = n + ' 个';
        empty.hidden = n > 0;
      };
      function addRow(src) {
        let isApi = !!src.isApi;
        const u = input({ value: src.url || '', placeholder: 'https://…', class: 'input mono', 'aria-label': '图源地址' });
        const path = input({ value: src.jsonPath || '', placeholder: '图片地址在 JSON 里的路径，例如 data.0.url', class: 'input mono', 'aria-label': 'JSON 路径' });
        const w = input({ value: String(src.weight || 1), type: 'number', min: 1, max: 100, class: 'input', 'aria-label': '权重', title: '权重：越大越常出现' });
        const kind = segmented([['img', '图片'], ['api', 'JSON']], isApi ? 'api' : 'img', (v, set) => {
          isApi = v === 'api';
          set(v);
          path.disabled = !isApi;
        }, { label: '类型' });
        path.disabled = !isApi;
        const row = h('div', { class: 'img-row' }, u, kind, path, w, iconButton('trash', '删除这个图源', () => { row.remove(); recount(); }, 'danger'));
        row.value = () => ({ url: u.value.trim(), isApi, jsonPath: isApi ? path.value.trim() : '', weight: Number(w.value) || 0 });
        rows.append(row);
        recount();
      }
      initial.forEach(addRow);
      recount();
      return {
        el: h('div', { class: 'img-editor' },
          h('div', { class: 'img-head' },
            count,
            h('span', { class: 'img-hint muted' }, '“图片”：地址直接返回图片；“JSON”：返回 JSON，按路径取图片地址。权重越大越常出现'),
            button('添加图源', { small: true, icon: 'plus', onclick: () => addRow({ weight: 1 }) })),
          rows, empty),
        value: () => [...rows.children].map((r) => r.value()),
      };
    }

    // ---- 账号与登录 ----
    function authCard() {
      const a = data.auth.config;
      const single = h('input', { type: 'checkbox' });
      single.checked = a.singleSession;
      const days = input({ type: 'number', min: 1, max: 365, value: String(Math.max(1, Math.round(a.sessionTtlHours / 24))), class: 'input' });
      const err = formError();
      const save = button('保存', {
        kind: 'primary', small: true,
        onclick: () => busy(save, async () => {
          err.classList.remove('show');
          try {
            reload(await api('PUT', '/panel-settings/auth', { singleSession: single.checked, sessionTtlHours: Math.round(Number(days.value) * 24) }));
            toast('已保存，之后的登录和续期生效');
          } catch (e) {
            showErr(err, e);
          }
        }),
      });
      const reset = button('恢复为配置文件', {
        small: true, disabled: !data.auth.fromPanel,
        onclick: () => busy(reset, async () => {
          try {
            reload(await api('DELETE', '/panel-settings/auth'));
            toast('已恢复为配置文件里的设置');
          } catch (e) {
            fail(e);
          }
        }),
      });
      return card('账号与登录', data.auth.fromPanel, [
        h('div', { class: 'form-grid settings-grid' },
          h('div', { class: 'field' }, h('span', { class: 'field-label' }, '单设备模式'),
            h('label', { class: 'check' }, single, '每个账号同时只能在一台设备上登录'),
            h('span', { class: 'field-hint' }, '新登录会把这个账号的其他客户端挤下线（管理后台的登录不受影响）')),
          field('客户端登录有效期（天）', days, '这么久没用才需要重新登录，用着就一直顺延')),
        err], [reset, save]);
    }

    // ---- 节点 ----
    function nodesCard() {
      const n = data.nodes.config;
      const pub = input({ value: n.publicUrl, placeholder: '留空按你打开后台的地址生成，例如 https://frp.example.com' });
      const mirror = input({ value: n.agentMirror, placeholder: '留空直连 GitHub，例如 https://ghproxy.example.com/' });
      const err = formError();
      const save = button('保存', {
        kind: 'primary', small: true,
        onclick: () => busy(save, async () => {
          err.classList.remove('show');
          try {
            reload(await api('PUT', '/panel-settings/nodes', { publicUrl: pub.value, agentMirror: mirror.value }));
            toast('已保存');
          } catch (e) {
            showErr(err, e);
          }
        }),
      });
      const reset = button('恢复为配置文件', {
        small: true, disabled: !data.nodes.fromPanel,
        onclick: () => busy(reset, async () => {
          try {
            reload(await api('DELETE', '/panel-settings/nodes'));
            toast('已恢复为配置文件里的设置');
          } catch (e) {
            fail(e);
          }
        }),
      });
      return card('节点', data.nodes.fromPanel, [
        h('div', { class: 'form-grid settings-grid' },
          field('面板地址（写进安装命令）', pub, '节点访问面板用的地址；后台走内网、节点走公网时要填'),
          field('默认下载代理', mirror, '添加节点时自动填上；每个节点也可以在自己的设置里改')),
        err], [reset, save]);
    }

    // ---- 部署（只读） ----
    function deployCard() {
      const d = data.deploy;
      const row = (k, v) => [h('dt', null, k), h('dd', null, v === '' || v == null || (Array.isArray(v) && !v.length) ? h('span', { class: 'faint' }, '—') : h('code', null, Array.isArray(v) ? v.join(', ') : String(v)))];
      return card('部署', undefined, [
        h('p', { class: 'muted card-note' }, '这些只能在 data/config.toml 或 MOEFRP_* 环境变量里改，改完重启面板生效。'),
        h('dl', { class: 'kv deploy-kv' },
          row('面板版本', d.version), row('监听地址', d.listen), row('数据库', d.dbPath), row('受信任的反向代理', d.trustedProxies),
          row('日志级别', d.logLevel), row('agent 来源', d.agentSource), row('面板主密钥', d.secretKey))]);
    }
  }


  // userDevicesDialog 用户还有效的登录：每台设备的会话 ID、设备指纹（HWID）、系统和硬件、IP、在线和隧道状态，可以单独让它下线。
  // 设备信息是客户端连实时通道时上报的，离线后显示最近一次的。
  // focus 是要突出显示的会话（从"客户端"页点进来的那台）：排在最前面并高亮。
  async function userDevicesDialog(u, focus) {
    const box = h('div', { class: 'dev-list' }, loadingBox());
    const d = openDialog({ title: u.nickname + ' 的登录设备', titleIcon: 'activity', wide: true, body: box,
      actions: [button('关闭', { onclick: () => d.close() })] });
    const load = async () => {
      let res;
      try {
        res = await api('GET', '/users/' + encodeURIComponent(u.nickname) + '/devices');
      } catch (e) {
        box.replaceChildren(errorBox(e, load));
        return;
      }
      const list = res.devices || [];
      if (!list.length) {
        box.replaceChildren(stateBox('inbox', '没有有效的登录', '用户登录客户端或管理后台后会出现在这里'));
        return;
      }
      if (focus) list.sort((a, b) => (b.sessionId === focus) - (a.sessionId === focus));
      box.replaceChildren(...list.map(card));
    };
    const copyable = (v) => (v ? h('span', { class: 'dev-copy' }, h('code', null, v), iconButton('copy', '复制', () => copyText(v))) : h('span', { class: 'faint' }, '—'));
    const card = (x) => {
      const dv = x.device || {};
      const t = x.tunnel || {};
      const [tl, tk] = TUNNEL_STATES[t.state] || ['', ''];
      const hw = [dv.cpu, dv.cores ? dv.cores + ' 线程' : '', dv.memoryMb ? (dv.memoryMb / 1024).toFixed(1) + ' GB 内存' : ''].filter(Boolean).join(' · ');
      const kick = button('下线', {
        small: true, kind: 'danger',
        onclick: async () => {
          if (!(await confirmDialog({ title: '让这台设备下线', danger: true, ok: '下线',
            message: '这台设备上的登录马上失效，客户端回到未登录状态（本地配置不受影响），用户可以重新登录。' }))) return;
          await busy(kick, async () => {
            try {
              await api('POST', '/clients/kick', { sessions: [x.sessionId] });
              toast('已让这台设备下线');
              load();
            } catch (e) {
              fail(e);
            }
          });
        },
      });
      return h('section', { class: 'dev-card' + (focus && x.sessionId === focus ? ' focus' : '') },
        h('div', { class: 'dev-head' },
          h('b', null, dv.hostname || x.deviceName || (x.kind === 'admin_web' ? '管理后台' : '未知设备')),
          badge(x.kind === 'admin_web' ? '管理后台' : '客户端', 'outline'),
          x.online ? badge('在线', 'ok') : badge('离线'),
          x.online && t.state ? badge('隧道' + tl, tk) : null,
          h('span', { class: 'grow' }), kick),
        h('dl', { class: 'kv dev-kv' },
          h('dt', null, '设备指纹'), h('dd', null, copyable(x.deviceId)),
          h('dt', null, '会话 ID'), h('dd', null, copyable(x.sessionId)),
          h('dt', null, '安装 ID'), h('dd', null, copyable(x.installId)),
          h('dt', null, '系统'), h('dd', null, [dv.os, dv.osBuild, dv.arch].filter(Boolean).join(' · ') || h('span', { class: 'faint' }, '没上报（客户端版本较旧或还没连过实时通道）')),
          h('dt', null, '硬件'), h('dd', null, [dv.model, hw].filter(Boolean).join(' · ') || h('span', { class: 'faint' }, '—')),
          h('dt', null, '客户端'), h('dd', null, dv.version || h('span', { class: 'faint' }, '—')),
          h('dt', null, 'IP'), h('dd', null, [x.ip ? '现在 ' + x.ip : (x.lastIp ? '最近 ' + x.lastIp : ''), x.loginIp ? '登录时 ' + x.loginIp : ''].filter(Boolean).join(' · ') || '—'),
          h('dt', null, '时间'), h('dd', null, '登录于 ', timeEl(x.createdAt), ' · 最近活跃 ', timeEl(x.lastSeenAt)),
          x.online && t.state && t.state !== 'idle' ? [h('dt', null, '隧道'), h('dd', null,
            [PROFILE_KINDS[t.profileKind], t.profileName, t.nodeName, (t.running || 0) + '/' + (t.proxies || 0) + ' 代理'].filter(Boolean).join(' · '))] : null));
    };
    load();
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
    'admin.node_secret': ['轮换节点密钥', 'danger'],
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
    let entries = null;
    const box = listCard();
    p.actions.append(button('刷新', { icon: 'refresh', onclick: () => load() }));
    p.body.append(h('div', { class: 'fill' },
      h('div', { class: 'toolbar' },
        segmented([['', '全部'], ['admin.', '管理操作'], ['auth.', '账号'], ['share.', '分享']], prefix, (v, set) => {
          prefix = v;
          set(v);
          load();
        }, { label: '按类型筛选' })),
      box));
    p.onLayout(() => { if (entries) draw(entries); });

    async function load() {
      box.replaceChildren(loadingBox());
      entries = null;
      try {
        const res = await api('GET', '/audit?limit=200&action=' + encodeURIComponent(prefix));
        if (!p.alive()) return;
        entries = res.entries || [];
        draw(entries);
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
      const event = (e) => {
        if (e.action === 'auth.login' && e.detail && e.detail.kind === 'admin_web') return badge('登录后台', 'accent');
        const [label, kind] = ACTIONS[e.action] || [e.action, null];
        return badge(label, kind);
      };
      fillList(box,
        dataList([{ label: '时间' }, { label: '事件' }, { label: '操作者' }, { label: '对象' }, { label: 'IP' }, { label: '详情' }], list,
          (e) => [
            h('td', { class: 'nowrap' }, h('time', { datetime: e.at, title: relTime(e.at) }, fmtTime(e.at))),
            h('td', { class: 'nowrap' }, event(e)),
            h('td', null, e.actor || h('span', { class: 'faint' }, '—')),
            h('td', { class: 'nowrap' }, e.target || h('span', { class: 'faint' }, '—')),
            h('td', { class: 'nowrap' }, e.ip ? h('code', null, e.ip) : h('span', { class: 'faint' }, '—')),
            h('td', null, detail(e.detail))],
          (e) => ({
            title: [event(e), [e.actor, e.target && e.target !== e.actor ? e.target : null].filter(Boolean).join(' → ') || null],
            meta: [h('time', { datetime: e.at, title: fmtTime(e.at) }, fmtTime(e.at)), e.ip],
            extra: Object.keys(e.detail || {}).length ? detail(e.detail) : null,
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
      h('div', { class: 'grid-2' },
        h('section', { class: 'card' },
          h('div', { class: 'card-head' }, h('h2', null, '怎么恢复')),
          h('div', { class: 'card-body' }, h('ol', { class: 'steps' },
            h('li', null, '停止服务：', h('code', null, 'docker compose stop moefrp')),
            h('li', null, '用备份文件替换 compose 同目录下的 ', h('code', null, 'data/moefrp.db')),
            h('li', null, '启动服务：', h('code', null, 'docker compose start moefrp'))))),
        h('section', { class: 'card' },
          h('div', { class: 'card-head' }, h('h2', null, '自动备份')),
          h('div', { class: 'card-body muted' },
            '升级数据库结构前，服务端会在数据库旁边自动保存一份 ', h('code', null, '*.pre-migrate-时间.db'),
            '。日常备份请定期在这里下载，或者备份整个 ', h('code', null, 'data'), ' 目录。')))));
  }

  // ---------- 官方节点：限额和用户权限 ----------

  const ROLE_OPTIONS = [['user', '普通用户'], ['admin', '管理员']];
  const PROXY_TYPES = ['tcp', 'udp', 'http', 'https', 'tcpmux', 'stcp', 'xtcp', 'sudp'];

  function quotaText(q) {
    if (!q) return '—';
    return [q.maxProxies ? '最多 ' + q.maxProxies + ' 个代理' : '代理数不限', q.ports ? '端口 ' + q.ports : '端口不限',
      q.types && q.types.length ? q.types.join('/') : '类型不限'].join(' · ');
  }

  // quotaInputs 是一份限额的输入框和读取函数
  function quotaInputs(q) {
    const max = input({ type: 'number', min: 0, max: 10000, value: String(q.maxProxies || 0), inputmode: 'numeric', 'aria-label': '最多同时在线的代理' });
    const ports = input({ value: q.ports || '', placeholder: '不限', 'aria-label': '远程端口范围' });
    const boxes = PROXY_TYPES.map((t) => {
      const cb = h('input', { type: 'checkbox', value: t, 'aria-label': t });
      cb.checked = !q.types || q.types.length === 0 || q.types.includes(t);
      return cb;
    });
    const read = () => {
      const types = boxes.filter((c) => c.checked).map((c) => c.value);
      return { maxProxies: Number(max.value) || 0, ports: ports.value.trim(), types: types.length === PROXY_TYPES.length ? [] : types };
    };
    return { max, ports, boxes, read };
  }

  // quotaFields 是单个用户的限额表单
  function quotaFields(q) {
    const f = quotaInputs(q);
    f.ports.placeholder = '不限，例如 10000-20000,30000';
    const el = h('div', { class: 'quota-fields' },
      h('div', { class: 'form-grid' }, field('最多同时在线的代理', f.max, '0 表示不限'), field('远程端口范围', f.ports, '留空不限')),
      field('允许的代理类型', h('div', { class: 'checks' }, f.boxes.map((cb) => h('label', { class: 'check' }, cb, cb.value)))));
    return { el, read: f.read };
  }

  // upgradeNodesDialog 批量升级 agent：默认勾上所有在线、有新版本的节点，依次发起升级（每个节点自己下载、重启，失败自动回滚）
  function upgradeNodesDialog(data, done) {
    const latest = (data.agent && data.agent.version) || '';
    const nodes = data.nodes || [];
    const can = (n) => n.status === 'online' && !!n.upgradeTo;
    const picks = new Map();
    const save = button('升级', { kind: 'primary', type: 'submit' });
    const count = () => {
      const k = [...picks.values()].filter((cb) => cb.checked).length;
      save.textContent = k ? '升级 ' + k + ' 个节点' : '升级';
      save.disabled = !k;
    };
    const rows = nodes.map((n) => {
      const cb = h('input', { type: 'checkbox', disabled: !can(n) });
      cb.checked = can(n);
      cb.addEventListener('change', count);
      picks.set(n.id, cb);
      const ver = n.info && n.info.agentVersion;
      const state = n.status !== 'online' ? h('span', { class: 'faint' }, '离线')
        : n.upgradeTo ? h('span', { class: 'nowrap' }, h('code', null, ver || '?'), ' → ', h('code', null, n.upgradeTo))
          : h('span', { class: 'faint nowrap' }, '已是最新' + (ver ? ' ' + ver : ''));
      return h('label', { class: 'upg-item' + (can(n) ? '' : ' off') }, cb, h('b', null, n.name), regionBadge(n.region), h('span', { class: 'grow' }), state);
    });
    const all = button('全选 / 全不选', {
      small: true,
      onclick: () => {
        const on = [...picks.values()].some((cb) => !cb.disabled && !cb.checked);
        for (const cb of picks.values()) if (!cb.disabled) cb.checked = on;
        count();
      },
    });
    count();
    const d = openDialog({
      title: '升级 agent', titleIcon: 'upgrade', wide: true,
      body: [h('p', { class: 'dialog-text' }, latest
        ? '最新版本 ' + latest + '。每个节点自己下载新版本并重启，frps 会断开几秒；升级失败会自动回滚。结果在各节点"命令"页的历史里。'
        : '还没查到 agent 最新版本，稍后再试。'),
      h('div', { class: 'upg-head' }, all), h('div', { class: 'upg-list' }, rows)],
      actions: [button('取消', { onclick: () => d.close() }), save],
      onSubmit: () => busy(save, async () => {
        const ids = [...picks.entries()].filter(([, cb]) => cb.checked).map(([id]) => id);
        const failed = [];
        for (const id of ids) {
          try {
            await api('POST', '/nodes/' + encodeURIComponent(id) + '/upgrade');
          } catch (e) {
            if (e.handled) return;
            failed.push((nodes.find((n) => n.id === id) || {}).name + '：' + errText(e));
          }
        }
        d.close();
        if (failed.length) fail(new Error('有 ' + failed.length + ' 个节点没能开始升级：' + failed.join('；')));
        else toast('已开始升级 ' + ids.length + ' 个节点');
        done();
      }),
    });
  }

  // nodeOrderDialog 调整节点顺序：拖动或点上移 / 下移。管理后台的列表和客户端的官方节点列表都按这个顺序。
  function nodeOrderDialog(nodes, done) {
    const order = nodes.slice();
    const list = h('ol', { class: 'order-list' });
    let from = -1;
    const move = (a, b) => {
      if (b < 0 || b >= order.length || a === b) return;
      const [x] = order.splice(a, 1);
      order.splice(b, 0, x);
      draw();
      list.children[b]?.querySelector(b > a ? '[data-dir=down]' : '[data-dir=up]')?.focus();
    };
    const draw = () => list.replaceChildren(...order.map((n, i) => {
      const up = iconButton('upgrade', '上移', () => move(i, i - 1));
      const down = iconButton('down', '下移', () => move(i, i + 1));
      up.dataset.dir = 'up';
      down.dataset.dir = 'down';
      up.disabled = i === 0;
      down.disabled = i === order.length - 1;
      const li = h('li', { class: 'order-item', draggable: 'true' },
        h('span', { class: 'order-grip', 'aria-hidden': 'true' }, icon('grip', 16)),
        h('span', { class: 'order-n' }, String(i + 1)),
        h('span', { class: 'order-name' }, h('b', null, n.name), regionBadge(n.region), n.official ? badge('开放', 'accent') : null),
        up, down);
      li.addEventListener('dragstart', (e) => { from = i; li.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; });
      li.addEventListener('dragend', () => { from = -1; li.classList.remove('dragging'); });
      li.addEventListener('dragover', (e) => { if (from >= 0) e.preventDefault(); });
      li.addEventListener('drop', (e) => { e.preventDefault(); if (from >= 0) move(from, i); });
      return li;
    }));
    draw();
    const save = button('保存顺序', { kind: 'primary', type: 'submit' });
    const d = openDialog({
      title: '节点顺序', titleIcon: 'sort',
      body: [h('p', { class: 'dialog-text' }, '拖动或点箭头调整顺序。这里的列表和客户端里的官方节点都按这个顺序排（客户端先按地区分组，组内按这个顺序），保存后马上推给在线客户端。'), list],
      actions: [button('取消', { onclick: () => d.close() }), save],
      onSubmit: () => busy(save, async () => {
        try {
          await api('PUT', '/nodes-order', { ids: order.map((n) => n.id) });
          toast('已保存节点顺序');
          d.close();
          done();
        } catch (e) {
          fail(e);
        }
      }),
    });
  }

  async function quotasDialog() {
    let roles;
    try {
      roles = await api('GET', '/quotas');
    } catch (e) {
      fail(e);
      return;
    }
    // 矩阵：列是角色，行是限额项，手机上也能一屏放下
    const editors = ROLE_OPTIONS.map(([v, label]) => [v, label, quotaInputs(roles[v] || {})]);
    const matrix = h('table', { class: 'qm' },
      h('thead', null, h('tr', null, h('th'), editors.map(([, label]) => h('th', { scope: 'col' }, label)))),
      h('tbody', null,
        h('tr', null, h('th', { scope: 'row' }, '最多代理'), editors.map(([, , f]) => h('td', null, f.max))),
        h('tr', null, h('th', { scope: 'row' }, '远程端口'), editors.map(([, , f]) => h('td', null, f.ports))),
        PROXY_TYPES.map((t, i) => h('tr', { class: i === 0 ? 'sep' : null }, h('th', { scope: 'row' }, h('code', null, t)),
          editors.map(([, , f]) => h('td', null, h('label', { class: 'check' }, f.boxes[i])))))));
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button('保存', { kind: 'primary', type: 'submit' });
    const d = openDialog({
      title: '官方节点限额',
      titleIcon: 'layers',
      wide: true,
      body: [h('p', { class: 'dialog-text' }, '每个角色在每个官方节点上能用的资源，由节点上的 agent 执行。个别用户在"用户 → 节点权限"里单独调整。'),
        matrix,
        h('ul', { class: 'hints' },
          h('li', null, '最多代理：同时在线的代理数，0 不限。'),
          h('li', null, '远程端口：tcp / udp 的远程端口范围，如 10000-20000,30000；留空不限。'),
          h('li', null, '不勾选的代理类型会被节点拒绝。')),
        err],
      actions: [button('取消', { onclick: () => d.close() }), submit],
      onSubmit: () => busy(submit, async () => {
        err.classList.remove('show');
        const body = {};
        for (const [v, , f] of editors) body[v] = f.read();
        try {
          await api('PUT', '/quotas', body);
          toast('限额已保存，正在推送到节点');
          d.close();
        } catch (e) {
          if (e.handled) return;
          err.textContent = errText(e);
          err.classList.add('show');
        }
      }),
    });
  }

  async function userNodesDialog(u) {
    const nick = encodeURIComponent(u.nickname);
    let data;
    try {
      data = await api('GET', '/users/' + nick + '/nodes');
    } catch (e) {
      fail(e);
      return;
    }
    const ov = data.override || {};
    const custom = h('input', { type: 'checkbox' });
    custom.checked = !!(data.override && (ov.maxProxies != null || ov.ports != null || ov.types != null));
    const base = Object.assign({}, data.roleQuota, data.effective);
    const qf = quotaFields(base);
    qf.el.hidden = !custom.checked;
    custom.addEventListener('change', () => { qf.el.hidden = !custom.checked; });

    const official = data.nodes.filter((x) => x.official);
    const selects = official.map((x) => {
      const roleOK = (x.roles || []).includes(data.role);
      const sel = h('select', { class: 'input select' },
        h('option', { value: '' }, '按角色：' + (roleOK ? '可用' : '不可用')),
        h('option', { value: 'allow' }, '允许'), h('option', { value: 'deny' }, '禁止'));
      sel.value = x.grant === true ? 'allow' : x.grant === false ? 'deny' : '';
      return [x, sel];
    });
    const resetBtn = button('重置节点凭证', {
      small: true,
      onclick: async (e) => {
        const b = e.currentTarget;
        if (!(await confirmDialog({ title: '重置节点凭证', ok: '重置', danger: true,
          message: u.nickname + ' 已领取的登录票据在所有官方节点上立即失效，正在运行的隧道不再接受新连接；客户端会自动领新票据重连。' }))) return;
        await busy(b, async () => {
          try {
            await api('POST', '/users/' + nick + '/reset-node-credential');
            toast('已重置');
          } catch (ex) {
            fail(ex);
          }
        });
      },
    });
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button('保存', { kind: 'primary', type: 'submit' });
    const d = openDialog({
      title: u.nickname + ' 的节点权限',
      titleIcon: 'server',
      wide: true,
      body: [
        h('h3', { class: 'sub-head' }, '官方节点'),
        official.length ? h('div', { class: 'grant-list' }, selects.map(([x, sel]) => h('div', { class: 'grant-row' },
          h('div', null, h('div', { class: 'setting-title' }, x.name), h('div', { class: 'setting-desc' },
            ((x.roles || []).includes('user') ? '开放给普通用户' : '只给管理员') +
            ' · 现在' + (x.allowed ? '可用' : '不可用'))), sel)))
          : h('p', { class: 'muted' }, '还没有官方节点。在节点的"设置"里开启。'),
        h('div', { class: 'gap' }),
        h('h3', { class: 'sub-head' }, '限额'),
        h('p', { class: 'cell-sub' }, '角色（' + ((ROLE_OPTIONS.find(([v]) => v === data.role) || [0, data.role])[1]) + '）的限额：' + quotaText(data.roleQuota)),
        h('label', { class: 'check' }, custom, '给这个用户单独设置'),
        qf.el,
        h('div', { class: 'gap' }),
        h('div', { class: 'setting' }, h('div', { class: 'setting-text' }, h('div', { class: 'setting-title' }, '节点凭证'),
          h('div', { class: 'setting-desc' }, '客户端每次连接官方节点都会领一张一次性登录票据；重置后已领的全部作废')), resetBtn),
        err,
      ],
      actions: [button('取消', { onclick: () => d.close() }), submit],
      onSubmit: () => busy(submit, async () => {
        err.classList.remove('show');
        const grants = {};
        for (const [x, sel] of selects) grants[x.nodeId] = sel.value === 'allow' ? true : sel.value === 'deny' ? false : null;
        let override = null;
        if (custom.checked) {
          const q = qf.read();
          override = { maxProxies: q.maxProxies, ports: q.ports, types: q.types };
        }
        try {
          await api('PUT', '/users/' + nick + '/nodes', { override, grants });
          toast('已保存，正在推送到节点');
          d.close();
        } catch (e) {
          if (e.handled) return;
          err.textContent = errText(e);
          err.classList.add('show');
        }
      }),
    });
  }

  // frpsSchema 返回 frps 配置表单的描述（分组、字段、默认值），只取一次。
  let schemaCache = null;
  async function frpsSchema() {
    if (!schemaCache) schemaCache = await api('GET', '/frps-schema');
    return schemaCache;
  }

  // secretDialog 显示面板主密钥的指纹、版本、保存方式，可以轮换。
  // 轮换在同一个弹窗里分步完成（说明 → 需要时输入密码 → 结果），不叠弹窗；结果页并排显示旧指纹和新指纹。
  async function secretDialog() {
    let info;
    try {
      info = await api('GET', '/node-secret');
    } catch (e) {
      fail(e);
      return;
    }
    const wrap = h('div', { class: 'keybox' });
    const foot = h('div', { class: 'foot-slot' });
    const d = openDialog({ title: '节点密钥', titleIcon: 'key', body: wrap, actions: [foot] });

    const fill = (el, ...kids) => el.replaceChildren(...kids.filter(Boolean));
    const fp = (v, cls) => h('code', { class: 'fp' + (cls ? ' ' + cls : '') }, v || '—');
    const kv = () => h('dl', { class: 'kv' },
      h('dt', null, '指纹'), h('dd', null, info.fingerprint ? fp(info.fingerprint) : h('span', { class: 'faint' }, '解不开')),
      h('dt', null, '版本'), h('dd', null, '第 ' + info.version + ' 版'),
      h('dt', null, '上次轮换'), h('dd', null, info.rotatedAt ? fmtTime(info.rotatedAt) : h('span', { class: 'faint' }, '从未轮换（第一次使用时自动生成）')),
      h('dt', null, '保存方式'), h('dd', null, info.protection));

    function overview() {
      fill(wrap, 
        h('p', { class: 'dialog-text' }, '面板主密钥用来签发节点登录票据、派生各节点的存储密钥，是节点托管的根。不会下发原值，数据库里也只存密文。'),
        info.error ? h('div', { class: 'callout warn' }, icon('alert', 16), h('span', null, info.error)) : null,
        kv(),
        h('ul', { class: 'hints' },
          h('li', null, '怀疑泄露（例如服务器被入侵、secret.key 外泄）时轮换。'),
          h('li', null, '轮换后在线节点和在线客户端马上换用新密钥，用户会断开一两秒后自动重连；离线节点上线时自动换。')));
      foot.replaceChildren(button('关闭', { onclick: () => d.close() }), button('轮换密钥…', { kind: 'danger', onclick: confirmStep }));
    }

    async function confirmStep() {
      let elevated = false;
      try {
        elevated = (await api('GET', '/elevate')).elevated;
      } catch (e) { /* 按没验证处理 */ }
      const pw = input({ type: 'password', autocomplete: 'current-password', placeholder: '管理员密码' });
      const err = h('div', { class: 'form-error', role: 'alert' });
      fill(wrap, 
        h('div', { class: 'callout warn' }, icon('alert', 16), h('span', null,
          '轮换后：所有已签发的登录票据立即作废，在线用户断开一两秒后自动重连；所有节点换用新的存储密钥重新加密本地配置。这一步不能撤销。')),
        h('dl', { class: 'kv' }, h('dt', null, '当前指纹'), h('dd', null, fp(info.fingerprint)), h('dt', null, '轮换后'), h('dd', null, '第 ' + (info.version + 1) + ' 版')),
        elevated ? null : field('输入密码确认', pw),
        err);
      const ok = button('确认轮换', {
        kind: 'danger',
        onclick: () => busy(ok, async () => {
          err.classList.remove('show');
          try {
            if (!elevated) {
              await api('POST', '/elevate', { password: pw.value }, { noRedirect: true, noReauth: true });
              elevated = true;
            }
            info = await api('POST', '/node-secret/rotate', undefined, { noReauth: true });
            done();
          } catch (e) {
            err.textContent = errText(e);
            err.classList.add('show');
            if (!elevated) pw.select();
          }
        }),
      });
      foot.replaceChildren(button('返回', { onclick: overview }), ok);
      (elevated ? ok : pw).focus();
    }

    function done() {
      fill(wrap, 
        h('div', { class: 'callout ok' }, icon('check', 16), h('span', null, '已轮换为第 ' + info.version + ' 版，正在推送到在线节点和客户端。')),
        h('dl', { class: 'kv' },
          h('dt', null, '之前'), h('dd', null, fp(info.previous, 'old')),
          h('dt', null, '现在'), h('dd', null, fp(info.fingerprint, 'new')),
          h('dt', null, '版本'), h('dd', null, '第 ' + info.version + ' 版'),
          h('dt', null, '轮换时间'), h('dd', null, info.rotatedAt ? fmtTime(info.rotatedAt) : '—')));
      foot.replaceChildren(button('完成', { kind: 'primary', onclick: () => d.close() }));
      toast('密钥已轮换');
    }

    overview();
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

  // dropsText：最近 24 小时断线次数，没断过时为空
  const dropsText = (n) => (n.drops24h ? '24 小时断线 ' + n.drops24h + ' 次' : '');

  function nodeStatusBadge(n) {
    if (n.pendingUninstall) return badge('待卸载', 'warn');
    // 刚断开、还在宽限期内（一般几秒就重连上）：不显示离线
    if (n.status === 'online' && n.reconnecting) return h('span', { class: 'badge warn', title: '连接刚断开，正在重连' }, '重连中');
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

  // copyBox 显示一段可复制的文字；labeled 时复制按钮带文字（安装命令这种主要操作）
  function copyBox(text, label = '已复制', labeled = false) {
    return h('div', { class: 'copybox' }, h('code', null, text), labeled
      ? button('复制', { small: true, icon: 'copy', onclick: () => copyText(text, label) })
      : iconButton('copy', '复制', () => copyText(text, label)));
  }

  function installBlock(res) {
    const parts = [];
    if (res.installCommand) {
      parts.push(h('p', { class: 'dialog-text' }, '在节点上以 root 执行下面的命令（Debian / Ubuntu / 红帽系等 systemd 系统，amd64 或 arm64）：'));
      parts.push(copyBox(res.installCommand, '安装命令已复制', true));
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

  // mirrorInput 是下载代理输入框，suggestions 作为下拉候选（默认代理和其他节点用过的）。
  function mirrorInput(value, suggestions) {
    const listId = 'mirrors-' + Math.random().toString(36).slice(2);
    const el = input({ value: value || '', placeholder: '留空直连 GitHub，例如 https://ghproxy.example.com/', maxlength: 200, list: listId, inputmode: 'url' });
    return [el, h('datalist', { id: listId }, (suggestions || []).map((m) => h('option', { value: m })))];
  }

  const MIRROR_HINT = '节点连不上 GitHub 时填，下载地址变成"代理 + GitHub 地址"，以后升级也用它。校验和由面板核对，代理改不了文件。';

  // 节点的地区分类：客户端的官方节点按它分组（顺序固定），必须选
  const REGIONS = [['cn', '中国大陆'], ['hmt', '港澳台'], ['intl', '境外']];
  const regionLabel = (r) => (REGIONS.find((x) => x[0] === r) || [])[1];
  const regionBadge = (r) => (regionLabel(r) ? badge(regionLabel(r), 'outline') : badge('未分类', 'warn'));
  function regionPicker(value) {
    let cur = value || '';
    const el = segmented(REGIONS, cur, (v, set) => { cur = v; set(v); }, { label: '地区' });
    return { el, value: () => cur };
  }

  function createNodeDialog(onDone, data) {
    const name = input({ maxlength: 40, placeholder: '例如：东京 1', required: true });
    const region = regionPicker('');
    const ag = (data && data.agent) || {};
    const [mirror, mirrorList] = mirrorInput(data && data.defaultMirror, data && data.mirrors);
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button('添加', { kind: 'primary', type: 'submit' });
    const d = openDialog({
      title: '添加节点',
      titleIcon: 'server',
      wide: true,
      body: [field('名称', name, '只在面板上显示，之后可以改'),
        field('地区', region.el, '客户端里的官方节点按地区分组：中国大陆、港澳台、境外'),
        ag.source === 'local' ? null : field('下载代理（可选）', h('div', null, mirror, mirrorList), MIRROR_HINT), err],
      actions: [button('取消', { onclick: () => d.close() }), submit],
      onSubmit: () => busy(submit, async () => {
        err.classList.remove('show');
        try {
          if (!region.value()) throw new Error('请选择节点所在地区');
          const res = await api('POST', '/nodes', { name: name.value, downloadMirror: mirror.value, region: region.value() });
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
    const box = listCard();
    const notice = h('div');
    let data = null;
    p.actions.append(
      button('刷新', { icon: 'refresh', onclick: () => load() }),
      button('排序', { icon: 'sort', onclick: () => data && (data.nodes || []).length > 1 && nodeOrderDialog(data.nodes, () => load()) }),
      button('升级', { icon: 'upgrade', onclick: () => data && upgradeNodesDialog(data, () => load()) }),
      button('限额', { icon: 'layers', onclick: () => quotasDialog() }),
      button('密钥', { icon: 'key', onclick: () => secretDialog() }),
      button('执行命令', { icon: 'terminal', onclick: () => data && execDialog(data.nodes) }),
      button('添加节点', { kind: 'primary', icon: 'plus', onclick: () => createNodeDialog(() => load(), data) }));
    p.body.append(h('div', { class: 'fill' }, notice, box));
    box.append(loadingBox());
    p.onLayout(() => { if (data) draw(); });

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
      const ag = data.agent || {};
      morphChildren(notice, (ag.version ? [] : [h('div', { class: 'callout warn mb' }, icon('alert', 16),
        h('span', null, ag.error ? '暂时不能安装和升级节点：' + ag.error : '正在从 GitHub 查询 agent 最新版本…'))]));
      const agentText = ag.version ? ' · agent 最新 ' + ag.version + (ag.source === 'github' ? '（GitHub ' + (ag.repo || '') + '）' : '（本地目录）') : '';
      const list = data.nodes || [];
      if (!list.length) {
        box.replaceChildren(stateBox('server', '还没有节点', '添加一台 VPS，面板会在上面运行 frps，并负责配置、监控、升级和卸载。',
          button('添加节点', { kind: 'primary', icon: 'plus', onclick: () => createNodeDialog(() => load(), data) })));
        return;
      }
      const pct = (used, total) => (total > 0 ? Math.round((used / total) * 100) : 0) + '%';
      fillList(box,
        dataList([{ label: '节点' }, { label: '状态' }, { label: 'frps' }, { label: '资源' }, { label: '网络' }, { label: 'agent' }, { label: '', cls: 'actions' }], list,
          (n) => {
            const r = n.report;
            const info = n.info || {};
            const fs = r && r.frps;
            const [fl, fk] = fs ? (FRPS_STATE[fs.state] || [fs.state]) : [null];
            const href = '#/nodes/' + encodeURIComponent(n.id);
            return [
              h('td', null, h('a', { class: 'node-link', href }, n.name), ' ', regionBadge(n.region), n.official ? [' ', badge('开放', 'accent')] : null,
                h('div', { class: 'cell-sub' }, [info.hostname, n.client && n.client.addr].filter(Boolean).join(' · ') || '—')),
              h('td', null, nodeStatusBadge(n),
                h('div', { class: 'cell-sub' }, n.status === 'online' ? dropsText(n) : n.lastSeen ? ['最后在线 ', timeEl(n.lastSeen)] : '')),
              h('td', null, fs ? [badge(fl, fk), h('div', { class: 'cell-sub' }, fs.state === 'running' ? ':' + fs.bindPort + ' · ' + fs.clients + ' 个客户端' : (fs.error || ''))]
                : h('span', { class: 'faint' }, '—')),
              h('td', { class: 'res-cell' }, r ? [meterCell('CPU', r.cpu, 100, r.cpu.toFixed(0) + '%'), meterCell('内存', r.memUsed, r.memTotal, fmtBytes(r.memUsed))]
                : h('span', { class: 'faint' }, '—')),
              h('td', { class: 'nowrap' }, r ? h('div', { class: 'cell-sub' }, '↓ ' + fmtRate(n.netRxRate), h('br'), '↑ ' + fmtRate(n.netTxRate)) : h('span', { class: 'faint' }, '—')),
              h('td', { class: 'nowrap' }, info.agentVersion ? h('code', null, info.agentVersion) : h('span', { class: 'faint' }, '—'),
                n.upgradeTo ? h('div', null, badge('可升级', 'info')) : null),
              h('td', { class: 'actions' }, h('a', { class: 'btn btn-sm', href }, '管理'))];
          },
          (n) => {
            const r = n.report;
            const info = n.info || {};
            const fs = r && r.frps;
            const [fl] = fs ? (FRPS_STATE[fs.state] || [fs.state]) : [null];
            return {
              href: '#/nodes/' + encodeURIComponent(n.id),
              title: [n.name, regionBadge(n.region), n.official ? badge('开放', 'accent') : null],
              trail: [nodeStatusBadge(n), icon('chevron', 16)],
              meta: [
                fs ? 'frps ' + fl + (fs.state === 'running' ? ' :' + fs.bindPort + ' · ' + fs.clients + ' 个客户端' : '') : null,
                r ? '↓ ' + fmtRate(n.netRxRate) + ' ↑ ' + fmtRate(n.netTxRate) : null,
                info.agentVersion ? 'agent ' + info.agentVersion + (n.upgradeTo ? '（可升级）' : '') : null,
                n.status !== 'online' && n.lastSeen ? ['最后在线 ', timeEl(n.lastSeen)] : null,
                n.status === 'pending' ? '还没有安装 agent' : null,
                dropsText(n),
              ],
              extra: r ? h('div', { class: 'row-meters' },
                meterCell('CPU', r.cpu, 100, r.cpu.toFixed(0) + '%'), meterCell('内存', r.memUsed, r.memTotal, pct(r.memUsed, r.memTotal))) : null,
            };
          }),
        h('div', { class: 'card-foot' }, '共 ' + list.length + ' 个节点，' + list.filter((n) => n.status === 'online').length + ' 个在线' + agentText + ' · 每 3 秒刷新'));
    }

    await load();
    autoRefresh(p, () => load(true));
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

  const NODE_TABS = [['overview', '概况'], ['config', '配置'], ['proxies', '代理'], ['logs', '日志'], ['exec', '命令'], ['settings', '设置']];

  async function pageNode(p, id) {
    let n = null;
    // 当前标签记在地址里（#/nodes/<ID>/<标签>），刷新、分享链接后还在这一页
    let tab = NODE_TABS.some((t) => t[0] === p.params[1]) ? p.params[1] : 'overview';
    let settingsPane = 'basic'; // "设置"页当前的分组
    const tabs = segmented(NODE_TABS, tab, (v, set) => {
      tab = v;
      set(v);
      history.replaceState(null, '', '#/nodes/' + encodeURIComponent(id) + '/' + v);
      drawTab();
    }, { label: '节点页面' });
    // 标签栏固定，下面的内容区占满剩余高度：列表、日志、编辑器在里面自己滚动
    const content = h('div', { class: 'tab-body grow' });
    p.actions.append(button('刷新', { icon: 'refresh', onclick: () => load() }));
    p.setHead('节点', '', '#/nodes');
    p.body.append(loadingBox());
    p.onLayout(() => { if (n && (tab === 'proxies' || tab === 'exec')) drawTab(); });

    async function load(quiet) {
      try {
        const v = await api('GET', '/nodes/' + encodeURIComponent(id));
        if (!p.alive()) return;
        const first = !n;
        n = v;
        p.setHead(h('span', { class: 'title-row' }, h('span', { class: 't' }, n.name), nodeStatusBadge(n)),
          [n.info && n.info.hostname, n.client && n.client.addr].filter(Boolean).join(' · '), '#/nodes');
        if (first) {
          p.body.replaceChildren(h('div', { class: 'fill' }, h('div', { class: 'toolbar' }, tabs), content));
          drawTab();
        } else if (tab === 'overview') {
          morphChildren(content, [tabOverview()]);
        } else if (tabRefresh) {
          await tabRefresh();
        }
      } catch (e) {
        if (!quiet && p.alive()) p.body.replaceChildren(errorBox(e, () => load()));
      }
    }

    // tabRefresh 是当前标签页的局部刷新（代理列表用），自动刷新时调用
    let tabRefresh = null;

    function drawTab() {
      tabRefresh = null;
      const fn = { overview: tabOverview, config: tabConfig, proxies: tabProxies, logs: tabLogs, exec: tabExec, settings: tabSettings }[tab];
      content.replaceChildren(fn());
    }

    const offlineBox = () => h('div', { class: 'card grow list-card' },
      stateBox('server', '节点不在线', n.status === 'pending' ? '还没有安装 agent。' : '节点上线后才能查看。'));

    const section = (title, sub, body, cls) => h('section', { class: 'card' + (cls ? ' ' + cls : '') },
      h('div', { class: 'card-head' }, h('h2', null, title), sub ? h('span', null, sub) : null), h('div', { class: 'card-body' }, body));
    const none = () => h('span', { class: 'faint' }, '—');
    const tile = (label, value, sub, cls) => h('div', { class: 'tile' + (cls ? ' ' + cls : '') },
      h('div', { class: 'tile-label' }, label),
      h('div', { class: 'tile-value', title: typeof value === 'string' ? value : null }, value == null || value === '' ? none() : value),
      sub ? h('div', { class: 'tile-sub', title: sub }, sub) : null);
    const meterTile = (label, used, total, sub) => h('div', { class: 'tile' },
      h('div', { class: 'tile-label' }, label, h('b', null, (total > 0 ? Math.round((used / total) * 100) : 0) + '%')),
      meter(used, total), sub ? h('div', { class: 'tile-sub', title: sub }, sub) : null);

    function tabOverview() {
      const info = n.info || {};
      const r = n.report;
      const fs = r && r.frps;
      const parts = [];
      if (n.status === 'pending') {
        parts.push(h('div', { class: 'callout warn span-all' }, icon('alert', 16),
          h('span', null, '还没有安装 agent。添加节点时给出的安装命令 24 小时内有效，找不到或过期了就重新生成。'),
          button('重新生成安装命令', { small: true, kind: 'primary', onclick: (e) => resetToken(e.currentTarget, false) })));
      }
      if (n.pendingUninstall) {
        parts.push(h('div', { class: 'callout warn span-all' }, icon('alert', 16), h('span', null, '已标记卸载：节点下次上线时会自动卸载并从面板删除。'),
          button('取消卸载', { small: true, onclick: async () => { await api('POST', '/nodes/' + encodeURIComponent(id) + '/cancel-uninstall').catch(fail); load(); } })));
      }

      // 左边一张"运行状态"卡（资源 + frps），右边连接信息和系统
      const res = r ? h('div', { class: 'tiles' },
        meterTile('CPU', r.cpu, 100),
        meterTile('内存', r.memUsed, r.memTotal, fmtBytes(r.memUsed) + ' / ' + fmtBytes(r.memTotal)),
        meterTile('磁盘', r.diskUsed, r.diskTotal, fmtBytes(r.diskUsed) + ' / ' + fmtBytes(r.diskTotal)),
        tile('下载', fmtRate(n.netRxRate)),
        tile('上传', fmtRate(n.netTxRate)),
        tile('负载', r.load.map((x) => x.toFixed(2)).join(' '))) : h('p', { class: 'muted' }, '节点在线后显示资源占用');

      const [fl, fk] = fs ? (FRPS_STATE[fs.state] || [fs.state]) : [null];
      const synced = n.configRev === n.appliedRev;
      const alerts = [
        fs && fs.error ? h('div', { class: 'callout danger' }, icon('alert', 16), h('span', null, fs.error)) : null,
        n.applyError ? h('div', { class: 'callout danger' }, icon('alert', 16), h('span', null, '上次下发失败：' + n.applyError)) : null,
      ].filter(Boolean);
      if (n.status !== 'pending') parts.push(section('运行状态', [r ? '已运行 ' + fmtUptime(r.uptime) : null, info.frpVersion ? 'frp v' + info.frpVersion : null].filter(Boolean).join(' · '), [
        res,
        h('div', { class: 'tiles tiles-sep' },
          tile('frps', fs ? badge(fl, fk) : null),
          tile('监听端口', fs && fs.bindPort ? String(fs.bindPort) : null),
          h('div', { class: 'tile', title: '面板 v' + n.configRev + ' / 节点 v' + n.appliedRev }, h('div', { class: 'tile-label' }, '配置'),
            h('div', { class: 'tile-value' }, synced ? badge('已同步 v' + n.configRev, 'ok') : badge('未同步', 'warn'))),
          tile('客户端/代理/连接', fs ? fs.clients + ' / ' + fs.proxies + ' / ' + fs.conns : null),
          tile('今日流量', fs ? '↓ ' + fmtBytes(fs.trafficIn) + '  ↑ ' + fmtBytes(fs.trafficOut) : null, null, 'span-2')),
        alerts.length ? h('div', { class: 'alerts' }, alerts) : null]));

      const side = [];
      if (n.client) {
        side.push(section('连接信息', '只接受 MoeFrp 客户端', h('dl', { class: 'conn' },
          h('dt', null, '地址'), h('dd', null, n.client.addr ? copyBox(n.client.addr + ':' + n.client.port) : h('span', { class: 'faint' }, '节点上线后显示，或在"设置"里填域名')),
          h('dt', null, '认证'), h('dd', { class: 'muted' }, '没有 token：用户在客户端登录后，每次连接自动领一次性票据（绑定设备）'))));
      }

      if (n.status !== 'pending') side.push(section('系统', null, h('div', { class: 'tiles' },
        tile('系统', info.distro, info.arch ? info.os + '/' + info.arch : null),
        tile('内核', info.kernel),
        tile('IP', n.lastIp),
        tile('agent', info.agentVersion ? [info.agentVersion, n.upgradeTo ? [' ', badge('可升级', 'info')] : null] : null, n.upgradeTo ? '最新 ' + n.upgradeTo : null),
        tile('远程命令', info.agentVersion ? (info.execEnabled ? '允许' : '已关闭') : null),
        n.status === 'online'
          ? tile('上线', n.connectedAt ? timeEl(n.connectedAt) : null, dropsText(n) || '24 小时内没有断线')
          : tile('最后在线', n.lastSeen ? timeEl(n.lastSeen) : null, '添加于 ' + fmtDate(n.createdAt)))));
      // 宽屏上也整行堆叠、内容横向铺开：并排的卡片高度对不齐，拉伸又会留白
      return h('div', { class: 'stack' }, parts, side);
    }

    // tabConfig 是 frps 配置表单：字段由面板的 schema 生成（shared/frpsset），分组显示，每项带说明。
    function tabConfig() {
      const card = h('div', { class: 'card grow fill-card' }, loadingBox());
      (async () => {
        let sc;
        try {
          sc = await frpsSchema();
        } catch (e) {
          card.replaceChildren(errorBox(e));
          return;
        }
        const ctl = {};
        const result = h('div', { hidden: true });
        const show = (el) => { result.replaceChildren(el); result.hidden = false; };
        const fill = (vals) => {
          for (const f of sc.fields) {
            const el = ctl[f.key];
            el.classList.remove('invalid');
            if (f.kind === 'bool') el.checked = !!vals[f.key];
            else if (f.kind === 'int') el.value = String(vals[f.key] || 0);
            else el.value = vals[f.key] || '';
          }
        };
        const read = () => {
          const out = {};
          for (const f of sc.fields) {
            const el = ctl[f.key];
            out[f.key] = f.kind === 'bool' ? el.checked : f.kind === 'int' ? (Number(el.value) || 0) : el.value.trim();
          }
          return out;
        };
        const control = (f) => {
          let el;
          if (f.kind === 'bool') el = h('input', { type: 'checkbox' });
          else if (f.kind === 'select') el = h('select', { class: 'input' }, f.options.map((o) => h('option', { value: o }, o)));
          else el = input({ type: f.kind === 'int' ? 'number' : 'text', min: f.min, max: f.max, placeholder: f.placeholder,
            class: 'input' + (f.kind === 'ports' ? ' mono' : ''), inputmode: f.kind === 'int' ? 'numeric' : null });
          ctl[f.key] = el;
          if (f.kind === 'bool') {
            return h('div', { class: 'field bool' }, h('label', { class: 'check' }, el, h('span', { class: 'field-label' }, f.label)),
              f.hint ? h('span', { class: 'field-hint' }, f.hint) : null);
          }
          return field(f.label, el, f.hint);
        };
        // 按分组切换，一次显示一组（每组 4–6 项）：手机上也一屏放下，不用滚动
        const SHORT = { basic: '基础', transport: '传输', vhost: '虚拟主机', advanced: '高级' };
        let group = sc.groups[0].key;
        const groups = sc.groups.map((g) => {
          const el = h('section', { class: 'cfg-group' },
            g.hint ? h('p', { class: 'cfg-group-hint' }, g.label + '：' + g.hint) : null,
            h('div', { class: 'cfg-grid' }, sc.fields.filter((f) => f.group === g.key).map(control)));
          el.dataset.group = g.key;
          return el;
        });
        const groupTabs = segmented(sc.groups.map((g) => [g.key, SHORT[g.key] || g.label]), group, (v, set) => showGroup(v, set), { label: '配置分组' });
        function showGroup(key, set) {
          group = key;
          (set || groupTabs.setValue)(key);
          for (const el of groups) el.hidden = el.dataset.group !== key;
        }
        showGroup(group);
        fill(n.settings || sc.defaults);

        const save = button(n.status === 'online' ? '保存并应用' : '保存', {
          kind: 'primary', small: true,
          onclick: () => busy(save, async () => {
            Object.values(ctl).forEach((el) => el.classList.remove('invalid'));
            try {
              const res = await api('PUT', '/nodes/' + encodeURIComponent(id) + '/settings', read());
              const r = res.result;
              if (!res.pushed) {
                show(h('div', { class: 'callout info' }, icon('info', 16), h('span', null, '已保存（v' + res.rev + '）。节点不在线，上线后自动下发。')));
              } else if (r.applied) {
                show(h('div', { class: 'callout ok' }, icon('check', 16), h('span', null, '已生效（v' + res.rev + '），frps 已用新配置重启。' + (r.warning ? ' 提示：' + r.warning : ''))));
                toast('frps 配置已生效');
              } else {
                show(h('div', { class: 'callout danger' }, icon('alert', 16),
                  h('span', null, r.error + (r.rolledBack ? '。frps 已恢复为之前的配置继续运行。' : ''))));
              }
              n = await api('GET', '/nodes/' + encodeURIComponent(id));
            } catch (e) {
              if (e.handled) return;
              const fields = (e.details && e.details.fields) || [];
              for (const fe of fields) {
                if (ctl[fe.field]) {
                  ctl[fe.field].classList.add('invalid');
                  // 出错的项在别的分组里：切过去
                  const f = sc.fields.find((x) => x.key === fe.field);
                  if (f && f.group !== group) showGroup(f.group);
                  ctl[fe.field].focus();
                }
              }
              show(h('div', { class: 'callout ' + (e.code === 'node_failed' ? 'warn' : 'danger') }, icon('alert', 16), h('span', null, errText(e))));
            }
          }),
        });
        const synced = n.configRev === n.appliedRev;
        card.replaceChildren(
          h('div', { class: 'card-head' }, h('h2', null, 'frps 配置'),
            h('span', null, synced ? '节点上是 v' + n.appliedRev : '已保存 v' + n.configRev + '，节点上还是 v' + n.appliedRev),
            h('div', { class: 'head-btns' },
              button('默认值', { small: true, title: '填入新节点的默认配置（还没保存）', onclick: () => fill(sc.defaults) }),
              button('还原', { small: true, title: '放弃没保存的修改', onclick: () => { fill(n.settings || sc.defaults); result.hidden = true; } }),
              save)),
          h('div', { class: 'card-body cfg-body' }, groupTabs, result, groups,
            h('p', { class: 'cell-sub' }, '节点不设 token，只接受面板签发的登录票据。保存后 agent 先校验再重启 frps，启动失败（如端口被占用）自动恢复旧配置；配置在节点上加密保存。')));
      })();
      return card;
    }

    function tabProxies() {
      if (n.status !== 'online') return offlineBox();
      const box = h('div', { class: 'card list-card grow' }, loadingBox());
      const fill = async () => {
        try {
          const res = await api('GET', '/nodes/' + encodeURIComponent(id) + '/proxies');
          if (!box.isConnected && tabRefresh !== fill) return;
          const list = (res.proxies || []).sort((a, b) => (b.online - a.online) || a.name.localeCompare(b.name));
          if (!list.length) {
            fillList(box, stateBox('inbox', '还没有代理', '客户端连上这个节点并开启代理后显示在这里。'));
            return;
          }
          const traffic = (x) => '↓ ' + fmtBytes(x.trafficIn) + ' · ↑ ' + fmtBytes(x.trafficOut);
          fillList(box, dataList([{ label: '名称' }, { label: '类型' }, { label: '用户' }, { label: '状态' }, { label: '连接' }, { label: '今日流量' }, { label: '上线时间' }], list,
            (x) => [h('td', null, h('code', null, x.name)), h('td', null, x.type), h('td', null, x.user || h('span', { class: 'faint' }, '—')),
              h('td', null, x.online ? badge('在线', 'ok') : badge('离线')), h('td', null, String(x.conns)),
              h('td', { class: 'nowrap' }, traffic(x)),
              h('td', { class: 'nowrap cell-sub' }, x.lastStart || '—')],
            (x) => ({
              title: h('code', { class: 't' }, x.name),
              trail: x.online ? badge('在线', 'ok') : badge('离线'),
              meta: [x.type, x.user, x.conns + ' 个连接', traffic(x)],
            })),
          h('div', { class: 'card-foot' }, '在线 ' + list.filter((x) => x.online).length + ' / 共 ' + list.length + ' 个（包括最近下线的）'));
        } catch (e) {
          fillList(box, errorBox(e));
        }
      };
      fill();
      tabRefresh = fill;
      return box;
    }

    function tabLogs() {
      if (n.status !== 'online') return offlineBox();
      const pre = h('pre', { class: 'term' }, '加载中…');
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
      return h('div', { class: 'card grow fill-card' },
        h('div', { class: 'card-head' }, h('h2', null, '最近 500 行'), h('span', null, '完整日志：journalctl -u moefrp-agent'),
          h('div', { class: 'head-btns' }, button('刷新', { small: true, icon: 'refresh', onclick: load }))),
        h('div', { class: 'card-body' }, pre));
    }

    function tabExec() {
      const disabled = n.info && n.info.execEnabled === false;
      const box = h('div', { class: 'card list-card grow' });
      const loadHist = async () => {
        try {
          const res = await api('GET', '/nodes/' + encodeURIComponent(id) + '/tasks');
          const list = res.tasks || [];
          const st = (t) => (t.status === 'running' ? badge('运行中', 'info') : t.status === 'done' ? badge('成功', 'ok') : badge('失败', 'danger'));
          const open = (t) => async () => {
            try {
              const full = await api('GET', '/tasks/' + encodeURIComponent(t.id));
              openDialog({ title: t.kind === 'exec' ? '命令结果' : '升级结果', wide: true, body: [h('pre', { class: 'term' }, '$ ' + full.command), taskResult(full, n)] });
            } catch (e) {
              fail(e);
            }
          };
          fillList(box, head, list.length
            ? dataList([{ label: '时间' }, { label: '类型' }, { label: '内容' }, { label: '结果' }, { label: '', cls: 'actions' }], list,
              (t) => [h('td', { class: 'nowrap' }, timeEl(t.createdAt)), h('td', { class: 'nowrap' }, t.kind === 'exec' ? '命令' : '升级'),
                h('td', null, h('code', { class: 'clip' }, t.command)), h('td', { class: 'nowrap' }, st(t)),
                h('td', { class: 'actions' }, button('查看', { small: true, onclick: open(t) }))],
              (t) => ({
                title: h('code', { class: 't' }, t.command),
                meta: [t.kind === 'exec' ? '命令' : '升级', timeEl(t.createdAt)],
                trail: [st(t), icon('chevron', 16)],
                onclick: open(t),
              }))
            : stateBox('inbox', '还没有执行过命令', null));
        } catch (e) {
          fillList(box, head, errorBox(e));
        }
      };
      const head = h('div', { class: 'card-head' }, h('h2', null, '历史'),
        h('span', null, disabled ? '安装时加了 --no-exec，已关闭远程命令' : '以 root 执行，需要确认密码，写入审计日志'),
        h('div', { class: 'head-btns' },
          iconButton('refresh', '刷新', loadHist),
          button('执行命令', { kind: 'primary', small: true, icon: 'terminal', disabled: disabled || n.status !== 'online', onclick: () => execDialog([n], n.id) })));
      box.append(head, loadingBox());
      loadHist();
      return box;
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

    // 设置：基本信息和官方节点是一张表单、一个保存按钮；维护操作是一组带说明的按钮
    function tabSettings() {
      const name = input({ value: n.name, maxlength: 40 });
      const region = regionPicker(n.region);
      const addr = input({ value: n.publicAddr, placeholder: n.lastIp || 'frp.example.com', maxlength: 253 });
      const [mirror, mirrorList] = mirrorInput(n.downloadMirror, []);
      const on = h('input', { type: 'checkbox' });
      on.checked = (n.roles || []).includes('user');
      const desc = input({ value: n.description || '', maxlength: 200, placeholder: '例如：东京机房，晚高峰稳定（可选）' });
      const old = n.info && n.info.agentVersion && !n.accessSupported;
      // 用户访问地址：域名手填；IP 留空自动检测，填 - 不显示
      const acc = n.access || { set: {}, effective: {} };
      const accSet = acc.set || {};
      const auto = (v) => (v ? '自动：' + v : '没检测到，可以手填');
      const accDomain = input({ value: accSet.domain || '', placeholder: '例如 sh1.example.com（可选）', maxlength: 253 });
      const accV4 = input({ value: accSet.ipv4 || '', placeholder: auto(acc.detectedV4), maxlength: 15, class: 'input mono' });
      const accV6 = input({ value: accSet.ipv6 || '', placeholder: auto(acc.detectedV6), maxlength: 45, class: 'input mono' });
      const save = button('保存', {
        kind: 'primary', small: true,
        onclick: () => busy(save, async () => {
          const access = { roles: on.checked ? ['user'] : [], description: desc.value };
          const accessChanged = on.checked !== (n.roles || []).includes('user') || access.description !== (n.description || '');
          const addrs = { domain: accDomain.value, ipv4: accV4.value, ipv6: accV6.value };
          const addrsChanged = addrs.domain.trim() !== (accSet.domain || '') || addrs.ipv4.trim() !== (accSet.ipv4 || '') || addrs.ipv6.trim() !== (accSet.ipv6 || '');
          try {
            if (!region.value()) throw new Error('请在"基本"里选择节点所在地区');
            await api('PATCH', '/nodes/' + encodeURIComponent(id), { name: name.value, publicAddr: addr.value, downloadMirror: mirror.value, region: region.value() });
            if (accessChanged) await api('PUT', '/nodes/' + encodeURIComponent(id) + '/official', access);
            if (addrsChanged) await api('PUT', '/nodes/' + encodeURIComponent(id) + '/access', addrs);
            toast(accessChanged ? '已保存，正在推送到节点' : '已保存');
            load(true);
          } catch (e) {
            fail(e);
          }
        }),
      });

      const online = n.status === 'online';
      const op = (label, sub, path, msg, opts = {}) => opButton(label, sub, Object.assign({ disabled: !online }, opts, {
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
      const uninstall = opButton('卸载', '删除节点上的全部文件', {
        icon: 'trash', danger: true,
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
      const del = opButton('只从面板删除', '不卸载节点上的程序', {
        icon: 'x', danger: true,
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

      // 分三组切换，一次显示一组：手机上也一屏放下。保存按钮保存"基本"和"开放与访问"两组（隐藏的那组也一起保存）
      const panes = {
        basic: h('div', { class: 'form-grid settings-grid' },
          field('名称', name), field('客户端连接地址', addr),
          field('地区', region.el, n.region ? '客户端里的官方节点按地区分组' : '还没分类，请选一个：客户端里的官方节点按地区分组'),
          h('div', { class: 'field span-all' }, h('span', { class: 'field-label' }, '下载代理（升级 agent、生成安装命令时用）'), mirror, mirrorList)),
        access: h('div', { class: 'official' },
          old ? h('div', { class: 'callout warn' }, icon('alert', 16), h('span', null, 'agent ' + n.info.agentVersion + ' 要升级到 1.4.0 才能由面板托管（没有 token、加密保存配置），升级前用户用不了这个节点。')) : null,
          h('div', { class: 'official-head' },
            h('label', { class: 'check setting-title' }, on, '开放给普通用户'),
            h('span', { class: 'muted' }, '管理员总能用；个别用户在"用户 → 节点权限"里单独允许或禁止')),
          field('给用户看的说明', desc, '显示在客户端的节点列表里。限额在节点列表的"限额"里按用户组设置'),
          h('div', { class: 'field-label access-title' }, '用户访问地址',
            h('span', { class: 'muted' }, '显示在客户端，用户复制去访问自己的 tcp / udp 代理。IP 留空自动检测，填 - 不显示')),
          h('div', { class: 'access-grid' }, field('域名', accDomain), field('IPv4', accV4), field('IPv6', accV6))),
        ops: h('div', { class: 'ops' },
          op('重启 frps', '客户端会短暂断开', '/restart', 'frps 已重启', { icon: 'refresh' }),
          op('升级 agent', n.upgradeTo ? '可升级到 ' + n.upgradeTo : '已是最新版本', '/upgrade', '已开始升级，结果在"命令"页的历史里',
            { disabled: !online || !n.upgradeTo, icon: 'upgrade', confirm: { title: '升级 agent', ok: '升级', message: 'agent 会下载新版本并重启，frps 会断开几秒。升级失败会自动回滚。' } }),
          opButton('重置令牌', '泄露或重装时用', { icon: 'key', onclick: (e) => resetToken(e.currentTarget, true) }),
          uninstall, del),
      };
      const showPane = (key) => {
        settingsPane = key;
        for (const [k, el] of Object.entries(panes)) el.hidden = k !== key;
        save.hidden = key === 'ops';
      };
      const paneTabs = segmented([['basic', '基本'], ['access', '开放与访问'], ['ops', '维护']], settingsPane, (v, set) => {
        set(v);
        showPane(v);
      }, { label: '设置分组' });
      showPane(settingsPane);
      return h('section', { class: 'card list-card set-card' },
        h('div', { class: 'card-head' }, h('h2', null, '节点设置'), h('div', { class: 'head-btns' }, save)),
        h('div', { class: 'card-body card-scroll node-set-body' }, paneTabs, panes.basic, panes.access, panes.ops));
    }

    await load();
    autoRefresh(p, () => load(true));
  }

  boot();
})();
