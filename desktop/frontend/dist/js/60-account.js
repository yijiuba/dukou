/* 60-account.js — 账号与订阅界面（账号线 T9，契约 v1.0）。
   ────────────────────────────────────────────────────────────────
   为什么这个文件看起来"反常规"：
   本仓库同时有另一个会话在改 index.html（2154 行）。为了让账号线能与它
   完全并行、互不覆盖，这里采用「零接触注入」——**不修改 index.html 一个字节**，
   而是在运行时：
     1) 动态创建登录弹窗节点挂到 body；
     2) 找到"我的"页里既有的 DOM 锚点（#page-me / .me-head / #trialSt …）就地填充；
     3) 接管已存在的内联函数 claimTrial()（用同名全局函数覆盖，onclick 自然指向新实现）。
   见 COORDINATION.md §三 与 《并行会话协作约定》§三。
   ────────────────────────────────────────────────────────────────
   边界：界面只管展示与交互；locked / 等级 / 在线时长全部由服务端算好下发，
   客户端不做任何业务判定（防本地篡改，见契约 §7）。 */
(function () {
  'use strict';

  var API = window.DukouAPI;
  if (!API) return;
  /* 20-api.js 若还没暴露账号方法（例如后端绑定层尚未接线），静默跳过，
     不要让异常顶掉 index.html 原有的演示态。 */
  if (typeof API.sessionInfo !== 'function') return;

  /* 浏览器里没有后端，保持 index.html 的演示态，不注入任何东西 */
  if (!API.hasBackend()) return;

  var state = {
    loggedIn: false,
    email: '',
    account: null,
    accountErr: null, // 上次 Account 拉取失败的原因（展示失败态，绝不永久「加载中」）
    referral: null,
    referralErr: null,
    plans: null,
    pending: null // {email, verificationId, isExistingUser}
  };

  /* ── 小工具 ─────────────────────────────────────────────── */

  function $(sel, root) { return (root || document).querySelector(sel); }
  function el(tag, cls, html) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (html != null) n.innerHTML = html;
    return n;
  }
  function errText(e) {
    var s = (e && (e.message || e.toString())) || '未知错误';
    return String(s).replace(/^Error:\s*/, '');
  }
  function toast(msg) {
    if (typeof window.toast === 'function') { window.toast(msg); return; }
    console.log('[account]', msg);
  }
  /* 契约时间是 UTC ISO（2026-11-20T09:00:00Z）；界面只显示日期 */
  function fmtDate(iso) {
    if (!iso) return '—';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
  }
  function yuan(cents) { return '¥' + (cents / 100).toFixed(2).replace(/\.00$/, ''); }
  /* 会话失效类错误（token 作废且续期失败）：引导重新登录，而不是干等 */
  function isAuthErr(e) {
    var s = errText(e);
    return s.indexOf('重新登录') >= 0 || s.indexOf('登录已过期') >= 0
      || s.indexOf('session is invalid') >= 0;
  }
  /* 失败态共用 HTML：会话失效给「重新登录」，其它给「重试」 */
  function failAction(authErr) {
    return authErr
      ? '会话已失效 <a href="javascript:void(0)" onclick="accLogin()" style="color:#dc2626;text-decoration:none">重新登录 ›</a>'
      : '读取失败 <a href="javascript:void(0)" onclick="accRetry()" style="color:#0f766e;text-decoration:none">重试 ›</a>';
  }

  /* ── 样式（一次性注入） ─────────────────────────────────── */

  function injectStyle() {
    if (document.getElementById('accStyle')) return;
    var css = ''
      + '#accMask{position:fixed;inset:0;background:rgba(17,24,39,.45);display:flex;'
      + 'align-items:center;justify-content:center;z-index:9999}'
      + '#accCard{width:380px;max-width:92vw;background:#fff;border-radius:16px;padding:24px;'
      + 'box-shadow:0 20px 60px rgba(0,0,0,.22);font-size:14px;color:#111827}'
      + '#accCard h3{margin:0 0 6px;font-size:17px}'
      + '#accCard .acc-sub{font-size:12.5px;color:#6b7280;margin-bottom:16px;line-height:1.6}'
      + '#accCard input{width:100%;box-sizing:border-box;padding:10px 12px;border:1px solid #dfe3e8;'
      + 'border-radius:10px;font-size:14px;outline:none;margin-bottom:10px}'
      + '#accCard input:focus{border-color:#0f766e;box-shadow:0 0 0 3px rgba(15,118,110,.12)}'
      + '#accCard .acc-row{display:flex;gap:8px}'
      + '#accCard .acc-row input{flex:1;margin-bottom:0}'
      + '#accCard button{cursor:pointer;border:0;border-radius:10px;font-size:14px;padding:10px 14px;'
      + 'background:#0f766e;color:#fff;font-weight:600}'
      + '#accCard button:disabled{background:#cbd5d1;cursor:not-allowed}'
      + '#accCard button.ghost{background:#f3f4f6;color:#374151;font-weight:500}'
      + '#accCard .acc-err{color:#dc2626;font-size:12.5px;min-height:18px;margin:8px 0 4px}'
      + '#accCard .acc-foot{display:flex;gap:8px;margin-top:14px}'
      + '#accCard .acc-foot button{flex:1}'
      + '#accHint{font-size:12px;color:#9ca3af;margin-top:12px;line-height:1.6}'
      + '.acc-banner{display:flex;align-items:center;gap:10px;background:#fff7ed;border:1px solid #fed7aa;'
      + 'color:#9a3412;border-radius:12px;padding:12px 14px;font-size:13px;margin-bottom:14px}'
      + '.acc-banner b{color:#7c2d12}';
    document.head.appendChild(el('style', null, css)).id = 'accStyle';
  }

  /* ── 登录弹窗 ───────────────────────────────────────────── */

  var modal = null;

  function closeAuth() {
    if (modal && modal.parentNode) modal.parentNode.removeChild(modal);
    modal = null;
  }

  function openAuth() {
    injectStyle();
    closeAuth();
    var mask = el('div');
    mask.id = 'accMask';
    mask.innerHTML = ''
      + '<div id="accCard">'
      + '  <h3>登录 / 注册</h3>'
      + '  <div class="acc-sub">用邮箱验证码登录，首次登录自动创建账号。<br>'
      + '    代理商不需要登录也能正常使用；登录后才能领取试用、查看订阅与推广收益。</div>'
      + '  <input id="accEmail" type="email" placeholder="邮箱地址，例如 you@example.com" autocomplete="email">'
      + '  <div class="acc-row" id="accCodeRow" style="display:none">'
      + '    <input id="accCode" type="text" inputmode="numeric" maxlength="6" placeholder="6 位验证码">'
      + '    <button class="ghost" id="accResend" type="button">重发</button>'
      + '  </div>'
      + '  <div class="acc-err" id="accErr"></div>'
      + '  <div class="acc-foot">'
      + '    <button class="ghost" id="accCancel" type="button">取消</button>'
      + '    <button id="accSubmit" type="button">发送验证码</button>'
      + '  </div>'
      + '  <div id="accHint">收不到邮件？先看垃圾箱；QQ/163 邮箱对陌生域名较严格，'
      + '    把发件人加入白名单即可。</div>'
      + '</div>';
    document.body.appendChild(mask);
    modal = mask;

    var emailIn = $('#accEmail', mask);
    var codeIn = $('#accCode', mask);
    var codeRow = $('#accCodeRow', mask);
    var errBox = $('#accErr', mask);
    var submit = $('#accSubmit', mask);
    var resend = $('#accResend', mask);

    function setErr(m) { errBox.textContent = m || ''; }
    function busy(b, label) {
      submit.disabled = b;
      submit.textContent = label || submit.textContent;
    }

    mask.addEventListener('click', function (e) { if (e.target === mask) closeAuth(); });
    $('#accCancel', mask).onclick = closeAuth;
    emailIn.focus();

    var countdown = 0, timer = null;
    function startCountdown(sec) {
      countdown = sec || 60;
      resend.disabled = true;
      resend.textContent = countdown + 's';
      clearInterval(timer);
      timer = setInterval(function () {
        countdown -= 1;
        if (countdown <= 0) {
          clearInterval(timer);
          resend.disabled = false;
          resend.textContent = '重发';
        } else {
          resend.textContent = countdown + 's';
        }
      }, 1000);
    }

    function sendCode() {
      var email = (emailIn.value || '').trim();
      if (!email) { setErr('请先填写邮箱'); emailIn.focus(); return; }
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(email)) { setErr('邮箱格式不正确'); emailIn.focus(); return; }
      setErr('');
      busy(true, '发送中…');
      API.loginRequestCode(email).then(function (r) {
        state.pending = { email: email, verificationId: r.verificationId, isExistingUser: !!r.isExistingUser };
        codeRow.style.display = 'flex';
        emailIn.readOnly = true;
        submit.textContent = '登录';
        submit.disabled = false;
        startCountdown(r.retryAfterSec || 60);
        setErr('');
        codeIn.focus();
        if (r.isExistingUser) toast('验证码已发送，请查收邮箱');
        else toast('验证码已发送，首次登录将自动创建账号');
      }).catch(function (e) {
        busy(false, '发送验证码');
        setErr(errText(e));
      });
    }

    function doLogin() {
      if (!state.pending) { sendCode(); return; }
      var code = (codeIn.value || '').replace(/\s/g, '');
      if (code.length < 4) { setErr('请填写完整的验证码'); codeIn.focus(); return; }
      setErr('');
      busy(true, '登录中…');
      API.loginVerify(state.pending.email, state.pending.verificationId,
        state.pending.isExistingUser, code).then(function (res) {
        state.loggedIn = true;
        state.email = res.email || state.pending.email;
        if (res.account) state.account = res.account;
        state.pending = null;
        closeAuth();
        toast('登录成功');
        try { window.dispatchEvent(new CustomEvent('acc:login')); } catch (e) {}
        refresh();
      }).catch(function (e) {
        busy(false, '登录');
        setErr(errText(e));
      });
    }

    submit.onclick = function () { if (state.pending) doLogin(); else sendCode(); };
    resend.onclick = function () { state.pending = null; emailIn.readOnly = false; sendCode(); };
    codeIn.addEventListener('keydown', function (e) { if (e.key === 'Enter') doLogin(); });
    emailIn.addEventListener('keydown', function (e) { if (e.key === 'Enter') sendCode(); });
  }

  /* ── "我的"页渲染（就地填充既有节点，不新增结构） ───────── */

  function renderMe() {
    /* 整段包一层 try/catch：这是往别人写的页面上注入内容，
       index.html 结构随时可能被并行会话调整。渲染失败宁可保持原样，
       也绝不能让异常冒泡影响页面其它功能。 */
    try {
      renderMeInner();
    } catch (e) {
      if (window.console) console.warn('[account] 渲染失败，保持原页面：', e);
      /* 打包版没有 console 出口，toast 出来才能第一时间发现渲染异常 */
      if (typeof window.toast === 'function') toast('账号区渲染异常：' + (e && e.message || e));
    }
  }

  function renderMeInner() {
    var page = document.getElementById('page-me');
    if (!page) return;
    var head = $('.me-head', page);
    if (!head) return;

    /* 顶部账号行 */
    var avatar = $('.avatar', head);
    var emailEl = $('b', head);
    var tag = $('.tag', head);
    var logoutBtn = $('button', head);

    if (state.loggedIn) {
      if (emailEl) emailEl.textContent = state.email;
      if (tag) tag.textContent = '邮箱登录';
      if (avatar) avatar.textContent = (state.email || 'D').charAt(0).toUpperCase();
      if (logoutBtn) {
        logoutBtn.textContent = '退出登录';
        logoutBtn.onclick = function () { doLogout(); };
      }
    } else {
      if (emailEl) emailEl.textContent = '未登录';
      if (tag) tag.textContent = '本地模式';
      if (avatar) avatar.textContent = 'D';
      if (logoutBtn) {
        logoutBtn.textContent = '登录 / 注册';
        logoutBtn.onclick = function () { openAuth(); };
      }
    }

    renderLevel(head);
    renderTrial(page);
    renderPlanRow(page);
    renderReferral(page);
    renderBanner(page);
    renderLevelTable();
    /* 侧边栏状态与首页试用条也由真实账号驱动（2026-11-17 真机反馈：
       领过试用仍显示「试用未领取」，静态演示文字误导用户） */
    renderSideTrial();
    renderHomeTrial();
  }

  /* 等级行：文案 + 进度条 + 下一级提示 */
  function renderLevel(head) {
    var r = state.referral;
    // 结构：<div style="flex:1"><b>…</b><span class="tag">…</span><div class="lvl-text">…</div>
    //       <div class="level-bar"><i></i></div><div class="lvl-next">…</div></div>
    var kids = head.children[1] ? head.children[1].children : [];
    var textDiv = null, nextDiv = null;
    for (var i = 0; i < kids.length; i++) {
      if (kids[i].className === 'level-bar') continue;
      if (kids[i].tagName === 'DIV') {
        if (!textDiv) textDiv = kids[i]; else if (!nextDiv) nextDiv = kids[i];
      }
    }
    var bar = $('.level-bar i', head);
    if (!r) {
      if (textDiv) {
        textDiv.innerHTML = state.loggedIn
          ? (state.referralErr
              ? failAction(isAuthErr(state.referralErr))
              : '等级加载中…')
          : '登录后查看等级与有效在线时长 <a href="javascript:void(0)" onclick="accLogin()" style="color:#0f766e;text-decoration:none;margin-left:8px">去登录 ›</a>';
      }
      if (nextDiv) nextDiv.textContent = state.loggedIn ? '' : '推广大使按有效在线时长与成交单数双轨升级';
      if (bar) bar.style.width = '0%';
      return;
    }
    var next = nextLevelOf(r);
    var nextAt = next ? next.minHours : 0;
    var pct = nextAt > 0 ? Math.min(100, Math.round(r.onlineHours / nextAt * 100)) : 100;
    if (textDiv) {
      textDiv.innerHTML = 'Lv' + r.level + ' ' + r.levelName + ' · 有效在线 ' + r.onlineHours + 'h'
        + ' <a href="javascript:void(0)" onclick="openModal(\'levelModal\')" '
        + 'style="color:#0f766e;text-decoration:none;margin-left:8px">等级说明 ›</a>';
    }
    if (bar) bar.style.width = pct + '%';
    if (nextDiv) nextDiv.textContent = r.nextLevelHint || '';
  }

  /* 下一档等级：取 levels[] 中 level 更大的最近一档（后台可增删，客户端不写死门槛） */
  function nextLevelOf(r) {
    var lv = (r && r.levels) || [];
    for (var i = 0; i < lv.length; i++) {
      if (lv[i].level > r.level) return lv[i];
    }
    return null;
  }

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  /* 等级说明弹窗：表格按 levels[] 渲染，规则文案按 rules{} 渲染，全部来自后台配置 */
  function renderLevelTable() {
    var table = document.getElementById('levelTable');
    var note = document.getElementById('levelRulesNote');
    var r = state.referral;
    if (!table || !r || !r.levels || !r.levels.length) return;
    var html = '<tr><th>等级</th><th>升级条件（满足其一）</th><th style="width:60px">返佣</th></tr>';
    for (var i = 0; i < r.levels.length; i++) {
      var l = r.levels[i];
      var cond;
      if (l.minHours > 0 && l.minOrders > 0) cond = '有效在线满 ' + l.minHours + ' 小时，或累计成交 ' + l.minOrders + ' 单';
      else if (l.minHours > 0) cond = '有效在线满 ' + l.minHours + ' 小时';
      else if (l.minOrders > 0) cond = '累计成交 ' + l.minOrders + ' 单';
      else cond = l.note || '注册即是';
      var hl = l.level === r.level ? ' style="background:#f8fafc"' : '';
      html += '<tr' + hl + '><td><b>Lv' + l.level + ' ' + esc(l.name) + '</b></td><td>'
        + esc(cond) + '</td><td>' + l.commissionPct + '%</td></tr>';
    }
    table.innerHTML = html;
    if (note) note.innerHTML = rulesNote(r.rules);
  }

  function rulesNote(rules) {
    if (!rules) return '';
    var period = { instant: '实时结算', weekly: '按周结算', monthly: '按月结算' }[rules.settlePeriod] || '按月结算';
    var scope = (rules.allowSecondLevel && rules.secondLevelPct > 0)
      ? '一级好友下单返你，二级返 ' + rules.secondLevelPct + '%'
      : '只做一级（好友直接下单返你），不做二级';
    var base = rules.commissionBase === 'net_profit' ? '按订单净利计佣' : '按订单金额计佣';
    return '<b>有效在线</b>：App 打开且代理处于「已连接」才计时，每天最多计 10 小时（后台挂着进程不算）。<br>'
      + '<b>双轨升级</b>：在线时长与推广业绩取较高的一档，两条路都能升级。<br>'
      + '<b>返佣规则</b>：' + scope + '；' + base + '；' + period
      + '，满 ' + yuan(rules.withdrawMinCents) + ' 可提现'
      + (rules.payoutNote ? '；' + esc(rules.payoutNote) : '') + '。';
  }

  /* 试用行：未领取 / 试用中 / 已结束 三态 */
  function renderTrial(page) {
    var st = document.getElementById('trialSt');
    var ex = document.getElementById('trialExp');
    var op = document.getElementById('trialOp');
    if (!st || !op) return;
    var a = state.account;

    if (!state.loggedIn) {
      st.textContent = '登录后可领取';
      st.style.color = '#6b7280';
      if (ex) ex.textContent = '—';
      op.innerHTML = '<button class="btn sm" onclick="accLogin()">登录</button>';
      return;
    }
    if (!a) {
      if (state.accountErr) {
        st.innerHTML = failAction(isAuthErr(state.accountErr));
        st.style.color = isAuthErr(state.accountErr) ? '#dc2626' : '#9ca3af';
      } else {
        st.textContent = '加载中…';
        st.style.color = '#6b7280';
      }
      op.innerHTML = '';
      if (ex) ex.textContent = '—';
      return;
    }
    if (a.trialAvailable) {
      st.textContent = '未领取';
      st.style.color = '#6b7280';
      if (ex) ex.textContent = '—';
      op.innerHTML = '<button class="btn sm" onclick="accClaimTrial()">'
        + '<svg class="i sm"><use href="#i-gift"/></svg>免费领取 3 天</button>';
      return;
    }
    var ends = a.trialEndsAt ? new Date(a.trialEndsAt) : null;
    var active = ends && ends.getTime() > Date.now();
    if (a.trialEndsAt) {
      st.textContent = active ? '试用中' : '已结束';
      st.style.color = active ? '#d97706' : '#9ca3af';
      if (ex) ex.textContent = fmtDate(a.trialEndsAt);
    } else {
      st.textContent = '—';
      st.style.color = '#9ca3af';
      if (ex) ex.textContent = '—';
    }
    op.innerHTML = '<button class="btn sm ghost" onclick="accBuy()">续费 / 升级</button>';
  }

  /* 付费订阅行：我只追加一行 <tr>，不动原有表格结构 */
  function renderPlanRow(page) {
    var table = page.querySelector('.card table');
    if (!table) return;
    var row = document.getElementById('accPlanRow');
    var a = state.account;
    if (!state.loggedIn || !a || !a.planId) {
      if (row && row.parentNode) row.parentNode.removeChild(row);
      return;
    }
    var planName = planNameOf(a.planId);
    if (!row) {
      row = el('tr');
      row.id = 'accPlanRow';
      var body = table.tBodies[0] || table;
      body.appendChild(row);
    }
    var expires = a.expiresAt ? new Date(a.expiresAt) : null;
    var daysLeft = expires ? Math.ceil((expires.getTime() - Date.now()) / 86400000) : 0;
    row.innerHTML = '<td>' + planName + '</td>'
      + '<td style="color:' + (daysLeft > 0 ? '#0f766e' : '#dc2626') + '">'
      + (daysLeft > 0 ? '有效 · 剩 ' + daysLeft + ' 天' : '已到期') + '</td>'
      + '<td>' + fmtDate(a.expiresAt) + '</td>'
      + '<td style="text-align:right"><button class="btn sm" onclick="accBuy()">'
      + (daysLeft > 0 ? '续费' : '重新订阅') + '</button></td>';
  }

  function planNameOf(id) {
    var plans = state.plans || [];
    for (var i = 0; i < plans.length; i++) if (plans[i].id === id) return plans[i].name;
    return id;
  }

  /* 推广大使卡：链接、数字、余额 */
  function renderReferral(page) {
    var card = null;
    var cards = page.querySelectorAll('.card');
    for (var i = 0; i < cards.length; i++) {
      if (cards[i].querySelector('.promo-link')) { card = cards[i]; break; }
    }
    if (!card) return;
    var input = $('.promo-link input', card);
    var cells = card.querySelectorAll('table tr td');
    var bal = card.querySelector('b[style*="10b981"], b');

    if (!state.loggedIn || !state.referral) {
      if (input) {
        input.value = !state.loggedIn
          ? '登录后生成你的专属推广链接'
          : (state.referralErr
              ? (isAuthErr(state.referralErr) ? '会话已失效，请重新登录' : '推广信息读取失败，请重试')
              : '加载中…');
      }
      for (var k = 0; k < cells.length; k++) cells[k].textContent = '—';
      if (bal && bal.tagName === 'B') bal.textContent = '¥0';
      return;
    }
    var r = state.referral;
    if (input) input.value = 'https://dukou.dev/r/' + (r.code || '');
    /* 表格四列：本月点击 / 注册 / 成交 / 待结算收益。
       点击与注册数契约 E8 不下发（只在官网用户中心可见），如实显示 —，不编数字。 */
    if (cells.length >= 4) {
      cells[0].textContent = '—';
      cells[1].textContent = '—';
      cells[2].textContent = String(r.orders);
      cells[3].textContent = yuan(r.earnedCents);
    }
    if (bal && bal.tagName === 'B') {
      bal.textContent = yuan(r.balanceCents);
      /* 提现门槛随后台规则下发，替换 index.html 里的静态演示文案 */
      var note = bal.parentNode && bal.parentNode.querySelector('span');
      if (note && r.rules && r.rules.withdrawMinCents > 0) {
        note.textContent = '满 ' + yuan(r.rules.withdrawMinCents) + ' 可提';
      }
    }
  }

  /* 到期锁定提示条（锁定只影响省心功能，代理不受影响——契约 §4 口径） */
  function renderBanner(page) {
    var old = document.getElementById('accLockBanner');
    var a = state.account;
    if (!state.loggedIn || !a || !a.locked) {
      if (old && old.parentNode) old.parentNode.removeChild(old);
      return;
    }
    if (old) return;
    var b = el('div', 'acc-banner');
    b.id = 'accLockBanner';
    b.innerHTML = '<svg class="i sm"><use href="#i-lock"/></svg>'
      + '<span><b>订阅已到期</b>：代理连接不受影响，随时可用；'
      + '仪表盘、多服务器切换、备份迁移等省心功能需续订后解锁。</span>'
      + '<button class="btn sm" style="margin-left:auto" onclick="accBuy()">查看套餐</button>';
    var head = page.querySelector('.page-head');
    if (head && head.nextSibling) page.insertBefore(b, head.nextSibling);
    else page.appendChild(b);
  }

  /* ── 试用状态：侧边栏左下角 + 首页试用条 ─────────────────── */

  /* trial 三态归类：unclaimed（可领）/ active（试用中）/ ended（已结束）。
     loggedOut / loading 返回 null，由调用方决定展示。 */
  function trialPhase(a) {
    if (!a) return null;
    var ends = a.trialEndsAt ? new Date(a.trialEndsAt) : null;
    if (ends && ends.getTime() > Date.now()) return 'active';
    if (a.trialAvailable) return 'unclaimed';
    return 'ended';
  }

  function renderSideTrial() {
    var el = document.getElementById('sideTrial');
    if (!el) return;
    if (!state.loggedIn) { el.textContent = '未登录'; el.style.color = '#6b7280'; return; }
    var phase = trialPhase(state.account);
    if (!phase) {
      el.textContent = state.accountErr ? '读取失败' : '加载中…';
      el.style.color = '#9ca3af';
      return;
    }
    if (phase === 'active') { el.textContent = '试用中'; el.style.color = '#d97706'; return; }
    if (phase === 'unclaimed') { el.textContent = '试用未领取'; el.style.color = ''; return; }
    el.textContent = '试用已结束'; el.style.color = '#9ca3af';
  }

  function renderHomeTrial() {
    var bar = document.getElementById('trialBar');
    if (!bar) return;
    var span = bar.querySelector('span');
    var btn = bar.querySelector('button');
    if (!span || !btn) return;
    if (!state.loggedIn || !state.account) return; // 未登录/加载中保持「新用户」默认文案
    var phase = trialPhase(state.account);
    if (phase === 'active') {
      span.innerHTML = '试用进行中 · <b>' + fmtDate(state.account.trialEndsAt) + '</b> 到期，'
        + '到期后代理照常可用，不限速不中断';
      btn.textContent = '续费 / 升级';
      btn.setAttribute('onclick', 'accBuy()');
    } else if (phase === 'unclaimed') {
      span.innerHTML = '新用户可免费领 <b>3 天</b>全功能试用 · 到期后代理照常可用，不限速不中断';
      btn.textContent = '免费领取';
      btn.setAttribute('onclick', 'claimTrial()');
    } else {
      span.innerHTML = '3 天试用已于 <b>' + fmtDate(state.account.trialEndsAt) + '</b> 结束 · '
        + '代理照常可用，续订解锁桌面端增值功能';
      btn.textContent = '查看套餐';
      btn.setAttribute('onclick', 'accBuy()');
    }
  }

  /* ── 数据加载 ───────────────────────────────────────────── */

  function refresh() {
    if (!API.hasBackend()) return;
    state.accountErr = null;
    state.referralErr = null;
    API.sessionInfo().then(function (s) {
      state.loggedIn = !!(s && s.loggedIn);
      state.email = (s && s.email) || '';
      if (!state.loggedIn) {
        state.account = null;
        state.referral = null;
        renderMe();
        /* 登出后锁定态要按「24h 保留最后快照」重新评估（契约 §十六 16.2） */
        try { window.refreshLockState(); } catch (e) {}
        return;
      }
      renderMe(); // 先把邮箱等确定信息画出来，网络请求慢也不空白
      API.account().then(function (a) {
        state.account = a;
        renderMe();
        /* 拉到新快照即刷新锁定态——续订/领试用后无需重启客户端（契约 §十六 16.2） */
        try { window.refreshLockState(); } catch (e) {}
      }).catch(function (e) {
        /* 失败必须落到界面上（失败态 + 重试/重登入口），
           绝不能让「加载中…」永远挂在那（2026-11-17 真机踩坑） */
        state.account = null;
        state.accountErr = e;
        renderMe();
        toast('订阅状态读取失败：' + errText(e));
      });

      API.referral().then(function (r) {
        state.referral = r;
        renderMe();
      }).catch(function (e) {
        state.referral = null;
        state.referralErr = e;
        renderMe(); // 等级/推广链接也要如实显示失败，不静默
      });

      API.plans().then(function (p) { state.plans = p || []; }).catch(function () { });
    }).catch(function (e) {
      // 解密失败等异常：明确提示要重新登录，不静默当成未登录
      toast('登录态读取失败：' + errText(e));
      state.loggedIn = false;
      renderMe();
    });
  }

  function doLogout() {
    if (!window.confirm('退出登录后需要重新验证邮箱才能领取试用与查看订阅，确定退出？')) return;
    API.logout().then(function () {
      state.loggedIn = false; state.email = ''; state.account = null; state.referral = null;
      toast('已退出登录');
      try { window.dispatchEvent(new CustomEvent('acc:logout')); } catch (e) {}
      refresh();
    }).catch(function (e) { toast('退出失败：' + errText(e)); });
  }

  function wireGlobals() {
    /* 先抓住 index.html 内联的原始实现，演示态下要退回它 */
    var origClaimTrial = window.claimTrial;
    window.accClaimTrial = function () {
      if (!state.loggedIn) { openAuth(); return; }
      API.claimTrial().then(function (r) {
        state.account = (r && r.account) || state.account;
        if (r && r.status === 'already') {
          /* 已领过 ≠ 领取成功：明确告知结束时间，别让用户以为功能已解锁 */
          var ends = (r.account && r.account.trialEndsAt) || (state.account && state.account.trialEndsAt);
          toast('试用已领取过' + (ends ? '（已于 ' + fmtDate(ends) + ' 结束）' : '')
            + '；每个账号限领一次，续费/升级可解锁全部功能');
        } else {
          toast('领取成功：3 天全功能试用已开启');
        }
        refresh();
        /* 领试用 = 新快照，锁定态立即解除（验收标准 §四.3） */
        try { window.refreshLockState(); } catch (e) {}
      }).catch(function (e) { toast('领取失败：' + errText(e)); });
    };
    /* 接管 index.html 内联的 claimTrial()：同名全局函数覆盖后，
       原有 onclick="claimTrial()" 自然指向真实接口，无需改 HTML。
       （MOCK.account 已翻 false，演示分支清理；浏览器态本文件顶部已整体跳过，
       index.html 的 origClaimTrial 仅作 -dev 构建调试参考保留。） */
    window.claimTrial = window.accClaimTrial;
    void origClaimTrial;
    window.accLogin = openAuth;
    /* 失败态的「重试」入口：清掉错误标记重新拉取 */
    window.accRetry = function () { refresh(); };
    window.accBuy = function () {
      if (typeof window.ext === 'function') {
        window.ext('https://dukou.dev/pricing', '订阅购买',
          '套餐、优惠与发票都在官网完成，支付走微信 / 支付宝。<br>'
          + '客户端只读取订阅状态，<b>不接触你的支付凭证</b>；付款后回到客户端重新登录即可刷新到期时间。');
      }
    };
  }

  wireGlobals();

  /* ── 启动 ───────────────────────────────────────────────── */

  function boot() {
    injectStyle();
    refresh();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
