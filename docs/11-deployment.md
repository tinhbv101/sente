# 11 — Chạy server trên VPS Ubuntu

> Hướng dẫn vận hành cho một VPS. Kiến trúc ở [04-architecture.md](04-architecture.md);
> tài liệu này chỉ nói cách đưa nó lên chạy.

## Trước khi bắt đầu

**Cần có:**

| | |
|---|---|
| VPS | Ubuntu 22.04 hoặc 24.04, tối thiểu **2 vCPU / 2 GB RAM / 20 GB đĩa** |
| Reverse proxy | Đã có sẵn trên VPS và chạy trong Docker — hướng dẫn này viết cho **Nginx Proxy Manager**. Nó lo TLS; server không mở cổng nào ra ngoài |
| Tên miền | Bản ghi A trỏ về VPS, chứng chỉ xin qua proxy |

**Vì sao 2 GB RAM:** Postgres ~256 MB, Redis ~64 MB, server ~200 MB lúc rỗi. 1 GB chạy được
nhưng không còn chỗ cho `docker build`, nên hãy build ở máy khác nếu chỉ có 1 GB.

## 1. Cài Docker

```bash
ssh root@<ip-vps>

apt-get update && apt-get install -y ca-certificates curl git
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
  https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

docker --version && docker compose version
```

## 2. Tạo người dùng riêng cho dịch vụ

Đừng chạy dịch vụ bằng `root`.

```bash
adduser --disabled-password --gecos "" sente
usermod -aG docker sente
```

## 3. Tường lửa

```bash
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
ufw status
```

> **Cảnh báo:** Docker tự thêm luật vào `iptables` và **đi vòng qua `ufw`**. Vì vậy
> `docker-compose.prod.yml` cố ý **không publish cổng nào** — proxy tới `sente` qua
> network Docker chung, còn `postgres` và `redis` chỉ `sente` thấy được. Nếu bạn thêm
> `ports:` cho bất kỳ service nào, nó sẽ lộ ra Internet bất kể `ufw` nói gì.

## 4. Lấy mã nguồn và cấu hình

```bash
su - sente
git clone <repo-url> sente && cd sente/deploy
cp .env.example .env
```

Sinh bí mật — **đừng tự nghĩ ra**:

```bash
python3 - <<'PY'
import secrets
print("SENTE_JWT_SECRET=" + secrets.token_hex(32))
print("POSTGRES_PASSWORD=" + secrets.token_hex(16))
PY
```

Tìm network Docker mà Nginx Proxy Manager đang chạy trên đó — `sente` phải nằm cùng network
thì NPM mới gọi tới được theo tên:

```bash
docker inspect <tên-container-npm> --format '{{range $k,$_ := .NetworkSettings.Networks}}{{$k}} {{end}}'
```

Dán vào `.env`:

```
SENTE_DOMAIN=sente.example.com
PROXY_NETWORK=<network vừa tìm được>
SENTE_JWT_SECRET=<64 ký tự hex>
POSTGRES_PASSWORD=<32 ký tự hex>
SENTE_VERSION=v0.1.0
```

`chmod 600 .env`. File này **không bao giờ** được commit.

## 5. Khởi động

```bash
docker compose -f docker-compose.prod.yml up -d --build
docker compose -f docker-compose.prod.yml ps
```

Lần đầu mất 2–4 phút để build. Kết quả mong đợi: cả ba service `healthy`.

Migration chạy tự động lúc khởi động (`SENTE_MIGRATE=true`), nên không cần bước riêng.

### Trỏ Nginx Proxy Manager vào server

Trong NPM → **Proxy Hosts → Add**:

| Trường | Giá trị |
|---|---|
| Domain Names | `sente.example.com` |
| Scheme | `http` |
| Forward Hostname | `sente` |
| Forward Port | `8080` |
| **Websockets Support** | **Bật** — thiếu cái này kết nối ván không bao giờ nâng cấp được |
| Block Common Exploits | Bật |
| Tab SSL | Request a new certificate · Force SSL · HTTP/2 |

