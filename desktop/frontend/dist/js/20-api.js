/* 20-api.js — 所有 Go 调用集中此处（§六纪律：界面禁止直呼 window.go.main.App）。
   mock 开关（§六设计）：v0.2 阶段账号/体检/更新走假数据，
   服务器清单/部署/连接状态走真实 Go 方法。 */
(function () {
  'use strict';

  /* window.go 在不同宿主里类型不同（WebView2 是对象，浏览器注入态可能是函数），
     只做结构化检查，不假设类型。 */
  function backend() {
    try {
      var app = window.go && window.go.main && window.go.main.App;
      return app || null;
    } catch (e) { return null; }
  }

  function call(method, args) {
    var app = backend();
    if (!app) {
      return Promise.reject(new Error('后端未就绪（请在桌面 App 内运行；浏览器里仅展示演示数据）'));
    }
    return app[method].apply(app, args || []);
  }

  /* mock 开关注释：值为 true 表示该域仍走前端假数据（M3 填充/后续接真） */
  var MOCK = {
    account: true,   // 订阅状态（v0.3 接真实账号）
    orders: true,    // 订单列表
    referral: true,  // 推广数据
    plans: true,     // 套餐
    doctor: false,   // 体检（M2-1 已接 App.Doctor）
    batch: true,     // 批量体检（M2 后续）
    sync: true,      // 同步配置（M3）
    update: true     // 更新检查（发版后接 GitHub Releases）
    // servers / deploy / proxyStatus / appInfo：不 mock，走真接口
  };

  window.MOCK = MOCK;
  window.DukouAPI = {
    hasBackend: function () { return !!backend(); },

    /* ── 服务器清单（真接口，§5.1） ── */
    listServers: function () { return call('ListServers'); },
    addServer: function (input) { return call('AddServer', [input]); },
    updateServer: function (id, input) { return call('UpdateServer', [id, input]); },
    refreshServerGeo: function (id) { return call('RefreshServerGeo', [id]); },
    removeServer: function (id) { return call('RemoveServer', [id]); },

    /* ── 部署与连接（真接口） ── */
    deploy: function (req) { return call('Deploy', [req]); },
    deployServer: function (id, input) { return call('DeployServer', [id, input]); },
    proxyStatus: function () { return call('ProxyStatus'); },
    connect: function (serverId) { return call('Connect', [serverId]); },
    disconnect: function () { return call('Disconnect'); },
    switchServer: function (serverId) { return call('SwitchServer', [serverId]); },

    /* ── 分层体检（真接口，M2-1 接入） ── */
    doctor: function (serverId) { return call('Doctor', [serverId]); },

    /* ── 残留清理（真接口，M2-4 接入） ── */
    cleanupResidue: function () { return call('CleanupResidue'); },

    /* ── 备份 / 导出 / 更新（v0.2 M3，真接口） ──
       exportBackup / importBackup / exportServers 会先弹系统文件对话框，
       返回 [结果, 是否取消]；取消时第二项为 true，界面不该报错。 */
    exportBackup: function (password) { return call('ExportBackup', [password]); },
    importBackup: function (password) { return call('ImportBackup', [password]); },
    exportServers: function (format) { return call('ExportServers', [format]); },
    checkUpdate: function () { return call('CheckUpdate'); },
    openExternal: function (url) { return call('OpenExternal', [url]); },

    /* ── 应用信息 ── */
    appInfo: function () { return call('AppInfo'); },
    configDir: function () { return call('ConfigDir'); },

    /* ── 账号与订阅（v0.3，契约 v1.0；真接口已就绪） ──
       注意：界面仍显示演示数据（MOCK.account 等为 true），
       接线时把对应开关置 false 即可，调用签名不再变化。 */
    sessionInfo: function () { return call('SessionInfo'); },
    loginRequestCode: function (email) { return call('LoginRequestCode', [email]); },
    loginVerify: function (email, verificationId, isExistingUser, code) {
      return call('LoginVerify', [email, verificationId, isExistingUser, code]);
    },
    logout: function () { return call('Logout'); },
    account: function () { return call('Account'); },
    plans: function () { return call('Plans'); },
    claimTrial: function () { return call('ClaimTrial'); },
    orders: function () { return call('Orders'); },
    referral: function () { return call('Referral'); },

    /* ── 增值服务（v1.1，契约 E10-E12；真接口已就绪） ── */
    products: function () { return call('Products'); },
    entitlements: function () { return call('Entitlements'); },
    serviceOrders: function () { return call('ServiceOrders'); },
    createServiceOrder: function (productId, note, scheduledAt) {
      return call('CreateServiceOrder', [productId, note || '', scheduledAt || '']);
    },

    /* ── AI 工具（v0.3，真接口）：检测 / 安装 / 启动 / 卸载 ──
       安装与卸载是长任务，进度经 ai:progress 事件推送（见 50-events.js）。
       返回值 { installed, version, binPath, changed, note }，note 是给人看的一句话。 */
    aiTools: function () { return call('AITools'); },
    aiToolInstall: function (id) { return call('AIToolInstall', [id]); },
    aiToolUninstall: function (id) { return call('AIToolUninstall', [id]); },
    aiToolLaunch: function (id) { return call('AIToolLaunch', [id]); }
  };
})();
