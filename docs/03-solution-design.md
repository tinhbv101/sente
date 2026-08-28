# 03 — Solution Design

> Tài liệu này ghi lại **các quyết định kỹ thuật và lý do**. Kiến trúc kết quả nằm ở
> [04-architecture.md](04-architecture.md); tài liệu này giải thích vì sao nó như vậy.
>
> Định dạng: mỗi quyết định là một ADR (Architecture Decision Record) ngắn. Khi một quyết
> định bị thay thế, **không xóa** — đánh dấu `Superseded by ADR-XXX` và viết ADR mới.

## Nguyên tắc thiết kế

Năm nguyên tắc dưới đây là tiêu chí phân xử khi các ADR mâu thuẫn nhau.

1. **Server là trọng tài.** Client không bao giờ được tin. Mọi nước đi, mọi giây đồng hồ,
   mọi kết quả đều do server quyết định. Client chỉ là bản sao dự đoán.
2. **Đúng luật quan trọng hơn nhanh.** Một ván sai luật phá hỏng niềm tin theo cách mà 100ms
   độ trễ thêm không bao giờ làm được. Khi phải chọn, chọn cái đúng.
3. **Ván cờ là một chuỗi sự kiện, không phải một ô dữ liệu.** Lưu nước đi, tính ra thế cờ.
   Điều này khiến replay, undo, audit và sửa lỗi trở thành hệ quả tự nhiên.
4. **Hỏng mạng là trạng thái bình thường, không phải ngoại lệ.** Mọi luồng phải trả lời được
   câu hỏi "nếu mất kết nối ngay tại đây thì sao?".
5. **Nhỏ trước, mở rộng sau.** Chạy được trên hai node nhỏ, nhưng không có quyết định nào
   khóa chặt khả năng scale ngang về sau.

---

