# 排障手册

这份文档里的每一条，都是实际踩过并定位到根因的。
按**症状**查，不用从头读。

---

## 先跑这三条，八成问题能自己定位

```bash
# 1) 本机代理服务活着吗？
systemctl is-active dukou-client

# 2) 端口上是谁在应答？
ss -lntp 'sport = :7890'

# 3) 走代理请求一次，看返回码
curl -sS -x http://127.0.0.1:7890 -o /dev/null -w "%{http_code}\n" https://api.github.com
```

三条的结果组合起来，基本就能落到下面某一节：

| `is-active` | `7890` 占用者 | curl 返回 | 去读 |
|---|---|---|---|
| `active` | 自己的进程 | `200` | 没坏，可能是浏览器没配 第 7 节 |
| `active` | 自己的进程 | `407` | 第 1 节 |
| `active` | 别人的进程 | 任意 | 第 2 节 |
| `failed` | 无 | 连不上 | 第 5 节 |
| `active` | 自己的进程 | `000` | 第 3 节 |

---

## 1. 407 / 503：密码两端不一致

**症状**

```
CONNECT tunnel failed, response 503
```

或者 curl 返回 `407`。

**这两个码分别是谁发的**

| 码 | 谁发的 | 含义 |
|---|---|---|
| `407` | 服务器（gost 本身） | 认证失败，密码不对 |
| `503` | 本机（gost 客户端） | 本机收到了请求，但连不上游——上游返回了 407 |

所以 503 的根因通常还是 407，只是被本机转述了一遍。

**判断**：两端各算一次长度和指纹，比对数字即可，不用把明文打出来。

```bash
# 在客户端机器上
PW=$(sudo sed -nE 's#.*dukou:([^@]+)@.*#\1#p' /etc/systemd/system/dukou-client.service | head -1)
echo "客户端  长度=${#PW}  指纹=$(printf '%s' "$PW" | md5sum | cut -c1-8)"

# 在服务器上
PW=$(sed -nE 's#.*dukou:([^@]+)@:8443.*#\1#p' /etc/systemd/system/dukou.service)
echo "服务端  长度=${#PW}  指纹=$(printf '%s' "$PW" | md5sum | cut -c1-8)"
```

两个数字都一致，说明密码没问题，往下查第 3 节；不一致就是这里的问题。

**处理**：不要手抄密码。让客户端从服务器现取：

```bash
PW=$(ssh root@你的服务器IP "sed -nE 's#.*dukou:([^@]+)@:8443.*#\1#p' /etc/systemd/system/dukou.service")
```

**为什么这个坑特别容易踩**：密码是 15~16 位的随机串，`S/5`、`l/1`、`O/0`、`B/8`
在终端字体里几乎一样。从截图里认、从聊天记录里复制，都会错。
只要错一个字符，表现就是「服务全绿、请求全挂」。

---

## 2. 出口 IP 不是你的服务器

**症状**：部署报「全部完成」，但

```bash
curl -sS -x http://127.0.0.1:7890 https://api.ipify.org
```

返回的是**另一台机器**的 IP，或者一个你不认识的内网出口。

**原因**：端口上坐着的不是 dukou 装的客户端。

```
7890 被旧代理占用
   ↓
dukou-client 启动 → bind: address already in use → 立刻退出
   ↓
systemctl restart 返回 0（它只代表 systemd 收下了请求）
   ↓
自检连 7890，应答的是旧代理 → 拿到旧服务器的 IP → 一路绿灯
```

这是最难自查的一类故障：**每一步都"成功"，结果是错的**。
判断方法只有一个——看端口归谁：

```bash
sudo ss -lntp 'sport = :7890'
sudo cat /proc/<PID>/cgroup | head -1     # 看它属于哪个 systemd 单元
```

**处理**

```bash
sudo systemctl disable --now gost-client    # 换成实际占用者的单元名
sudo systemctl daemon-reload
# 然后重跑客户端安装
```

新版 dukou 会在安装前检查端口占用，并在装完后复查「端口是否由自己的单元监听」，
遇到这类情况会直接拦下并告诉你要停哪个服务。

