# 11 — Chạy server trên VPS Ubuntu

> Hướng dẫn vận hành cho một VPS. Kiến trúc ở [04-architecture.md](04-architecture.md);
> tài liệu này chỉ nói cách đưa nó lên chạy.

## Trước khi bắt đầu

**Cần có:**

| | |
|---|---|
| VPS | Ubuntu 22.04 hoặc 24.04, tối thiểu **2 vCPU / 2 GB RAM / 20 GB đĩa** |
| Reverse proxy | Đã có sẵn trên VPS — hướng dẫn này viết cho **Nginx Proxy Manager**. Nó lo TLS; server chỉ nghe trên loopback |
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
> `docker-compose.prod.yml` chỉ publish `sente` trên **`127.0.0.1`** — proxy trên cùng
> host thấy được, Internet thì không — còn `postgres` và `redis` không publish gì. Đổi
> `SENTE_BIND` thành `0.0.0.0`, hay thêm `ports:` cho service khác, là lộ thẳng ra Internet
> bất kể `ufw` nói gì.

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

Kiểm tra cổng `8080` trên host còn trống — nếu không, đặt `SENTE_PORT` khác:

```bash
sudo ss -ltnp | grep ':8080 ' || echo "8080 trống"
```

Dán vào `.env`:

```
SENTE_DOMAIN=sente.example.com
SENTE_JWT_SECRET=<64 ký tự hex>
POSTGRES_PASSWORD=<32 ký tự hex>
SENTE_VERSION=v0.1.0
# SENTE_PORT=8080          # đổi nếu 8080 đã có người dùng
```

`chmod 600 .env`. File này **không bao giờ** được commit.

### 4.1 Khóa Apple (push và Sign in with Apple)

Cần tài khoản Apple Developer Program. Ở *Certificates, Identifiers & Profiles*:

1. **Membership details** → chép **Team ID**.
2. **Identifiers → App IDs** → `app.sente.go` với Push Notifications, Sign in with Apple,
   Associated Domains. Trong Sign in with Apple → *Server-to-Server Notification Endpoint*:
   `https://<SENTE_DOMAIN>/v1/auth/apple/notifications`.
3. **Keys** → một khóa **Apple Push Notifications service**, một khóa **Sign in with Apple**.
   Mỗi khóa tải được **một lần** duy nhất: `AuthKey_<KEY_ID>.p8`.

Đưa hai file `.p8` vào thư mục bí mật, giữ nguyên tên:

```bash
mkdir -p ~/sente/deploy/secrets && chmod 700 ~/sente/deploy/secrets
# scp AuthKey_*.p8 vào đó, rồi:
chmod 644 ~/sente/deploy/secrets/*.p8    # container chạy non-root nên cần đọc được
```

Thêm vào `.env`:

```
SENTE_APPLE_TEAM_ID=<Team ID>
SENTE_APNS_KEY_ID=<Key ID của khóa APNs>
SENTE_SIWA_KEY_ID=<Key ID của khóa Sign in with Apple>
```

Server tự tìm `/run/secrets/AuthKey_<KEY_ID>.p8`; thư mục được mount chỉ-đọc và nằm trong
`.gitignore` (`deploy/secrets/`, `*.p8`). Bỏ trống `SENTE_APNS_KEY_ID` là tắt push; bỏ trống
`SENTE_APPLE_TEAM_ID` là tắt Sign in with Apple và AASA. Sau khi lên, `GET /v1/config` phải có
`"apple_sign_in": true, "push": true`.

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
| Forward Hostname | `127.0.0.1` nếu NPM chạy `network_mode: host`; nếu NPM ở network Docker riêng thì dùng IP gateway của host trong Docker (`172.17.0.1` với bridge mặc định) |
| Forward Port | `8080` (hoặc `SENTE_PORT` bạn đã đặt) |
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

### 6.1 Nếu domain đi qua Cloudflare

Bật "orange cloud" là chèn thêm một proxy trước NPM. Hai hệ quả:

**Rate limit tính sai.** NPM nối IP upstream vào `X-Forwarded-For`, nên phần tử ngoài cùng
bên phải — cái server tin — là IP của **Cloudflare**, và mọi người dùng chia chung một bucket
đăng ký 10/phút. Sửa bằng cách bảo server đọc header Cloudflare đặt riêng:

```
# deploy/.env
SENTE_CLIENT_IP_HEADER=CF-Connecting-IP
```

**Header đó giả được nếu ai đó gọi thẳng vào origin**, bỏ qua Cloudflare. Chặn bằng cách chỉ
cho 80/443 nhận từ dải IP của Cloudflare:

```bash
for ip in $(curl -s https://www.cloudflare.com/ips-v4) $(curl -s https://www.cloudflare.com/ips-v6); do
  sudo ufw allow from "$ip" to any port 80,443 proto tcp
done
sudo ufw delete allow 80/tcp && sudo ufw delete allow 443/tcp
```

Dải IP này Cloudflare thỉnh thoảng đổi — cân nhắc đặt lệnh trên vào cron hằng tuần.

WebSocket đi qua Cloudflare bình thường, kể cả gói miễn phí. Kiểm nhanh — phải thấy `101`:

```bash
curl -si --http1.1 -H "Connection: Upgrade" -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: $(openssl rand -base64 16)" \
  "https://sente.example.com/v1/ws?pv=1&game_id=<id>&token=<token>" | head -1
```

Lưu ý `--http1.1`: curl mặc định đàm phán HTTP/2 với Cloudflare, và bắt tay WebSocket không
đi qua HTTP/2 — không có cờ đó sẽ thấy `426`, trông như lỗi nhưng không phải.

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
| `SENTE_CLIENT_IP_HEADER` | | rỗng | Header chứa IP thật khi có CDN trước proxy, ví dụ `CF-Connecting-IP`. Xem [§6.1](#61-nếu-domain-đi-qua-cloudflare) |
| `SENTE_MIGRATE` | | `true` | Đặt `false` nếu chạy migration riêng |
| `SENTE_APPLE_TEAM_ID` | | rỗng | Bật Sign in with Apple và file AASA cho universal link. Lấy từ Apple Developer → Membership |
| `SENTE_APPLE_BUNDLE_ID` | | `app.sente.go` | `aud` của identity token và `apns-topic` |
| `SENTE_APNS_KEY_ID` | | rỗng | Bật push. Cần `SENTE_APPLE_TEAM_ID` |
| `SENTE_SIWA_KEY_ID` | | rỗng | Khóa Sign in with Apple; chỉ kiểm tra đọc được lúc khởi động |
| `SENTE_SECRETS_DIR` | | `/run/secrets` | Nơi tìm `AuthKey_<KEY_ID>.p8` |
| `SENTE_APNS_KEY_FILE`, `SENTE_SIWA_KEY_FILE` | | theo Key ID | Chỉ khi file không mang tên Apple đặt |
| `SENTE_APP_STORE_URL` | | rỗng | Nút "Tải trên App Store" ở landing page `/j/<code>` |

**`/metrics`** (Prometheus) không có xác thực — nó dành cho mạng nội bộ. Trong NPM, thêm một
Custom Location `/metrics` trả `403`, hoặc chỉ scrape từ trong VPS (`127.0.0.1:8080/metrics`).

Hai biến sau **không** phải của server mà của `docker-compose.prod.yml`, đọc từ `deploy/.env`:

| Biến | Mặc định | Ghi chú |
|---|---|---|
| `SENTE_BIND` | `127.0.0.1` | Địa chỉ host để publish. **Không bao giờ** đặt `0.0.0.0` |
| `SENTE_PORT` | `8080` | Cổng host cho proxy trỏ vào |
| `SENTE_SECRETS_DIR` | `./secrets` | Thư mục host chứa `.p8`, mount vào `/run/secrets` |

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
| Push "sắp hết giờ" | Chỉ có "đến lượt bạn" (ván thư tín), "ván kết thúc", "bạn nhận lời mời" |
| Tắt từng loại thông báo | Cột `push_prefs` có, endpoint `PATCH /v1/devices` chưa |
| Tracing | Có `/metrics` Prometheus (bốn series của ADR-015); chưa có trace |

**Đã có:** Sign in with Apple (liên kết tài khoản khách, webhook thu hồi), push qua APNs
với token key ([§4.1](#41-khóa-apple-push-và-sign-in-with-apple)),
lời mời qua link với landing page cho người chưa cài app, refresh token xoay vòng,
xóa tài khoản trong app, báo cáo/chặn (App Store 1.2 và 5.1.1(v)), xuất SGF, ván thư tín được
xử hết giờ bởi sweeper, rate limit theo IP cho đăng ký / theo người dùng cho mọi thứ khác
([06 §1.3](06-api-and-realtime-protocol.md#13-rate-limit)).
Ghế trống trong ván tạo trực tiếp qua `POST /v1/games` vẫn là "ai vào trước lấy" — đó là
đường thử nghiệm; ván thật đi qua lời mời.

Nói cách khác: **đủ để mở cho một nhóm nhỏ quen biết.** Để công khai còn thiếu giám sát
có cảnh báo — hiện chưa biết được khi nào nó hỏng.