## ADR-001 — Native iOS thay vì cross-platform

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Bối cảnh.** Yêu cầu chỉ nhắm iOS ([A2](01-requirements.md#9-giả-định)). Bàn cờ cần render
mượt với gesture tinh vi (kéo–thả có con trỏ lệch), cần haptics, VoiceOver tùy biến sâu cho
lưới 19×19, và cần chạy nền/khôi phục đúng vòng đời iOS.

**Quyết định.** Native Swift 6 + SwiftUI, iOS 17+.

**Phương án đã cân nhắc.**

| Phương án | Ưu | Nhược | Kết luận |
|-----------|-----|-------|----------|
| Native Swift/SwiftUI | Gesture, haptics, a11y, vòng đời nền tốt nhất; engine luật viết bằng Swift thuần chạy rất nhanh | Phải viết lại nếu làm Android | **Chọn** |
| Flutter | Một codebase cho 2 nền tảng | Gesture bàn cờ và VoiceOver lưới lớn phải tự làm lại; tích hợp APNs/background phức tạp hơn | Loại |
| React Native | Cùng lý do trên + engine luật chạy trong JS bridge chậm | | Loại |
| Unity | Tốt cho game 3D | Quá nặng cho một app cờ 2D; app size vượt [NFR-PL4](01-requirements.md#56-nền-tảng) | Loại |

**Hệ quả.**
- (+) Chất lượng tương tác cao nhất, đúng persona A (người mới, dễ đặt nhầm quân).
- (−) Nếu quyết định làm Android (câu hỏi mở [Q2](01-requirements.md#11-câu-hỏi-mở)), phải
  viết lại toàn bộ UI. Backend không bị ảnh hưởng vì protocol trung lập (xem ADR-004).
- (−) Engine luật Swift không dùng lại được cho Android → khi đó cân nhắc lại ADR-002.

---

## ADR-002 — Nơi đặt engine luật cờ

**Trạng thái:** Accepted · **Ngày:** 2026-08-28 · **Rủi ro cao nhất của dự án**

**Bối cảnh.** Cần engine luật ở **hai nơi**:
- Client: để phản hồi trong 1 frame ([NFR-P3](01-requirements.md#51-hiệu-năng)), để chặn nước
  sai luật kèm giải thích trước khi tốn một vòng mạng, và để chơi offline (pass-and-play, replay).
- Server: vì server là trọng tài (Nguyên tắc 1).

Nếu hai bản engine lệch nhau dù chỉ ở một thế cờ hiếm, người chơi sẽ thấy quân đặt xuống rồi
bị "giật" ngược lại — mất niềm tin ngay lập tức.

**Quyết định.** Hai implementation độc lập (Swift ở client, Go ở server), ràng buộc với nhau
bằng một **bộ conformance vectors dùng chung**:

```
rules-spec/                        # repo hoặc submodule dùng chung
├── zobrist_table.json             # hằng số hash, cả hai bên nạp cùng file
├── vectors/
│   ├── legality/*.json            # thế cờ + nước đi + kết quả mong đợi
│   ├── capture/*.json
│   ├── ko_superko/*.json
│   ├── scoring/*.json
│   └── handicap/*.json
└── VERSION                        # rules_version, ví dụ "1.0.0"
```

Mỗi vector có dạng:

```json
{
  "id": "ko-basic-recapture-illegal",
  "rules": "japanese",
  "board_size": 9,
  "setup": { "black": ["d4","e5","d6","c5"], "white": ["e4","f5","e6"] },
  "moves": ["B:e5", "W:d5"],
  "expect": { "legal": false, "reason": "ko" }
}
```

Schema đầy đủ ở [`rules-spec/SCHEMA.md`](../rules-spec/SCHEMA.md) — đó mới là bản chuẩn;
đoạn trên chỉ là ví dụ. Vector **phải viết tay**: một vector sinh ra từ chính engine mà nó
kiểm tra thì không chứng minh được gì.

CI của **cả hai** repo chạy toàn bộ vectors. Không bên nào merge được nếu fail.
Thay đổi engine ⇒ thêm vector ⇒ bump `rules_version` ⇒ cả hai cùng cập nhật.

Server gửi `rules_version` trong `game_state`; client so với bản của mình, lệch minor thì
hiện cảnh báo mềm, lệch major thì tắt tính năng validate cục bộ và chuyển sang chế độ
"chờ server xác nhận" (chậm hơn nhưng vẫn chơi được).

**Phương án đã cân nhắc.**

| Phương án | Ưu | Nhược | Kết luận |
|-----------|-----|-------|----------|
| Hai bản + conformance vectors | Mỗi bên viết bằng ngôn ngữ tự nhiên của mình, hiệu năng tối đa, không có build phức tạp | Vẫn có nguy cơ lệch ở thế cờ không có trong vectors | **Chọn** |
| Lõi Rust dùng chung (`swift-bridge` cho iOS, `cgo`/sidecar cho Go) | Một implementation duy nhất → không thể lệch | Build cross-compile cho arm64 iOS + linux server; debug qua FFI khó; thêm một ngôn ngữ vào stack với team 2 người ([C1](01-requirements.md#8-ràng-buộc)) | Loại **cho v1.0**; xem lại nếu làm Android |
| Chỉ server có engine, client hỏi server mọi nước | Không thể lệch | Vi phạm NFR-P3; không chơi offline được; UX kém khi mạng chậm | Loại |
| Chỉ client có engine, server tin client | Đơn giản nhất | Vi phạm Nguyên tắc 1; gian lận tầm thường | Loại |

**Hệ quả.**
- (+) Không có FFI, không cross-compile; mỗi engine ~1.200 dòng, test được kỹ.
- (−) Phải kỷ luật: mọi bug luật phát hiện được **bắt buộc** phải thành một vector mới trước
  khi sửa. Đây là quy trình, không phải công cụ — cần ghi vào definition-of-done.
- (−) Thuật toán đề xuất quân chết ([02 §6.4](02-go-rules-spec.md#64-đề-xuất-quân-chết-tự-động))
  **chỉ** viết ở Go, không nhân bản — vì nó là heuristic, không phải luật, và client không cần nó.

---

## ADR-003 — Ngôn ngữ và runtime backend: Go

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Bối cảnh.** Backend cần: giữ hàng chục nghìn kết nối WebSocket ([NFR-S2](01-requirements.md#52-khả-năng-chịu-tải--sẵn-sàng)),
mỗi ván có một timer chính xác, và xử lý nước đi ở p95 < 30ms.

**Quyết định.** Go 1.23+, `nhooyr.io/websocket` (hoặc `gorilla/websocket`), một goroutine
sở hữu mỗi ván đang hoạt động.

**Phương án đã cân nhắc.**

| Phương án | Ưu | Nhược | Kết luận |
|-----------|-----|-------|----------|
| **Go** | Goroutine ánh xạ tự nhiên sang "một actor mỗi ván"; ~4KB/goroutine → 30k kết nối rất thoải mái; timer chuẩn tốt; deploy một binary tĩnh; hệ sinh thái ops trưởng thành | Không có supervision tree sẵn như Elixir | **Chọn** |
| Elixir / Phoenix Channels | Mô hình actor + Presence + PubSub gần như có sẵn cho đúng bài toán này; fault tolerance tốt nhất | Team nhỏ, khả năng tuyển/bảo trì thấp hơn ở VN; hiệu năng CPU cho Monte Carlo playout kém | Loại (sát nút) |
| Node.js / TypeScript | Chia sẻ type với client web tương lai; dev nhanh | Single-thread + playout tốn CPU → phải tách service riêng; timer kém chính xác dưới tải | Loại |
| Swift Vapor | Dùng lại đúng engine luật Swift (giải quyết ADR-002 hoàn toàn) | Hệ sinh thái server nhỏ, ops/observability yếu, rủi ro vận hành cao cho v1.0 | Loại — nhưng là phương án dự phòng đáng cân nhắc nếu bug lệch engine trở nên nghiêm trọng |

**Hệ quả.**
- (+) Monte Carlo score estimator chạy được in-process, không cần service riêng.
- (+) Một binary → deploy và rollback đơn giản.
- (−) Phải tự viết supervision/reclaim khi node chết (xem ADR-005).

---

## ADR-004 — Transport realtime: WebSocket + JSON

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** WebSocket (`wss://`) hai chiều, payload JSON, mỗi message có `type` và `seq`.
REST/HTTPS cho mọi thứ không realtime (auth, danh sách ván, hồ sơ, lịch sử).

**Phương án đã cân nhắc.**

| Phương án | Kết luận |
|-----------|----------|
| **WebSocket + JSON** | **Chọn.** `URLSessionWebSocketTask` có sẵn trong iOS, không thêm dependency; JSON dễ debug, dễ log, dễ viết test bằng tay; băng thông không phải ràng buộc (một nước đi ~120 byte, một ván ~40KB) |
| WebSocket + Protobuf/MessagePack | Nhỏ hơn ~60% nhưng thêm codegen vào cả hai bên; lợi ích không tương xứng ở quy mô này. **Ghi nhận cho tương lai:** protocol đã versioned nên đổi được sau mà không phá tương thích |
| SSE (server→client) + POST (client→server) | Vượt proxy tốt hơn, nhưng hai kênh → thứ tự và reconnect phức tạp hơn hẳn | Loại |
| gRPC bidirectional streaming | Codegen tốt, nhưng gRPC-Swift nặng và HTTP/2 qua các proxy di động VN không ổn định bằng WS | Loại |
| MQTT | Thừa tính năng, thêm broker để vận hành | Loại |

**Hệ quả.**
- (+) Debug bằng `websocat` được, viết integration test bằng tay được.
- (−) Payload lớn hơn Protobuf. Bù bằng permessage-deflate cho message > 512 byte.
- (+) Protocol trung lập với client → mở đường cho Android/web sau này (ADR-001 hệ quả).

Đặc tả đầy đủ ở [06-api-and-realtime-protocol.md](06-api-and-realtime-protocol.md).

---

## ADR-005 — Sở hữu ván: single-owner actor + định tuyến qua Redis

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Bối cảnh.** Hai người chơi có thể kết nối vào hai node khác nhau. Một ván cần: thứ tự nước
đi tuyệt đối, một đồng hồ duy nhất, một timer hết giờ duy nhất. Nếu hai node cùng ghi vào
một ván, sẽ có race về đồng hồ và về `move_no`.

**Quyết định.** Mỗi ván đang hoạt động có **đúng một node sở hữu** (owner). Node sở hữu giữ
một goroutine chứa toàn bộ trạng thái ván trong bộ nhớ và là nơi duy nhất áp dụng nước đi.

```
Client A ──WS──▶ Node 1 ──(không sở hữu)──▶ forward qua Redis Stream ──▶ Node 2 (owner)
Client B ──WS──▶ Node 2 (owner) ──────────────────────────────────────────┘
                                   │
                                   ├─ ghi PostgreSQL (nguồn sự thật)
                                   └─ publish broadcast qua Redis Pub/Sub ──▶ mọi node có subscriber
```

- Quyền sở hữu = một **lease** trong Redis: `SET game:{id}:owner {node_id} NX EX 30`,
  node owner gia hạn mỗi 10 giây.
- Node không sở hữu chỉ làm nhiệm vụ cổng: chuyển tiếp lệnh lên owner, nhận broadcast từ
  Redis Pub/Sub rồi đẩy xuống client của mình.
- Owner chết → lease hết hạn sau ≤ 30s → node đầu tiên nhận được lệnh cho ván đó giành lease
  và **dựng lại trạng thái từ PostgreSQL** (replay các nước đi). Đáp ứng
  [NFR-S4](01-requirements.md#52-khả-năng-chịu-tải--sẵn-sàng) khi kèm sweeper chủ động
  (xem [04](04-architecture.md)).

**Phương án đã cân nhắc.**

| Phương án | Nhược | Kết luận |
|-----------|-------|----------|
| **Single-owner + lease** | Cần cơ chế reclaim; có một hop nội bộ khi hai người ở hai node | **Chọn** |
| Sticky routing theo `game_id` ở load balancer (consistent hashing) | Không có hop nội bộ, nhưng LB phải hiểu `game_id` (nằm sau khi đã kết nối, không có ở lớp L4); rebalance khi thêm/bớt node làm đứt ván | Loại |
| Không có owner, dùng optimistic lock trên DB (`WHERE move_no = $expected`) | Đơn giản, không cần Redis lease — nhưng đồng hồ và timer hết giờ không có chỗ trú; mỗi nước đi tốn một round-trip DB | Loại cho ván live; **vẫn dùng cho ván correspondence** (không cần actor) |
| Một node duy nhất (không scale ngang) | Đơn giản nhất, đủ cho 10k ván | Loại vì vi phạm Nguyên tắc 5 và NFR-S3 |

**Hệ quả.**
- (+) Đồng hồ và thứ tự nước đi không có race — chúng ở trong một goroutine.
- (+) Ván correspondence không cần actor: chúng đi theo đường REST + optimistic lock, tiết
  kiệm tài nguyên vì có thể có hàng trăm nghìn ván "đang mở" nhưng không hoạt động.
- (−) Thêm một hop nội bộ (~1–3ms trong cùng VPC) khi hai người chơi rơi vào hai node khác nhau.
- (−) Phải viết và test kỹ đường reclaim — đây là chỗ dễ sinh bug nhất của kiến trúc.

---

## ADR-006 — Lưu trữ: append-only moves, thế cờ là dữ liệu dẫn xuất

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** Nguồn sự thật của một ván là bảng `moves` chỉ ghi thêm (append-only). Thế cờ
hiện tại **không** được lưu như một cột — nó được tính bằng cách replay.

```sql
-- nguồn sự thật
moves(game_id, move_no, color, kind, col, row, played_at, time_left_ms, ...)
-- dẫn xuất, có thể dựng lại bất cứ lúc nào
games(..., current_move_no, board_hash, phase, ...)
```

- Replay 300 nước trên 19×19 mất ~1ms — rẻ hơn nhiều so với chi phí của một cột thế cờ bị lệch.
- Redis cache thế cờ đã dựng (`game:{id}:state`) để tránh replay ở đường nóng; cache có thể
  mất bất cứ lúc nào mà không ảnh hưởng tính đúng đắn.
- `games.board_hash` lưu Zobrist hash hiện tại như một **checksum**: mỗi lần dựng lại từ
  moves, so hash; lệch nhau ⇒ báo động, không im lặng bỏ qua.

**Hệ quả.**
- (+) Replay, undo, xuất SGF, và audit "vì sao ván này ra kết quả đó" đều miễn phí.
- (+) Sửa được lỗi lịch sử: nếu phát hiện bug engine, replay lại toàn bộ ván với engine mới
  để đo mức ảnh hưởng.
- (−) Không query được trực tiếp theo thế cờ (ví dụ "tìm ván có thế này") — không phải yêu cầu.
- (−) Cần cột `rules_version` trên `games` để replay đúng bằng phiên bản luật lúc ván diễn ra.

---

## ADR-007 — Đặt quân lạc quan (optimistic) + hòa giải với server

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Bối cảnh.** [NFR-P3](01-requirements.md#51-hiệu-năng) đòi phản hồi < 16ms, nhưng round-trip
tới Singapore là 30–60ms và có thể là 2 giây trên 3G kém.

**Quyết định.** Client áp dụng nước đi cục bộ ngay lập tức bằng engine Swift, hiển thị quân ở
trạng thái "chưa xác nhận" (độ mờ nhẹ, không có hiệu ứng đặt quân đầy đủ), rồi gửi lên server.

```
1. Người chơi nhả tay
2. Client: isLegal() cục bộ → sai thì báo lỗi ngay, KHÔNG gửi lên server
3. Client: applyMove() cục bộ → render quân ở trạng thái pending
4. Client: gửi { type: "move", client_move_id: <uuid>, expected_move_no: N, point: ... }
5a. Server ACK → client bỏ trạng thái pending, chạy hiệu ứng đặt quân + haptic
5b. Server NACK → client rollback về state trước (state cũ vẫn còn nguyên nhờ immutability),
    hiện lý do, và nếu lý do là OUT_OF_SYNC thì yêu cầu full resync
```

Sau 3 giây chưa có ACK, hiện chỉ báo "đang gửi…" nhưng **không** rollback — nước đi có thể
đang trên đường.

**Idempotency.** `client_move_id` là UUID do client sinh. Server lưu nó cùng nước đi và có
`UNIQUE(game_id, client_move_id)`. Gửi lại cùng `client_move_id` (sau reconnect) trả về kết
quả cũ, không tạo nước đi mới. Đây là thứ khiến [J3](01-requirements.md#j3--mất-mạng-giữa-ván-realtime-p0) hoạt động.

**Optimistic concurrency.** `expected_move_no` cho phép server phát hiện client đang tụt hậu
(ví dụ đã bỏ lỡ nước của đối phương) và trả `OUT_OF_SYNC` kèm trạng thái đầy đủ thay vì áp
dụng một nước đi sai bối cảnh.

**Hệ quả.**
- (+) Cảm giác tức thì ngay cả trên mạng kém.
- (−) Có khả năng "giật ngược" nếu đồng thời cả hai bên cùng đi (chỉ xảy ra khi client lệch
  lượt — hiếm). Xử lý bằng animation rollback rõ ràng + toast giải thích, không im lặng.
- (+) Kết hợp với ADR-006 (state bất biến), rollback chỉ là gán lại con trỏ về state cũ.

---

## ADR-008 — Xác thực: khách trước, Sign in with Apple sau

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.**

```
Lần mở app đầu tiên
  → client sinh keypair Ed25519, lưu private key vào Keychain (kAccessAfterFirstUnlock,
    KHÔNG đồng bộ iCloud)
  → POST /v1/auth/guest  { device_public_key, device_check_token }
  → server tạo user ẩn danh, trả access token (JWT, 15') + refresh token (30 ngày, quay vòng)

Nâng cấp
  → Sign in with Apple → POST /v1/auth/apple { identity_token }
  → server verify token với Apple, gắn Apple sub vào user hiện tại (KHÔNG tạo user mới)
  → toàn bộ lịch sử ván được giữ nguyên
```

- **DeviceCheck / App Attest** dùng khi tạo tài khoản khách để chống tạo hàng loạt.
- Refresh token quay vòng (rotating): mỗi lần dùng sinh token mới, token cũ vô hiệu; phát
  hiện dùng lại token cũ ⇒ thu hồi cả họ token (dấu hiệu bị đánh cắp).
- Access token ngắn hạn để việc thu hồi (ban, xóa tài khoản) có hiệu lực trong ≤ 15 phút.

**Phương án đã cân nhắc.** Bắt đăng nhập ngay từ đầu — loại, vì mâu thuẫn trực tiếp với
[G1](01-requirements.md#21-mục-tiêu-sản-phẩm) (chơi trong 30 giây) và persona A.

**Hệ quả.**
- (+) Ma sát bằng không cho ván đầu tiên.
- (−) Mất thiết bị = mất tài khoản khách. Chấp nhận, nhưng phải nhắc người dùng đăng nhập
  sau ván đầu tiên thắng lợi (thời điểm có động lực cao nhất).
- (−) Cần luồng gộp tài khoản (merge) nếu người dùng đăng nhập Apple trên thiết bị mới trong
  khi đã có tài khoản khách ở đó → giải quyết bằng hỏi người dùng chọn giữ tài khoản nào.

---

## ADR-009 — Xác định quân chết: Benson + Monte Carlo, người quyết định cuối

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** Xem thuật toán ở [02 §6.4](02-go-rules-spec.md#64-đề-xuất-quân-chết-tự-động).
Tóm tắt lý do:

- **Không** để người chơi tự đánh dấu từ đầu: persona A không biết đám nào chết, sẽ bế tắc ở
  màn hình cuối ván — đây là chỗ bỏ cuộc kinh điển của các app cờ vây.
- **Không** dùng engine mạnh (KataGo) ở v1.0: cần GPU hoặc CPU đáng kể, thêm một service để
  vận hành, chưa tương xứng với giá trị mang lại ở MVP.
- **Không** để thuật toán quyết định thay người: kể cả KataGo cũng sai trong thế hiếm; luật
  cờ vây vốn quy định người chơi thỏa thuận.

**Hệ quả.**
- (+) Persona A kết thúc được ván mà không cần hiểu sống chết.
- (+) Persona B chỉnh được khi thuật toán sai.
- (−) Monte Carlo tốn CPU ở server (300 playout × 19×19 ≈ 60ms một core). Chạy trong
  goroutine riêng với timeout cứng 300ms; quá hạn thì chỉ trả kết quả Benson (an toàn: không
  đề xuất gì).

---

## ADR-010 — Lưu trữ cục bộ trên client: SwiftData

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** SwiftData cho cache ván, hàng đợi nước đi chưa gửi, và ván pass-and-play offline.

| Phương án | Kết luận |
|-----------|----------|
| **SwiftData** | **Chọn.** Tích hợp thẳng với `@Observable`/SwiftUI, không dependency ngoài, đủ cho khối lượng dữ liệu nhỏ (vài trăm ván) |
| GRDB | Kiểm soát SQL tốt hơn, migration rõ ràng hơn — nhưng thêm dependency và chưa cần đến sức mạnh đó |
| Core Data | SwiftData là lớp trên của nó, không có lý do dùng trực tiếp |
| File JSON | Không có query, không transaction cho hàng đợi nước đi |

**Ràng buộc.** Dữ liệu cục bộ là **cache, không phải nguồn sự thật** — trừ hàng đợi nước đi
chưa gửi và ván offline. Xóa toàn bộ local store phải luôn an toàn (app tự đồng bộ lại).

---

## ADR-011 — Render bàn cờ: SwiftUI Canvas

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** Vẽ lưới, quân, và các lớp phủ bằng `Canvas` của SwiftUI; gesture xử lý ở lớp
`SwiftUI` phía trên; chỉ các quân có animation (quân vừa đặt, quân đang bị bắt) mới nằm ở
view riêng để tận dụng animation của SwiftUI.

| Phương án | Kết luận |
|-----------|----------|
| **SwiftUI Canvas** | **Chọn.** 361 giao điểm là quá nhỏ để cần GPU chuyên dụng; đo được 60fps ổn định; ít code, dễ test bằng snapshot |
| 361 `View` riêng lẻ | SwiftUI diffing 361 view mỗi frame → tụt frame trên máy cũ | Loại |
| Metal | Nhanh nhất nhưng phải tự làm mọi thứ kể cả text tọa độ và a11y | Loại |
| SpriteKit | Hợp với game arcade; tích hợp SwiftUI và VoiceOver rườm rà | Loại |

**Accessibility.** `Canvas` không tự sinh accessibility element. Phải phủ một lớp
`accessibilityChildren` gồm 361 phần tử ảo với label dạng `"D4, quân đen"` và
`accessibilityAction` để đặt quân — đáp ứng [NFR-A11Y1](01-requirements.md#55-khả-năng-tiếp-cận--bản-địa-hóa).
Đây là công việc đáng kể, phải nằm trong ước lượng.

---

## ADR-012 — Mời bạn qua Universal Link, có xử lý deferred

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** `https://sente.app/j/<code>` với `apple-app-site-association`.

Luồng khi **chưa cài app** ([J1](01-requirements.md#j1--rủ-bạn-chơi-ván-đầu-tiên-p0)):

```
Chạm link → Safari mở trang landing (có preview cấu hình ván) → nút "Mở trong App Store"
  → cài app → mở lần đầu
  → app gọi GET /v1/invites/pending?fingerprint=<device_fingerprint>
  → server đối chiếu với lượt truy cập landing page gần đây từ cùng IP + user-agent class
  → trả về mã mời → app hiện thẳng màn hình xem trước lời mời
```

**Lưu ý riêng tư.** Fingerprint chỉ gồm IP + lớp thiết bị + khung thời gian 30 phút, không
lưu quá 1 giờ, không dùng cho mục đích nào khác. Ghi rõ trong privacy policy. Nếu không khớp
được, người dùng vẫn nhập mã 8 ký tự thủ công — luôn có đường thoát.

**Phương án đã cân nhắc.** SDK deferred deep link của bên thứ ba (Branch/Adjust) — loại vì
kéo theo SDK theo dõi, ảnh hưởng App Privacy Label và không cần thiết ở quy mô này.

---

## ADR-013 — Thông báo đẩy qua APNs với token key

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** APNs HTTP/2 với **token-based auth** (`.p8` key), không dùng certificate.
Worker riêng đọc từ hàng đợi, không gửi trực tiếp từ đường xử lý nước đi.

- `apns-collapse-id = game:<id>` để nhiều lượt liên tiếp không dồn thành nhiều thông báo.
- `apns-priority = 5` cho ván correspondence (tiết kiệm pin), `10` cho cảnh báo sắp hết giờ.
- Nội dung thông báo **không chứa** nội dung chat (tránh rò rỉ ở màn hình khóa); chỉ đưa tên
  đối thủ và cỡ bàn.
- Thất bại `410 Unregistered` ⇒ xóa device token khỏi DB.

**Hệ quả.** Việc gửi push không nằm trên đường nóng ⇒ APNs chậm hay lỗi không làm chậm nước đi.

---

## ADR-014 — Phiên bản hóa protocol

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.**
- REST: prefix đường dẫn `/v1/...`.
- WebSocket: query param `?pv=1` (protocol version) khi handshake; server từ chối phiên bản
  không hỗ trợ với mã đóng rõ ràng.
- Mọi message có trường `type` dạng chuỗi; **thêm** trường là thay đổi tương thích, **đổi ý
  nghĩa** hoặc **xóa** trường là breaking.
- Client bỏ qua trường lạ (forward compatible). Server bỏ qua trường lạ từ client.
- Cam kết hỗ trợ đồng thời **2 phiên bản protocol gần nhất** ([NFR-Q4](01-requirements.md#53-chất-lượng--bảo-trì)),
  vì người dùng iOS không cập nhật app ngay.

---

## ADR-015 — Quan sát hệ thống (observability) từ ngày đầu

**Trạng thái:** Accepted · **Ngày:** 2026-08-28

**Quyết định.** OpenTelemetry ở server (traces + metrics), structured log JSON, `trace_id`
sinh ở client và truyền qua mọi message WebSocket.

Bốn chỉ số phải có dashboard từ ngày đầu tiên:

| Chỉ số | Vì sao |
|--------|--------|
| `move_apply_duration` (histogram) | Bảo vệ NFR-P2 |
| `illegal_move_rejected_total{reason}` | **Chỉ số cảnh báo lệch engine.** Client đã lọc trước, nên nếu server từ chối một nước đi vì lý do luật (không phải `not_your_turn`), gần như chắc chắn hai engine đã lệch nhau |
| `game_desync_total` | Số lần client phải full resync |
| `game_ended_total{reason}` | Theo dõi tỉ lệ `abandonment`, đo G5 |

`illegal_move_rejected_total{reason="suicide"|"ko"|"superko"|"occupied"}` > 0 phải bắn cảnh
báo — nó có nghĩa là một người chơi thật đang gặp trải nghiệm "quân bị giật ngược".

---

## Bảng tổng hợp quyết định

| ADR | Quyết định | Rủi ro chính | Giảm thiểu |
|-----|-----------|--------------|------------|
| 001 | Native iOS | Khóa nền tảng | Protocol trung lập client |
| 002 | Hai engine + conformance vectors | **Lệch engine** | Vectors trong CI cả hai repo + metric cảnh báo (ADR-015) |
| 003 | Backend Go | Tự viết reclaim | Test chaos cho node failure |
| 004 | WebSocket + JSON | Băng thông | permessage-deflate; đổi được sang binary nhờ ADR-014 |
| 005 | Single-owner actor | Bug ở đường reclaim | Chaos test bắt buộc trước release |
| 006 | Append-only moves | Replay tốn CPU nếu ván rất dài | Redis cache + checkpoint mỗi 50 nước |
| 007 | Optimistic move | "Giật ngược" quân | Chỉ khi lệch lượt; animation + giải thích rõ |
| 008 | Guest-first auth | Mất thiết bị = mất tài khoản | Nhắc đăng nhập sau ván thắng đầu tiên |
| 009 | Benson + Monte Carlo | Đề xuất sai | Người chơi luôn sửa được; timeout an toàn |
| 010 | SwiftData | Migration hạn chế | Local store là cache, xóa được an toàn |
| 011 | SwiftUI Canvas | A11y phải tự làm | Đưa vào ước lượng ngay từ đầu |
| 012 | Universal Link + deferred | Fingerprint không khớp | Luôn có đường nhập mã thủ công |
| 013 | APNs token auth | — | — |
| 014 | Protocol versioned | Gánh nặng tương thích | Chỉ giữ 2 phiên bản |
| 015 | OTel từ ngày đầu | — | — |