---

## 3. 外部连不上 8443：安全组还是系统防火墙？

这两个是完全不同的东西，报错却都是「连不上」。

| | 在哪配置 | 工具能不能改 |
|---|---|---|
| **系统防火墙**（firewalld / ufw / iptables） | 服务器内部 | 能，dukou 会自动放行 |
| **云安全组** | 云服务商控制台 | 不能，只能你手动点 |

**判断**

```bash
# 在服务器上，看端口有没有在监听（这是"服务端没问题"的证据）
ss -lnt | grep 8443

# 在你本机，看能不能连上（这才是"安全组"的证据）
timeout 8 bash -c 'cat < /dev/null > /dev/tcp/你的服务器IP/8443' && echo OPEN || echo BLOCKED
```

- 服务器上 `LISTEN` + 本机 `BLOCKED` → **安全组没放行**，去控制台加 TCP:8443
- 服务器上也没有 `LISTEN` → 服务没起来，跳 第 5 节

---

## 4. 能连上但很慢

**先分清是"带宽不够"还是"丢包把 TCP 按住了"**——处理方式完全不同。

```bash
# 单连接
curl -sS -x http://127.0.0.1:7890 -o /dev/null \
  -w "单连接 %{speed_download} B/s\n" \
  "https://speed.cloudflare.com/__down?bytes=10000000"
```

然后并发三路，各下一份：

```bash
for i in 1 2 3; do
  curl -sS -x http://127.0.0.1:7890 -o /dev/null \
    -w "  #$i %{speed_download} B/s\n" \
    "https://speed.cloudflare.com/__down?bytes=5000000" &
done; wait
```

| 结果 | 结论 |
|---|---|
| 三路合计 ≈ 单连接 | **带宽到顶了**，这是你这条线路的上限，软件无法突破 |
| 三路合计 ≈ 单连接 × 3 | 单连接被窗口/丢包限制，还能优化 → 看 BBR |

**确认 BBR 是否生效**

```bash
sysctl net.ipv4.tcp_congestion_control    # 应为 bbr
lsmod | grep bbr                          # 模块是否加载
```

在丢包明显的跨国链路上，拥塞算法从 `cubic` 换成 `bbr`，实测能带来一个数量级的差别——
因为 `cubic` 把丢包一律当拥塞，一丢就砍半窗口；`bbr` 不会。

**还有一种情况**：服务器本身没问题，但你本地的宽带到海外就是窄。
换一台机器（比如云电脑 vs 家用电脑）再测一次，结果可能完全不同。

---

## 5. 服务起不来：查日志，别猜

```bash
sudo systemctl status dukou-client --no-pager
sudo journalctl -u dukou-client -n 30 --no-pager
```

日志里的关键词对应关系：

| 日志里出现 | 含义 | 处理 |
|---|---|---|
| `address already in use` | 端口被占 | 第 2 节 |
| `permission denied` | 不是 root | 用 `sudo systemctl restart` |
| `i/o timeout` / `connection refused` | 连不上下游 | 安全组或服务器挂了，查 第 3 节 |
| `first record does not look like a TLS handshake` | 客户端把 `-F` 写成了 `http+tls://` | 改成 `-F tls://` |
| 什么都不输出就退出 | 命令参数错，gost 启动即退 | 手动跑 `ExecStart` 那一行看报错 |

手动复现（最快）：

```bash
sudo systemctl cat dukou-client | grep ExecStart
# 把这行复制出来，前面加 sudo，去掉 ExecStart= 直接执行
```

---

## 6. 改了配置却没效果

**症状**：明明改了密码/端口，重启了服务，行为还是旧的。

**原因**：`systemctl enable --now` 在服务**已经在运行时不会重启**。
它只负责「设为开机自启」+「如果没在跑就启动」，配置改了它不管。

```bash
# 错误：服务在跑时，这一行什么都不会发生
sudo systemctl enable --now dukou-client

# 正确：显式重启
sudo systemctl daemon-reload && sudo systemctl restart dukou-client
```

改完一定回读确认，不要靠「应该生效了」：

