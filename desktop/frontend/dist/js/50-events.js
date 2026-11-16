/* 50-events.js — 订阅 Wails 后端事件（§5.2 事件名规范）。
   事件分发到 window.onXxx 钩子，由主脚本（index.html 内联）注册实现；
   本文件只负责"接线"，不写业务逻辑。 */
(function () {
  'use strict';

  function bind(name, hook) {
    if (typeof window.runtime === 'object' && window.runtime && typeof window.runtime.EventsOn === 'function') {
      window.runtime.EventsOn(name, function (data) {
        if (typeof window[hook] === 'function') window[hook](data);
      });
    }
  }

  bind('deploy:event', 'onDeployEvent');   // 部署流水线进度（step_start/done/fail/log）
  bind('proxy:status', 'onProxyStatus');   // 连接状态变化（M2 起有实质推送）
  bind('doctor:event', 'onDoctorEvent');   // 体检逐层推进（M2）
  bind('update:available', 'onUpdateAvailable'); // 启动后发现新版本
})();
