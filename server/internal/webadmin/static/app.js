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
        if (r.onclick) listen(tr, 'click', (e) => { if (!e.target.closest('button, a')) r.onclick(e); });
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
        listen(el, 'click', (e) => { if (!e.target.closest('button')) r.onclick(e); });
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
    { id: 'audit', label: '审计日志', short: '审计', icon: 'list', desc: '登录、注册、分享和管理操作的记录', render: pageAudit },
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
          opButton('节点权限', '官方节点、限额、节点凭证', { icon: 'server', onclick: () => userNodesDialog(u) })),
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

  // secretDialog 显示面板主密钥的指纹，可以轮换。
  async function secretDialog() {
    let info;
    try {
      info = await api('GET', '/node-secret');
    } catch (e) {
      fail(e);
      return;
    }
    const kv = h('dl', { class: 'kv' });
    const draw = () => kv.replaceChildren(
      h('dt', null, '指纹'), h('dd', null, h('code', null, info.fingerprint)),
      h('dt', null, '上次轮换'), h('dd', null, info.rotatedAt ? fmtTime(info.rotatedAt) : h('span', { class: 'faint' }, '从未轮换（第一次使用时自动生成）')));
    draw();
    const rotate = button('轮换密钥', {
      kind: 'danger',
      onclick: async () => {
        if (!(await confirmDialog({ title: '轮换节点密钥', danger: true, ok: '轮换',
          message: '所有已签发的登录票据立即作废，在线用户会断开几秒、自动领新票据重连；所有节点换用新的存储密钥重新加密本地配置。确定继续？' }))) return;
        await busy(rotate, async () => {
          try {
            info = await api('POST', '/node-secret/rotate');
            draw();
            toast('已轮换，正在推送到节点');
          } catch (e) {
            fail(e);
          }
        });
      },
    });
    const d = openDialog({
      title: '节点密钥',
      titleIcon: 'key',
      body: [h('p', { class: 'dialog-text' }, '面板主密钥用来签发节点登录票据、派生各节点的存储密钥。只存在面板数据库里，不会下发原值。'),
        h('div', { class: 'gap' }), kv,
        h('ul', { class: 'hints' },
          h('li', null, '怀疑密钥泄露（例如数据库备份外泄）时轮换。'),
          h('li', null, '离线节点上线后自动换用新密钥；节点离线超过 7 天，重启后要等面板连上才能启动 frps。'))],
      actions: [button('关闭', { onclick: () => d.close() }), rotate],
    });
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

  function createNodeDialog(onDone, data) {
    const name = input({ maxlength: 40, placeholder: '例如：东京 1', required: true });
    const ag = (data && data.agent) || {};
    const [mirror, mirrorList] = mirrorInput(data && data.defaultMirror, data && data.mirrors);
    const err = h('div', { class: 'form-error', role: 'alert' });
    const submit = button('添加', { kind: 'primary', type: 'submit' });
    const d = openDialog({
      title: '添加节点',
      titleIcon: 'server',
      wide: true,
      body: [field('名称', name, '只在面板上显示，之后可以改'),
        ag.source === 'local' ? null : field('下载代理（可选）', h('div', null, mirror, mirrorList), MIRROR_HINT), err],
      actions: [button('取消', { onclick: () => d.close() }), submit],
      onSubmit: () => busy(submit, async () => {
        err.classList.remove('show');
        try {
          const res = await api('POST', '/nodes', { name: name.value, downloadMirror: mirror.value });
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
              h('td', null, h('a', { class: 'node-link', href }, n.name), n.official ? [' ', badge('开放', 'accent')] : null,
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
              title: [n.name, n.official ? badge('开放', 'accent') : null],
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

  async function pageNode(p, id) {
    let n = null;
    let tab = 'overview';
    const tabs = segmented([['overview', '概况'], ['config', '配置'], ['proxies', '代理'], ['logs', '日志'], ['exec', '命令'], ['settings', '设置']], tab, (v, set) => {
      tab = v;
      set(v);
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
        const groups = sc.groups.map((g) => h('section', { class: 'cfg-group' },
          h('div', { class: 'cfg-group-head' }, h('h3', null, g.label), g.hint ? h('span', null, g.hint) : null),
          h('div', { class: 'cfg-grid' }, sc.fields.filter((f) => f.group === g.key).map(control))));
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
          h('div', { class: 'card-body cfg-body' }, result, groups,
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
      const addr = input({ value: n.publicAddr, placeholder: n.lastIp || 'frp.example.com', maxlength: 253 });
      const [mirror, mirrorList] = mirrorInput(n.downloadMirror, []);
      const on = h('input', { type: 'checkbox' });
      on.checked = (n.roles || []).includes('user');
      const desc = input({ value: n.description || '', maxlength: 200, placeholder: '例如：东京机房，晚高峰稳定（可选）' });
      const old = n.info && n.info.agentVersion && !n.accessSupported;
      const save = button('保存', {
        kind: 'primary', small: true,
        onclick: () => busy(save, async () => {
          const access = { roles: on.checked ? ['user'] : [], description: desc.value };
          const accessChanged = on.checked !== (n.roles || []).includes('user') || access.description !== (n.description || '');
          try {
            await api('PATCH', '/nodes/' + encodeURIComponent(id), { name: name.value, publicAddr: addr.value, downloadMirror: mirror.value });
            if (accessChanged) await api('PUT', '/nodes/' + encodeURIComponent(id) + '/official', access);
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

      return h('div', { class: 'stack' },
        h('section', { class: 'card' },
          h('div', { class: 'card-head' }, h('h2', null, '节点设置'), h('div', { class: 'head-btns' }, save)),
          h('div', { class: 'card-body' },
            h('div', { class: 'form-grid settings-grid' },
              field('名称', name), field('客户端连接地址', addr),
              h('div', { class: 'field span-all' }, h('span', { class: 'field-label' }, '下载代理（升级 agent、生成安装命令时用）'), mirror, mirrorList)),
            h('div', { class: 'official' },
              old ? h('div', { class: 'callout warn' }, icon('alert', 16), h('span', null, 'agent ' + n.info.agentVersion + ' 要升级到 1.4.0 才能由面板托管（没有 token、加密保存配置），升级前用户用不了这个节点。')) : null,
              h('div', { class: 'official-head' },
                h('label', { class: 'check setting-title' }, on, '开放给普通用户'),
                h('span', { class: 'muted' }, '管理员总能用；个别用户在"用户 → 节点权限"里单独允许或禁止')),
              field('给用户看的说明', desc, '显示在客户端的节点列表里。限额在节点列表的"限额"里按用户组设置')))),
        h('section', { class: 'card' },
          h('div', { class: 'card-head' }, h('h2', null, '维护')),
          h('div', { class: 'card-body' }, h('div', { class: 'ops' },
            op('重启 frps', '客户端会短暂断开', '/restart', 'frps 已重启', { icon: 'refresh' }),
            op('升级 agent', n.upgradeTo ? '可升级到 ' + n.upgradeTo : '已是最新版本', '/upgrade', '已开始升级，结果在"命令"页的历史里',
              { disabled: !online || !n.upgradeTo, icon: 'upgrade', confirm: { title: '升级 agent', ok: '升级', message: 'agent 会下载新版本并重启，frps 会断开几秒。升级失败会自动回滚。' } }),
            opButton('重置令牌', '泄露或重装时用', { icon: 'key', onclick: (e) => resetToken(e.currentTarget, true) }),
            uninstall, del))));
    }

    await load();
    autoRefresh(p, () => load(true));
  }

  boot();
})();