Tab **Advanced**, thêm:

```nginx
# Heartbeat mỗi 20 giây giữ kết nối sống; timeout mặc định 60 giây của nginx
# quá sát — một lần mạng di động chập chờn là đứt ván.
proxy_read_timeout 300s;
proxy_send_timeout 300s;
```

NPM tự đặt `X-Forwarded-For`, nên `SENTE_TRUST_PROXY=true` trong compose là đúng — rate limit
đăng ký tính theo IP thật của người dùng, không phải IP của proxy.

## 6. Kiểm tra

```bash
cd ~/sente && ./scripts/smoke.sh https://sente.example.com
```

Script này không chỉ ping — nó đăng ký tài khoản khách, tạo một ván, tạo và nhận lời mời qua
link, và kiểm tra endpoint có chặn khi thiếu token. Nếu nó xanh thì proxy, database, Redis,
lease, actor và engine luật đều đang hoạt động.

Nếu `readyz` qua mà bước WebSocket sau đó hỏng ở app, gần như luôn là quên bật **Websockets
Support** trong NPM. Kiểm tra nhanh từ máy ngoài:

```bash
curl -si https://sente.example.com/v1/ws | head -1     # 401 là đúng: cần token, nhưng đã tới server
```

## 7. Vận hành hằng ngày

```bash
cd ~/sente/deploy

docker compose -f docker-compose.prod.yml logs -f sente      # xem log
docker compose -f docker-compose.prod.yml ps                  # trạng thái
docker compose -f docker-compose.prod.yml restart sente       # khởi động lại
```

Log ở dạng JSON một dòng một bản ghi, có `node_id` và `game_id`, nên `jq` lọc được ngay:

```bash
docker compose -f docker-compose.prod.yml logs sente | jq -r 'select(.level=="ERROR")'
```

### Cập nhật phiên bản

```bash
cd ~/sente && git pull
cd deploy && docker compose -f docker-compose.prod.yml up -d --build sente
```