```bash
systemctl show dukou-client -p ExecMainStartTimestamp   # 启动时间是否刚更新
sudo grep -o 'myuser:[^@]*@' /etc/systemd/system/dukou-client.service | sed 's/:.*/***/'
```

---

## 7. 浏览器设了代理却不生效

| 现象 | 原因 | 处理 |
|---|---|---|
| 代理软件显示在跑，浏览器还是走直连 | 浏览器旁边还挂着代理插件，插件规则优先于系统代理 | 禁用插件（SwitchyOmega / Zero Omega 等），只留系统代理 |
| 设了「使用系统代理」但网站打不开 | 系统代理本身没设成功 | `gsettings get org.gnome.system.proxy mode` 应为 `manual` |
| PAC 文件明明存在，浏览器读不到 | Firefox 是 snap 装的，沙盒读不到本地文件 | 把 PAC 用 `python3 -m http.server` 托管成 `http://127.0.0.1:端口/proxy.pac` |
| 改了代理，某些工具仍走旧路 | 命令行工具读环境变量，不读系统代理 | `env | grep -i proxy`，必要时 `source ~/.bashrc` |

**一句话原则**：浏览器只认一个来源。要么系统代理，要么插件，别两个一起用。

---

## 8. 明文连接被中间设备重置

**症状**：GitHub 能开，Google / ChatGPT 打不开；或者所有站点都卡在 `CONNECT` 阶段被 RST。

**诊断**：换一个加密端口对比。

```bash
# 明文 HTTP 代理端口
curl -sS -x http://你的服务器:1080 -o /dev/null -w "plain: %{http_code}\n" https://www.google.com

# TLS 加密端口
curl -sS -x https://你的服务器:8443 --proxy-insecure -o /dev/null -w "tls: %{http_code}\n" https://www.google.com
```

如果明文失败、TLS 成功，原因就确定了：**`CONNECT 域名:443` 这串明文被链路上的设备识别并重置了**。
这类设备看不懂加密隧道里的内容，所以 `https://` 代理端口能过。

dukou 默认只提供加密端口（8443），不生成明文监听，就是为了避免这一点。

---

## 9. 粘一段脚本，终端直接闪退

**症状**：粘贴一段多行 shell 后，终端窗口自己关了。

**原因**：那段脚本里有 `read` 和 `exit`。

```
粘贴多行时，read 会把紧跟的下一行当成输入吃掉
   ↓
变量拿到的是 "set -e" 这种字符串，判断失败
   ↓
走到 exit 1 —— 退出的是你的登录 shell，窗口就关了
```

**处理**：交互式粘贴的脚本里不要用 `read`；需要 `exit` 时用 `return`，
或者把脚本写成单行、用一个文件执行 `bash script.sh`。

---

## 10. 改配置把自己锁在门外

**症状**：通过面板或 IP 访问服务器管理界面，改完代理后连不上了。

**原因**：管理界面走的是 IP，而 PAC 规则是「非国内域名全部走代理」——
IP 不在国内域名列表里，于是管理连接也进了隧道。代理一改坏，面板立刻失联。

**处理**：先本地直连绕过。

```bash
# 关掉浏览器代理，或用云服务商的网页版远程连接（Workbench / VNC，不经过你的代理）
```

**预防**：在 PAC 或忽略列表里把管理用的 IP 放行：

```javascript
if (host === "你的服务器IP") return "DIRECT";
```

系统代理的 `ignore-hosts` 里也应该有 `127.0.0.0/8`、`*.aliyun.com`、`*.aliyuncs.com`——
最后一条尤其重要，少了它，`apt` 走内网镜像会莫名返回 503。

---

## 附：三条自查命令，建议存下来

```bash
# 一、链路是否通 + 出口是谁
curl -sS -x http://127.0.0.1:7890 https://api.ipify.org; echo

# 二、端口归谁（排查假成功）
sudo ss -lntp 'sport = :7890'

# 三、服务最近说了什么
sudo journalctl -u dukou-client -n 30 --no-pager
```

绝大多数问题，这三条跑完就有方向了。
