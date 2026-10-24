// 在样式生效前套用保存的主题（浅色 / 深色），避免先按系统主题画一帧再变。切换逻辑在 app.js。
'use strict';
try {
  const t = localStorage.getItem('moefrp-admin-theme');
  if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t;
} catch (e) { /* 存储不可用时跟随系统 */ }
