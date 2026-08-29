# Sente

Cờ vây online trên iOS. Thiết kế đầy đủ ở [`docs/`](docs/README.md).

## Trạng thái

| Phần | Trạng thái |
|---|---|
| Tài liệu thiết kế (11 tài liệu) | Xong |
| `rules-spec/` — hằng số Zobrist, 43 conformance vector, parity lock | Xong |
| `sente-ios/Packages/GoKit` — engine luật Swift | **Xong · 57/57 xanh · coverage 98,9% dòng / 93,4% nhánh** |
| `sente-server/internal/rules` — engine luật Go | **Xong · 40/40 xanh** |
| `sente-server/internal/game` — đồng hồ, đàm phán quân chết, máy trạng thái ván | **Xong · 68/68 xanh** |
| `sente-server/internal/store` — PostgreSQL, migration, append-only moves | **Xong · 25/25 xanh (testcontainers)** |
| `sente-server/internal/cluster` — lease Redis, heartbeat, reaper | **Xong · 14/14 xanh (Redis thật)** |
| `sente-server/internal/game` — Actor: goroutine sở hữu ván, timer đồng hồ | **Xong · 15/15 xanh (fake clock, `-race`)** |
| `sente-server/internal/node` — Registry: lease + store + actor, **chaos test takeover** | **Xong · 19/19 xanh** |
| `sente-server/internal/wire` — codec command/event xuyên tiến trình | **Xong · 7/7 xanh** |
| `sente-server/internal/hub` — định tuyến xuyên node, fanout sự kiện | **Xong · 14/14 xanh** |
| `sente-server/internal/auth` — tài khoản khách + JWT | **Xong · 6/6 xanh** (chưa có Apple, chưa có refresh) |
| `sente-server/internal/httpapi` — REST + WebSocket gateway + **lời mời qua link** | **Xong · 25/25 xanh (đầu-cuối)** |
| `sente-server/internal/ratelimit` — token bucket trong Redis | **Xong · 8/8 xanh** |
| `cmd/server` + Docker, chạy sau reverse proxy có sẵn — **deploy được** | **Xong** — xem [docs/11](docs/11-deployment.md) |
| CI (`make ci`) — drift, parity, test, coverage gate | **Xong** |
| `sente-ios/Packages/SenteNet` — REST + WebSocket client, reconnect, outbox | **Xong · 12/12 xanh** |
| `sente-ios/Packages/SenteUI` — bàn cờ Canvas, cử chỉ đặt quân, đồng hồ | **Xong · 6/6 xanh** |
| `sente-ios/App` — Home, mời/nhận lời mời, màn hình ván, đếm điểm, cài đặt (sáng/tối/hệ thống), icon | **Chạy được trên simulator** · `GameStore` **23/23 test** (optimistic/rollback, thứ tự sự kiện, canary hash) |
| Còn lại (Sign in with Apple, push, landing page, XCUITest, TestFlight) | Chưa bắt đầu |

Hai engine luật độc lập, cùng chạy một bộ vector và một file parity — ràng buộc quan trọng
nhất của toàn hệ thống ([ADR-002](docs/03-solution-design.md#adr-002--nơi-đặt-engine-luật-cờ)).

## Bố cục

```
docs/           thiết kế: yêu cầu, luật cờ, kiến trúc, API, roadmap
rules-spec/     hợp đồng dùng chung giữa hai engine luật
  zobrist_table.json    hằng số hash — đổi là breaking change
  vectors/              conformance vector, viết tay từ đặc tả
  parity/
    positions.json      36 thế cờ + hash, chống hồi quy
    games.json          24 ván ghi từng nước, differential giữa hai engine
  tools/                script sinh bảng Zobrist và vector
sente-ios/      app iOS — project.yml (xcodegen) sinh ra Sente.xcodeproj
  Packages/GoKit        engine luật, Swift thuần, không dependency
  Packages/SenteNet     REST + WebSocket, Keychain, backoff
  Packages/SenteUI      BoardView (Canvas), tokens, đồng hồ, banner
  App/                  màn hình; GameStore giữ cặp confirmed/optimistic
  scripts/render-icon.swift   vẽ icon bằng CoreGraphics, hai biến thể sáng/tối
sente-server/   backend Go
  internal/rules        engine luật, gói thuần, không I/O
  internal/game         đồng hồ (4 thể thức), đàm phán quân chết,
                        GameSession — máy trạng thái thuần của một ván
  internal/store        PostgreSQL: schema, migration, kho ván
  internal/cluster      lease Redis: ai sở hữu ván nào
  internal/node         registry: nhận ván, dựng lại từ DB, nhả lease
  internal/wire         codec command/event giữa hai node
  internal/hub          định tuyến: chạy tại chỗ hay chuyển tiếp
  internal/auth         tài khoản khách + JWT
  internal/httpapi      REST + WebSocket gateway + lời mời
  internal/ratelimit    token bucket dùng chung giữa các node
  cmd/server            binary chạy một node
deploy/         compose dev (Postgres + Redis) và compose production sau proxy
scripts/        cổng chất lượng chạy được cả local lẫn CI
```

## Chạy

```bash
make ci           # đúng những gì một pull request phải qua (cần Docker)
make app          # build app iOS trên simulator + chạy test GameStore (cần xcodegen)
make test-fast    # như trên, bỏ phần cần Docker
make db-up        # dựng Postgres + Redis để chạy tay
make run          # chạy server ở local
make image        # build image production
make smoke URL=https://sente.example.com   # kiểm tra một bản đã deploy
make test         # cả hai engine chạy cùng bộ vector
make test-ios     # engine Swift chạy conformance vector
make test-server  # engine Go chạy đúng bộ vector đó
make perf         # đo lại ở bản release, nơi con số mới có nghĩa
make spec         # sinh lại bảng Zobrist và vector

make cover        # gate: >= 95% dòng, >= 90% nhánh
make drift        # chặn việc sửa tay file được sinh tự động
```

## Điều quan trọng nhất cần biết

Có **hai** engine luật — Swift ở client để phản hồi trong một frame, Go ở server vì server
là trọng tài. Chúng lệch nhau là rủi ro lớn nhất của dự án. Thứ giữ chúng đồng bộ là
`rules-spec/`: cùng bảng hằng số, cùng bộ vector, cùng file parity, chạy trong CI của cả hai
bên. Chi tiết ở [ADR-002](docs/03-solution-design.md#adr-002--nơi-đặt-engine-luật-cờ).

Ba lớp ràng buộc, từ yếu tới mạnh:

1. **43 conformance vector** — viết tay từ đặc tả, cả hai engine cùng chạy.
2. **36 parity position** — hash của thế cờ cuối, bắt việc một engine đổi cách hash.
3. **24 game trace** — 3.936 lần kiểm hash *từng nước*, bắt phân kỳ đúng tại nước xảy ra.

Rủi ro thứ hai — **hai node cùng sở hữu một ván** — được chặn bằng lease Redis có
compare-and-swap, và được chứng minh bằng chaos test trong `internal/node`: giết node giữa
ván, node khác dựng lại từ database, thế cờ và tù binh phải khớp từng chút.

Mọi bug luật phát hiện được **phải** thành một vector mới **trước khi** sửa. Đây là quy trình,
không phải công cụ — không có gì tự động ép được nó.
