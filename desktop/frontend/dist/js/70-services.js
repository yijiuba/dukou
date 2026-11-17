/* 70-services.js — 增值服务页接线（契约 v1.1 E10-E12）
 *
 * 为什么是运行时注入而不是改 index.html：
 * 与 60-account.js 同一纪律（COORDINATION.md §三）——index.html 是共享高危文件，
 * 本文件整页接管 #page-svc 的内容，index.html 一行不动。
 *
 * 数据流：
 *   E10 products     → 商品卡（公开，未登录也展示）
 *   E11 entitlements → 「我持有的服务」条（需登录）
 *   E12b service_orders → 订单/预约状态弹窗
 *   E12 create       → 下单（人工收款，pending）
 */
(function () {
  'use strict';

  var API = window.DukouAPI;
  if (!API || typeof API.products !== 'function') return;

  var state = { products: [], ents: [], orders: [], busy: false };

  function $(s, r) { return (r || document).querySelector(s); }
  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function toast(m) { if (typeof window.toast === 'function') window.toast(m); }
  function yuan(cents) {
    var v = (cents || 0) / 100;
    return '¥' + (v % 1 === 0 ? v.toFixed(0) : v.toFixed(2));
  }
  function meta(p, key, dflt) {
    try { var m = typeof p.meta === 'string' ? JSON.parse(p.meta) : p.meta; return m && m[key] != null ? m[key] : dflt; }
    catch (e) { return dflt; }
  }
  function isLoggedIn() {
    return API.sessionInfo().then(function (s) { return !!(s && s.loggedIn); }).catch(function () { return false; });
  }

  /* ── 弹窗（复用页面现有样式类，自带遮罩） ── */
  function openDialog(title, bodyHTML, footHTML) {
    closeDialog();
    var mask = document.createElement('div');
    mask.id = 'svcMask';
    mask.style.cssText = 'position:fixed;inset:0;background:rgba(17,24,39,.45);display:flex;align-items:center;'
      + 'justify-content:center;z-index:80;padding:20px;';
    mask.innerHTML = '<div style="background:#fff;border-radius:16px;max-width:520px;width:100%;padding:24px 26px;'
      + 'box-shadow:0 20px 48px rgba(17,24,39,.2);max-height:82vh;overflow:auto">'
      + '<h2 style="font-size:16px;margin-bottom:10px">' + esc(title) + '</h2>'
      + '<div id="svcDlgBody" style="font-size:13px;line-height:1.9;color:#374151">' + bodyHTML + '</div>'
      + '<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:18px">' + (footHTML || '')
      + '<button class="btn" data-svc-close="1">关闭</button></div></div>';
    document.body.appendChild(mask);
    mask.addEventListener('click', function (e) {
      if (e.target === mask || e.target.closest('[data-svc-close]')) closeDialog();
    });
    return mask;
  }
  function closeDialog() {
    var m = document.getElementById('svcMask');
    if (m) m.remove();
  }

  /* ── 渲染整页 ── */
  function render() {
    var page = document.getElementById('page-svc');
    if (!page) return;
    Promise.all([
      API.products().catch(function () { return []; }),
      API.sessionInfo().catch(function () { return null; }),
    ]).then(function (r) {
      var products = r[0] || [];
      var sess = r[1] || {};
      state.products = products;
      if (!sess.loggedIn) { paint(page, sess, [], []); return; }
      return Promise.all([
        API.entitlements().catch(function () { return []; }),
        API.serviceOrders().catch(function () { return []; }),
      ]).then(function (r2) {
        paint(page, sess, r2[0] || [], r2[1] || []);
      });
    }).catch(function (e) {
      console.warn('[services] 渲染失败，保持原页面：', e);
    });
  }

  var SVC_STATUS = { pending: '待确认收款', paid: '已付款 · 待预约', done: '已交付', refunded: '已退款' };

  function paint(page, sess, ents, orders) {
    state.ents = ents; state.orders = orders;
    var loggedIn = !!sess.loggedIn;

    var heldPills = ents.map(function (e) {
      if (e.kind === 'course' || e.remaining == null) {
        return '<span class="held-pill ok">' + esc(e.name) + ' · 已开通</span>';
      }
      return '<span class="held-pill">' + esc(e.name) + ' · 待服务 <b>' + e.remaining + '</b> 次</span>';
    }).join('');
    if (!ents.length && loggedIn) {
      var pendingCount = orders.filter(function (o) { return o.status === 'pending'; }).length;
      heldPills = pendingCount
        ? '<span class="held-pill">' + pendingCount + ' 个订单待确认收款</span>'
        : '<span class="held-pill ok">暂无已购服务</span>';
    }

    var cards = state.products.map(function (p) {
      var owned = ents.some(function (e) { return e.refId === p.id; });
      var priceSub = p.delivery === 'self' ? '自助图文+视频' : '/次 · 人工服务';
      var btn;
      if (owned) {
        btn = '<button class="btn ghost" disabled>已开通</button>';
      } else if (p.kind === 'course') {
        btn = '<button class="btn" data-svc-buy="' + esc(p.id) + '">' + (loggedIn ? '立即购买' : '登录后购买') + '</button>';
      } else {
        btn = '<button class="btn" data-svc-buy="' + esc(p.id) + '">' + (loggedIn ? '预约服务' : '登录后预约') + '</button>';
      }
      return '<div class="svc">'
        + '<h4>' + esc(p.name) + '</h4>'
        + '<p>' + esc(p.summary) + '</p>'
        + '<div class="p">' + (p.priceCents ? yuan(p.priceCents) : '待定')
        + ' <small style="font-size:12px;color:#6b7280">' + priceSub + '</small></div>'
        + btn + '</div>';
    }).join('');

    var head = '<div class="page-head"><h2 style="display:flex;align-items:center">增值服务'
      + '<a href="javascript:void(0)" data-svc-orders="1" '
      + 'style="margin-left:auto;font-size:13px;font-weight:400;color:#0f766e;text-decoration:none">订单管理 ›</a></h2>'
      + '<div class="sub">AI 课程包与 1 对 1 注册陪跑，均为人工交付 · 订单、发票与退款在官网用户中心办理</div></div>';

    var held = '<div class="held-bar" id="heldBar">'
      + '<b>我持有的服务</b>'
      + (loggedIn ? (heldPills || '<span class="held-pill">暂无</span>')
        : '<span class="held-pill">登录后查看已购服务</span>')
      + '<a href="javascript:void(0)" data-svc-orders="1">查看 / 预约 ›</a></div>';

    page.innerHTML = head + held + '<div class="svc-grid">' + (cards || '<div class="svc"><h4>商品加载失败</h4><p>请检查网络后重试</p></div>') + '</div>';
  }

  function onPageClick(e) {
    var buy = e.target.closest('[data-svc-buy]');
    if (buy) { buyFlow(buy.dataset.svcBuy, buy); return; }
    if (e.target.closest('[data-svc-orders]')) { ordersFlow(); }
  }

  /* ── 购买 / 预约 ── */
  function buyFlow(productId, btn) {
    var p = state.products.filter(function (x) { return x.id === productId; })[0];
    if (!p) return;
    isLoggedIn().then(function (ok) {
      if (!ok) {
        if (typeof window.accLogin === 'function') { window.accLogin(); toast('登录后再回来预约'); return; }
        toast('请先登录'); return;
      }
      if (p.kind === 'course') { courseDialog(p); } else { serviceDialog(p); }
    });
  }

  function courseDialog(p) {
    var chapters = meta(p, 'chapters', []);
    var body = '<p style="margin-bottom:10px">' + esc(p.summary) + '</p>'
      + (chapters.length ? '<div style="font-size:12.5px;color:#6b7280;margin-bottom:4px">课程大纲</div><ol style="padding-left:18px;margin:0 0 12px">'
        + chapters.map(function (c) { return '<li>' + esc(c) + '</li>'; }).join('') + '</ol>' : '')
      + '<div style="background:#f8fafc;border:1px solid #eef0f3;border-radius:10px;padding:10px 12px;font-size:12.5px;color:#6b7280">'
      + '价格 <b style="color:#111827">' + yuan(p.priceCents) + '</b> · ' + esc(meta(p, 'updates', '永久更新'))
      + '<br>一期人工收款：提交后按客服指引转账，管理员确认后自动开通（一般 10 分钟内）</div>';
    var foot = '<button class="btn primary" data-svc-submit="' + esc(p.id) + '">提交订单</button>';
    openDialog(p.name, body, foot);
    $('#svcDlgBody').parentNode.querySelector('[data-svc-submit]').onclick = function () {
      submit(p.id, '', '');
    };
  }

  function serviceDialog(p) {
    var body = '<p style="margin-bottom:10px">' + esc(p.summary) + '</p>'
      + '<div style="background:#f8fafc;border:1px solid #eef0f3;border-radius:10px;padding:10px 12px;font-size:12.5px;color:#6b7280;margin-bottom:12px">'
      + '价格 <b style="color:#111827">' + yuan(p.priceCents) + '</b> · ' + esc(meta(p, 'duration', ''))
      + ' · ' + esc(meta(p, 'guarantee', '')) + '</div>'
      + '<label style="display:block;font-size:12.5px;color:#6b7280;margin-bottom:5px">希望的服务时间（可留空，付款后与客服约）</label>'
      + '<input id="svcWhen" type="date" style="width:100%;margin-bottom:12px">'
      + '<label style="display:block;font-size:12.5px;color:#6b7280;margin-bottom:5px">备注（你的时段偏好 / 要注册的平台）</label>'
      + '<textarea id="svcNote" rows="3" style="width:100%;font:inherit;border:1px solid #d1d5db;border-radius:8px;padding:8px 10px"></textarea>';
    var foot = '<button class="btn primary" data-svc-submit="' + esc(p.id) + '">提交预约</button>';
    openDialog(p.name, body, foot);
    $('#svcDlgBody').parentNode.querySelector('[data-svc-submit]').onclick = function () {
      var when = ($('#svcWhen') && $('#svcWhen').value) || '';
      var note = ($('#svcNote') && $('#svcNote').value) || '';
      submit(p.id, note, when ? when + 'T00:00:00Z' : '');
    };
  }

  function submit(productId, note, scheduledAt) {
    if (state.busy) return;
    state.busy = true;
    var btns = document.querySelectorAll('[data-svc-submit]');
    for (var i = 0; i < btns.length; i++) { btns[i].disabled = true; btns[i].textContent = '提交中…'; }
    API.createServiceOrder(productId, note, scheduledAt).then(function (o) {
      state.busy = false;
      closeDialog();
      toast('已提交 ' + o.productName + '，等管理员确认收款（一般 10 分钟内）');
      render();
    }).catch(function (e) {
      state.busy = false;
      for (var i = 0; i < btns.length; i++) { btns[i].disabled = false; btns[i].textContent = '重新提交'; }
      toast((e && e.message) || '提交失败，请重试');
    });
  }

  /* ── 我的服务订单 ── */
  function ordersFlow() {
    isLoggedIn().then(function (ok) {
      if (!ok) {
        if (typeof window.accLogin === 'function') window.accLogin();
        return;
      }
      API.serviceOrders().then(function (rows) {
        var body;
        if (!rows.length) {
          body = '<p style="color:#6b7280">还没有服务订单。课程包与陪跑提交后在这里查状态。</p>';
        } else {
          body = '<table style="width:100%;border-collapse:collapse;font-size:12.5px">'
            + '<tr style="color:#6b7280;text-align:left"><th style="padding:6px 4px;font-weight:400">商品</th>'
            + '<th style="font-weight:400">状态</th><th style="font-weight:400">提交</th></tr>'
            + rows.map(function (o) {
              return '<tr style="border-top:1px solid #eef0f3"><td style="padding:8px 4px">' + esc(o.productName)
                + '<br><span style="color:#9ca3af">' + esc(o.id) + '</span></td>'
                + '<td>' + esc(SVC_STATUS[o.status] || o.status) + '</td>'
                + '<td style="color:#6b7280">' + esc((o.createdAt || '').slice(0, 10)) + '</td></tr>';
            }).join('') + '</table>';
        }
        openDialog('我的服务订单', body, '');
      }).catch(function (e) { toast((e && e.message) || '订单加载失败'); });
    });
  }

  /* ── 挂接：页面切换 / 登录登出时刷新 ── */
  var origNav = window.nav;
  if (typeof origNav === 'function') {
    window.nav = function (p) {
      origNav.apply(this, arguments);
      if (p === 'svc') render();
    };
  }
  window.addEventListener('acc:login', render);
  window.addEventListener('acc:logout', render);

  document.addEventListener('DOMContentLoaded', function () {
    var page = document.getElementById('page-svc');
    if (page) page.addEventListener('click', onPageClick);
    if (!state.products.length) render();
  });
  if (document.readyState !== 'loading') {
    var p0 = document.getElementById('page-svc');
    if (p0) p0.addEventListener('click', onPageClick);
    render();
  }
})();