Container cũ nhận `SIGTERM` và **bàn giao ván đang chơi** trước khi thoát
([04 §5.2](04-architecture.md#52-deploy-realtime-service-không-làm-đứt-ván)). `stop_grace_period`
đặt 45 giây để nó kịp làm việc đó. Người chơi thấy banner "Đang kết nối lại…" chớp qua.

### Sao lưu

Ván cờ là dữ liệu duy nhất không tái tạo được. Redis mất sạch cũng không sao.

```bash
# vào crontab của user sente: 0 3 * * *
docker compose -f ~/sente/deploy/docker-compose.prod.yml exec -T postgres \
  pg_dump -U sente sente | gzip > ~/backups/sente-$(date +\%F).sql.gz
find ~/backups -name 'sente-*.sql.gz' -mtime +14 -delete
```

**Chưa diễn tập khôi phục thì chưa gọi là có backup.** Thử một lần:

```bash
gunzip -c ~/backups/sente-2026-08-28.sql.gz | \
  docker compose -f docker-compose.prod.yml exec -T postgres psql -U sente -d sente_restore_test
```

## 8. Không dùng Docker (systemd)

Nếu muốn chạy binary trực tiếp:

```bash
# trên máy dev, build cho Linux
cd sente-server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o sente-server ./cmd/server
scp sente-server sente@<ip>:/usr/local/bin/
```

```ini
# /etc/systemd/system/sente.service
[Unit]
Description=Sente
After=network-online.target postgresql.service redis-server.service

[Service]
User=sente
ExecStart=/usr/local/bin/sente-server
EnvironmentFile=/etc/sente/env
Restart=always
RestartSec=5
# Cho phép drain hoàn tất trước khi bị giết
TimeoutStopSec=45
KillSignal=SIGTERM

# Dịch vụ không cần gì ngoài mạng và socket của nó
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=multi-user.target
```

Vẫn cần reverse proxy phía trước cho TLS, và WebSocket phải được proxy đúng cách — với nginx
thuần nghĩa là `proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade";`
và `proxy_read_timeout` đủ dài. NPM làm việc này bằng nút **Websockets Support**.

## 9. Biến môi trường

| Biến | Bắt buộc | Mặc định | Ghi chú |
|---|:---:|---|---|
| `SENTE_DATABASE_URL` | ✔ | — | `postgres://user:pass@host:5432/db?sslmode=...` |
| `SENTE_JWT_SECRET` | ✔ | — | Tối thiểu 32 byte. Server **từ chối khởi động** nếu ngắn hơn |
| `SENTE_REDIS_URL` | | `redis://localhost:6379` | |
| `SENTE_ADDR` | | `:8080` | |
| `SENTE_NODE_ID` | | hostname | Phải khác nhau giữa các node |
| `SENTE_ALLOWED_ORIGINS` | | rỗng | Origin cho WebSocket; rỗng = chỉ same-origin |
| `SENTE_PUBLIC_URL` | | rỗng | Gốc để dựng link mời `…/j/<code>`; rỗng thì không trả `share_url` |
| `SENTE_TRUST_PROXY` | | `false` | Tin `X-Forwarded-For` để tính rate limit theo IP thật. **Chỉ** bật khi đứng sau proxy — bật sai là ai cũng giả được IP |
| `SENTE_MIGRATE` | | `true` | Đặt `false` nếu chạy migration riêng |

Thiếu biến bắt buộc thì server **thoát ngay lúc khởi động** kèm thông báo rõ, thay vì chết ở
request đầu tiên cần đến nó.

## 10. Khi cần nhiều hơn một node

Kiến trúc đã sẵn sàng: node nào cũng giống node nào, ai sở hữu ván nào do lease trong Redis
quyết định lúc chạy ([ADR-005](03-solution-design.md#adr-005--sở-hữu-ván-single-owner-actor--định-tuyến-qua-redis)).
Thêm node chỉ là chạy thêm container:

```yaml
sente-2:
  <<: *sente-base
  environment:
    SENTE_NODE_ID: node-2
```

Rồi thêm upstream thứ hai vào proxy host trong NPM (tab Advanced, khối `upstream`) hoặc
dùng một load balancer riêng. **Không** cần sticky session — hai người chơi
rơi vào hai node khác nhau vẫn chơi chung một ván, lệnh được chuyển tiếp qua Redis
([04 §4.1](04-architecture.md#41-đi-một-nước-ván-live-hai-người-ở-hai-node)).

Điều kiện: mọi node phải dùng **chung một Redis và chung một PostgreSQL**, và `SENTE_NODE_ID`
phải khác nhau. Trùng node id sẽ khiến hai tiến trình tranh cùng một lease.

## 11. Những gì server này **chưa** có

Nói rõ để không ai tưởng đã xong:

| Thiếu | Hệ quả |
|---|---|
| Sign in with Apple, refresh token | Access token hết hạn sau 15 phút là phải đăng ký khách lại |
| Push (APNs) | Ván thư tín không báo được cho ai |
| Ván thư tín qua REST | Chỉ chơi realtime khi cả hai đang mở kết nối |
| Metric / tracing | Chỉ có log; `/readyz` là thứ duy nhất để giám sát |
| Landing page cho `/j/<code>` | Link mời hiện chỉ là API; người chưa cài app mở ra thấy JSON |

**Đã có:** lời mời qua link (`/v1/challenges`) với ghế được gán lúc chấp nhận, và rate limit
theo IP cho đăng ký / theo người dùng cho mọi thứ khác ([06 §1.3](06-api-and-realtime-protocol.md#13-rate-limit)).
Ghế trống trong ván tạo trực tiếp qua `POST /v1/games` vẫn là "ai vào trước lấy" — đó là
đường thử nghiệm; ván thật đi qua lời mời.

Nói cách khác: **đủ để mở cho một nhóm nhỏ quen biết. Chưa đủ để công khai** — thiếu
đăng nhập bền và thiếu giám sát, nghĩa là chưa biết được khi nào nó hỏng.
