# 06 — API & Realtime Protocol

> Hợp đồng giữa iOS app và backend. Phiên bản hóa theo
> [ADR-014](03-solution-design.md#adr-014--phiên-bản-hóa-protocol): REST là `/v1/...`,
> WebSocket là `?pv=1`.
>
> **Quy tắc tương thích:** thêm trường = tương thích; đổi ý nghĩa hoặc xóa trường = breaking.
> Cả hai phía **phải bỏ qua trường lạ**.

## 1. Quy ước chung

| Mục | Quy ước |
|-----|---------|
| Base URL | `https://api.sente.app` |
| WebSocket | `wss://api.sente.app/v1/ws?pv=1` |
| Định dạng | JSON, UTF-8. Trường dùng `snake_case` |
| Thời gian | ISO 8601 UTC với mili giây: `2026-08-28T09:14:03.221Z` |
| Khoảng thời gian | Số nguyên **mili giây**, hậu tố `_ms` |
| Tọa độ | Chuỗi hiển thị (`"d4"`, `"q16"`) — cột A–T bỏ chữ I, hàng đếm từ dưới lên. **Không** dùng tọa độ SGF trong protocol |
| ID | UUID chuỗi |
| Xác thực | `Authorization: Bearer <access_token>` |
| Trace | Client sinh `X-Trace-Id` cho mỗi request; server truyền tiếp vào log |

**Vì sao dùng tọa độ hiển thị thay vì chỉ số số nguyên trong protocol:** log và bug report
đọc được bằng mắt. Chi phí thêm ~2 byte/nước là không đáng kể ([ADR-004](03-solution-design.md#adr-004--transport-realtime-websocket--json)).
Bên trong engine vẫn dùng chỉ số nguyên ([05 §2](05-data-model.md#2-quy-ước-chung)).

### 1.1 Vỏ bọc lỗi (REST)

Mọi lỗi trả về cùng một shape, kể cả 4xx và 5xx:

```json
{
  "error": {
    "code": "challenge_expired",
    "message": "Lời mời này đã hết hạn.",
    "details": { "expired_at": "2026-08-21T10:00:00.000Z" },
    "trace_id": "01J8X2..."
  }
}
```

`message` đã được bản địa hóa theo header `Accept-Language` và **hiển thị trực tiếp được cho
người dùng**. `code` là thứ client dựa vào để rẽ nhánh logic — không bao giờ parse `message`.

### 1.2 Phân trang

Con trỏ (cursor-based), không dùng offset:

```
GET /v1/games?status=finished&limit=20&cursor=eyJlbmRlZF9hdCI6...
→ { "items": [...], "next_cursor": "eyJ...", "has_more": true }
```

### 1.3 Rate limit

Trả về header trên mọi response:

```
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 57
X-RateLimit-Reset: 1756377600
```

Vượt hạn mức → `429` với `code: "rate_limited"` và header `Retry-After`.

| Nhóm endpoint | Hạn mức |
|---------------|---------|
| `POST /v1/auth/*` | 10 / phút / IP |
| Tạo lời mời | 20 / giờ / user |
| Nước đi correspondence | 120 / phút / user |
| Chat | 20 / phút / user |
| Còn lại (đọc) | 120 / phút / user |
| WebSocket message | 30 / 10 giây / kết nối |

## 2. REST API

### 2.1 Xác thực

| Method | Path | Mô tả |
|--------|------|-------|
| `POST` | `/v1/auth/guest` | Tạo tài khoản khách gắn với thiết bị |
| `POST` | `/v1/auth/apple` | Đăng nhập / liên kết Sign in with Apple |
| `POST` | `/v1/auth/refresh` | Đổi refresh token lấy cặp token mới (rotating). Mọi thất bại đều là `401`; dùng lại token đã xoay quá 10 giây → `session_revoked`, cả họ token bị thu hồi |
| `POST` | `/v1/auth/logout` | Thu hồi refresh token hiện tại |

```http
POST /v1/auth/guest
Content-Type: application/json

{
  "device_public_key": "MCowBQYDK2VwAyEA...",
  "device_check_token": "AgAAAO...",
  "locale": "vi"
}
```

```json
201 Created
{
  "user": { "id": "018f...", "display_name": "an-cao-thu-2481", "friend_code": "K7MPQ2XB", "is_guest": true },
  "access_token": "eyJhbGciOi...",
  "expires_in": 900,
  "refresh_token": "rt_8Xq2..."
}
```

```http
POST /v1/auth/apple
Authorization: Bearer <access_token khách, nếu đang nâng cấp tài khoản>

{ "identity_token": "eyJraWQiOi...", "nonce": "<nonce gốc app đã sinh>", "full_name": "Bùi Văn Tính" }
```

Trả về cùng hình dạng với `POST /v1/auth/guest` (`user` + cặp token mới). `nonce` là giá trị
**gốc** app sinh ra; Apple để SHA-256 của nó trong token và server so khớp. `full_name` chỉ
có ở lần đăng nhập đầu — Apple không trả lại lần sau — nên server dùng ngay để đặt tên.

Quy tắc chọn tài khoản:
- Có `Authorization` của khách và `sub` chưa gắn với ai → **liên kết vào khách hiện tại**,
  `is_guest` thành `false`, giữ nguyên ván và mã bạn bè.
- `sub` đã thuộc một tài khoản → **trả về tài khoản đó**, dù đang là khách khác. Người bấm
  nút rõ ràng muốn tài khoản của họ; tài khoản khách bị bỏ lại. *(Sửa lại khi cài đặt: bản
  thiết kế định trả `409 account_conflict` để hỏi — bỏ, vì màn hình hỏi "giữ cái nào" làm
  khó đúng người dùng đang cần nhất: người vừa đổi máy.)*
- Không có gì cả → tạo tài khoản mới, không phải khách.
- Server chưa cấu hình `SENTE_APPLE_TEAM_ID` → `404 not_available`; `feature_flags.apple_sign_in`
  cho app biết trước để ẩn nút.

```http
POST /v1/auth/apple/notifications      ← Apple gọi, không phải app
{ "payload": "<JWT ký bởi Apple, claim events chứa type + sub>" }
```

`consent-revoked` / `account-delete` ⇒ gỡ liên kết, thu hồi mọi refresh token của tài khoản,
tài khoản trở lại là khách ([08 §2.4](08-security.md#24-sign-in-with-apple)). Đăng ký URL này
trong App ID → Sign in with Apple → *Server-to-Server Notification Endpoint*.

### 2.2 Hồ sơ

| Method | Path | Mô tả |
|--------|------|-------|
| `GET` | `/v1/me` | Hồ sơ, cài đặt, số ván đang chờ mình đi |
| `PATCH` | `/v1/me` | Đổi `display_name` (2–24 ký tự sau khi cắt khoảng trắng, sai → `400 invalid_name`); trả hồ sơ mới. `locale`, `settings` chưa làm |
| `DELETE` | `/v1/me` | Xóa tài khoản ([FR-A5](01-requirements.md#41-tài-khoản--danh-tính)) — bất đồng bộ, phản hồi `202` |
| `GET` | `/v1/users/by-code/{friend_code}` | Tra người chơi theo mã bạn bè |

### 2.3 Bạn bè và chặn

| Method | Path | Mô tả |
|--------|------|-------|
| `GET` | `/v1/friends` | Danh sách bạn + trạng thái online |
| `POST` | `/v1/friends/requests` | `{ "friend_code": "K7MPQ2XB" }` |
| `POST` | `/v1/friends/requests/{user_id}/accept` | |
| `POST` | `/v1/friends/requests/{user_id}/decline` | |
| `DELETE` | `/v1/friends/{user_id}` | Hủy kết bạn |
| `POST` | `/v1/blocks` | `{ "user_id": "..." }` |
| `DELETE` | `/v1/blocks/{user_id}` | |

### 2.4 Lời mời

| Method | Path | Mô tả |
|--------|------|-------|
| `POST` | `/v1/challenges` | Tạo lời mời |
| `GET` | `/v1/challenges` | Lời mời tôi tạo + lời mời gửi tới tôi |
| `GET` | `/v1/challenges/{code}` | Xem trước (**không cần đăng nhập** — dùng cho landing page) |
| `POST` | `/v1/challenges/{code}/accept` | Chấp nhận → tạo ván |
| `POST` | `/v1/challenges/{code}/decline` | |
| `DELETE` | `/v1/challenges/{id}` | Hủy lời mời của mình |
| `GET` | `/v1/invites/pending` | Deferred deep link ([ADR-012](03-solution-design.md#adr-012--mời-bạn-qua-universal-link-có-xử-lý-deferred)) |

```http
POST /v1/challenges

{
  "invitee_id": null,
  "creator_color": "random",
  "config": {
    "board_size": 9,
    "rules": "japanese",
    "komi": 6.5,
    "handicap": 0,
    "is_ranked": false,
    "time_control": { "kind": "byoyomi", "main_time_ms": 600000, "periods": 3, "period_time_ms": 30000 }
  }
}
```

```json
201 Created
{
  "id": "018f...",
  "code": "R4TN8KMP",
  "share_url": "https://sente.app/j/R4TN8KMP",
  "expires_at": "2026-09-04T09:14:03.221Z",
  "status": "pending"
}
```

```json
POST /v1/challenges/R4TN8KMP/accept
201 Created
{ "game_id": "018f...", "your_color": "white" }
```

Chấp nhận là thao tác **có tính đua**: hai người cùng bấm vào một link mở. Server dùng
`UPDATE challenges SET status='accepted' WHERE code=$1 AND status='pending' RETURNING *` —
ai không nhận được dòng nào sẽ nhận `409 challenge_already_accepted`.

### 2.5 Ván cờ

| Method | Path | Mô tả |
|--------|------|-------|
| `GET` | `/v1/games` | Ván của tôi. `?status=active\|finished`, `?opponent=`, `?board_size=` |
| `GET` | `/v1/games/{id}` | Trạng thái đầy đủ (dùng khi vào ván lần đầu, trước khi mở WS) |
| `GET` | `/v1/games/{id}/moves?from=0` | Danh sách nước đi (dùng cho replay) |
| `POST` | `/v1/games/{id}/moves` | **Chỉ cho ván correspondence.** Ván live phải đi qua WebSocket |
| `GET` | `/v1/games/{id}/sgf` | Tải SGF (`Content-Type: application/x-go-sgf`) |
| `GET` | `/v1/games/{id}/chat` | Lịch sử chat |
| `POST` | `/v1/games/{id}/resign` | Xin thua (khả dụng cả khi WS đứt) |

```http
POST /v1/games/018f.../moves

{
  "client_move_id": "3f2a...",
  "expected_move_no": 42,
  "kind": "play",
  "point": "q16"
}
```

```json
200 OK
{
  "move_no": 43,
  "board_hash": "0x8a3f1c...",
  "captured": ["r16", "r15"],
  "to_play": "white",
  "move_deadline": "2026-08-30T09:14:03.221Z"
}
```

Gửi lại cùng `client_move_id` trả về **đúng response cũ** với `200`, không tạo nước đi mới.

### 2.6 Thiết bị, báo cáo, cấu hình

| Method | Path | Mô tả |
|--------|------|-------|
| `POST` | `/v1/devices` | Đăng ký APNs token: `{ "apns_token": "<hex>", "environment": "sandbox"\|"production", "app_version": "0.1.0" }` → `204`. Gọi lại mỗi lần mở app; token đổi chủ thì theo người mới |
| `PATCH` | `/v1/devices/{id}` | Cập nhật `push_prefs` — **chưa làm**; cột `push_prefs` đã có, mặc định bật cả |
| `DELETE` | `/v1/devices/{token}` | Hủy đăng ký, chỉ token của chính mình |
| `POST` | `/v1/reports` | Báo cáo người chơi |
| `GET` | `/v1/config` | **Không cần auth.** Cấu hình client |
| `GET` | `/j/{code}` | **Không cần auth.** Landing page HTML cho người chưa cài app |
| `GET` | `/.well-known/apple-app-site-association` | **Không cần auth.** Chỉ có khi cấu hình `SENTE_APPLE_TEAM_ID` |
| `GET` | `/metrics` | Prometheus. **Không** được proxy ra ngoài — chỉ mạng nội bộ |

```json
GET /v1/config
{
  "rules_version": "1.0.0",
  "min_supported_app_version": "1.0.0",
  "recommended_app_version": "1.2.0",
  "protocol_versions": [1],
  "max_main_time_ms": { "9": 10800000, "13": 32400000, "19": 86400000 },
  "feature_flags": { "matchmaking": false, "ai_opponent": false, "ranked": false,
                     "apple_sign_in": true, "push": true, "invite_links": true, "refresh_tokens": true },
  "server_time": "2026-08-28T09:14:03.221Z"
}
```

`max_main_time_ms` là trần thời gian chính mỗi bên theo cỡ bàn; `POST /v1/games` và
`POST /v1/challenges` vượt trần trả `400 invalid_config` ("bàn 9×9 cho phép tối đa 3 giờ mỗi
bên"). Ván `correspondence` không áp trần. App gọi endpoint này lúc khởi động. `min_supported_app_version` cho phép ép nâng cấp khi có
breaking change; `server_time` cho lần hiệu chỉnh đồng hồ đầu tiên.

## 3. WebSocket Protocol

### 3.1 Bắt tay

```
GET /v1/ws?pv=1
Host: api.sente.app
Upgrade: websocket
Authorization: Bearer <access_token>
```

`URLSessionWebSocketTask` cho phép đặt header trên `URLRequest`, nên token đi ở header, không
ở query string (query string bị ghi vào access log của proxy).

Server trả `hello` ngay sau khi upgrade:

```json
{ "type": "hello", "payload": {
    "session_id": "018f...",
    "server_time": "2026-08-28T09:14:03.221Z",
    "rules_version": "1.0.0",
    "heartbeat_interval_ms": 20000
}}
```

### 3.2 Vỏ bọc message

```json
{
  "id":   "c-17",
  "type": "move",
  "ts":   "2026-08-28T09:14:03.221Z",
  "payload": { }
}
```

- `id`: do bên gửi sinh. Client dùng tiền tố `c-`, server dùng `s-`.
- Response tương ứng với một request mang thêm `re: "<id gốc>"`.
- Message do server tự phát (broadcast) không có `re`.

### 3.3 Danh mục message

**Client → Server**

| `type` | Payload | Ghi chú |
|--------|---------|---------|
| `subscribe` | `{ game_id }` | Bắt đầu nhận sự kiện của ván |
| `unsubscribe` | `{ game_id }` | |
| `resume` | `{ game_id, last_move_no, board_hash }` | Đồng bộ lại sau khi đứt kết nối, hoặc khi client tự thấy lệch (hụt nước, sai hash). *Sửa lại khi cài đặt:* server luôn trả `game_state` đầy đủ, chưa có `sync_delta` |
| `move` | `{ game_id, client_move_id, expected_move_no, kind, point? }` | `kind` ∈ `play` \| `pass` \| `resign` |
| `mark_dead` | `{ game_id, point, dead }` | Bật/tắt trạng thái chết của **đám quân chứa** `point` |
| `scoring_accept` | `{ game_id, accepted }` | `false` = rút lại đồng ý |
| `scoring_resume` | `{ game_id }` | "Chơi tiếp" — không đồng ý kết quả |
| `undo_request` | `{ game_id }` | |
| `undo_response` | `{ game_id, accept }` | |
| `chat` | `{ game_id, body }` | |
| `ping` | `{ client_time }` | Mỗi 20 giây |

**Server → Client**

| `type` | Payload | Khi nào |
|--------|---------|---------|
| `hello` | `{ session_id, server_time, rules_version, heartbeat_interval_ms }` | Ngay sau khi kết nối |
| `game_state` | Trạng thái đầy đủ (xem [§3.4](#34-game_state)) | Sau `subscribe`, hoặc `resume` khi lệch quá xa |
| `sync_delta` | `{ game_id, moves: [...], clock, server_time }` | Sau `resume` khi chỉ thiếu vài nước |
| `move_ack` | `{ game_id, client_move_id, move_no, board_hash, clock }` | Nước đi được chấp nhận |
| `move_rejected` | `{ game_id, client_move_id, code, message }` | Nước đi bị từ chối |
| `move_made` | `{ game_id, move_no, color, kind, point?, captured: [], board_hash, clock }` | Broadcast cho mọi subscriber |
| `clock_update` | `{ game_id, clock, server_time }` | Mỗi 5 giây trong ván live |
| `clock_adjusted` | `{ game_id, color, delta_ms, reason }` | Bù giờ sau sự cố server |
| `scoring_state` | `{ game_id, dead_points: [], suggested_points: [], score, black_accepted, white_accepted }` | Trong giai đoạn đếm điểm |
| `game_over` | `{ game_id, result, final_board_hash }` | Ván kết thúc |
| `undo_requested` | `{ game_id, by }` | |
| `undo_result` | `{ game_id, accepted, move_no }` | Nếu `accepted` thì **`game_state` đi ngay sau**: client không tự lùi được (quân bị bắt đã mất), nên khóa thao tác cho tới khi nhận bàn cờ mới |
| `resync_required` | `{ game_id, move_no }` | Sau "chơi tiếp" từ đếm điểm; `game_state` đi ngay sau |
| `chat` | `{ game_id, message_id, user_id, display_name, body, move_no, ts }` | |
| `presence` | `{ game_id, user_id, online }` | Đối thủ vào/rời |
| `error` | `{ code, message, re? }` | Lỗi cho một message cụ thể hoặc lỗi phiên |
| `pong` | `{ client_time, server_time }` | Trả lời `ping`; dùng để tính clock offset |

### 3.4 `game_state`

```json
{
  "type": "game_state",
  "re": "c-3",
  "payload": {
    "game_id": "018f...",
    "phase": "playing",
    "rules": "japanese",
    "rules_version": "1.0.0",
    "board_size": 19,
    "komi": 6.5,
    "handicap": 0,
    "players": {
      "black": { "user_id": "018a...", "display_name": "an", "rank": "12k", "online": true },
      "white": { "user_id": "018b...", "display_name": "binh", "rank": "10k", "online": false }
    },
    "board": "................../..........b.......//...w............../...",
    "board_hash": "0x8a3f1c0b2d4e5f60",
    "to_play": "black",
    "move_no": 42,
    "last_move": { "move_no": 42, "color": "white", "kind": "play", "point": "q16" },
    "ko_point": null,
    "captures": { "black": 4, "white": 2 },
    "consecutive_passes": 0,
    "clock": {
      "black": { "main_ms": 845000, "periods": 3, "period_ms": 30000 },
      "white": { "main_ms": 1102000, "periods": 3, "period_ms": 30000 },
      "move_deadline": "2026-08-28T09:28:03.221Z"
    },
    "server_time": "2026-08-28T09:14:03.221Z"
  }
}
```

**Mã hóa `board`.** Chuỗi độ dài `board_size²`, đọc theo hàng từ **trên xuống**, mỗi ký tự là
`.` (trống), `b` (đen), `w` (trắng). 361 byte cho 19×19 — nhỏ hơn mảng JSON và đọc được bằng
mắt khi debug. `/` trong ví dụ trên chỉ để minh họa, **không** xuất hiện trong dữ liệu thật.

### 3.5 Đi một nước — ví dụ đầy đủ

```json
// Client → Server
{ "id": "c-17", "type": "move", "ts": "2026-08-28T09:14:03.221Z", "payload": {
    "game_id": "018f...",
    "client_move_id": "3f2a8c11-0000-4000-8000-000000000001",
    "expected_move_no": 42,
    "kind": "play",
    "point": "d4"
}}
```

```json
// Server → Client A (người đi)
{ "id": "s-91", "re": "c-17", "type": "move_ack", "payload": {
    "game_id": "018f...",
    "client_move_id": "3f2a8c11-0000-4000-8000-000000000001",
    "move_no": 43,
    "board_hash": "0x71bc0e...",
    "clock": { "black": { "main_ms": 838400, "periods": 3, "period_ms": 30000 },
               "white": { "main_ms": 1102000, "periods": 3, "period_ms": 30000 },
               "move_deadline": "2026-08-28T09:32:41.100Z" }
}}
```

```json
// Server → tất cả subscriber (kể cả A)
{ "id": "s-92", "type": "move_made", "payload": {
    "game_id": "018f...",
    "move_no": 43,
    "color": "black",
    "kind": "play",
    "point": "d4",
    "captured": ["c4", "c5"],
    "board_hash": "0x71bc0e...",
    "clock": { "...": "như trên" }
}}
```

Client A nhận **cả hai**: `move_ack` để bỏ trạng thái pending, `move_made` là broadcast chung
nên phải **khử trùng lặp theo `move_no`** — nếu đã áp dụng `move_no` này rồi thì bỏ qua.

### 3.6 Từ chối nước đi

```json
{ "id": "s-93", "re": "c-17", "type": "move_rejected", "payload": {
    "game_id": "018f...",
    "client_move_id": "3f2a8c11-...",
    "code": "out_of_sync",
    "message": "Bàn cờ đã thay đổi. Đang đồng bộ lại…"
}}
```

Kèm theo `out_of_sync`, server **luôn** gửi ngay một `game_state` đầy đủ ngay sau đó — client
không cần chủ động xin.

| `code` | Ý nghĩa | Client làm gì |
|--------|---------|---------------|
| `not_your_turn` | Chưa tới lượt | Rollback lặng lẽ, đồng bộ lại |
| `out_of_sync` | `expected_move_no` không khớp | Rollback + chờ `game_state` |
| `occupied` | Đã có quân | **Cảnh báo lệch engine** — log về server |
| `suicide` | Tự sát | **Cảnh báo lệch engine** |
| `ko` / `superko` | Cấm bởi luật ko | **Cảnh báo lệch engine** |
| `game_not_playing` | Ván đã kết thúc / đang đếm điểm | Hiện trạng thái đúng |
| `timeout` | Đã hết giờ trước khi nước đi tới | Hiện kết quả ván |
| `internal` | Lỗi server (ví dụ ghi DB thất bại) | Cho phép thử lại |

Ba mã in đậm ở trên là những mã **không bao giờ được xảy ra** — client đã kiểm tra bằng engine
cục bộ trước khi gửi. Xảy ra nghĩa là hai engine đã lệch nhau; client gửi báo cáo chẩn đoán
(thế cờ + nước đi) về server để phân tích ([ADR-015](03-solution-design.md#adr-015--quan-sát-hệ-thống-observability-từ-ngày-đầu)).

### 3.7 Đếm điểm

```json
// Server → Client, khi vào phase scoring
{ "type": "scoring_state", "payload": {
    "game_id": "018f...",
    "dead_points":      ["c17", "c18", "d17"],
    "suggested_points": ["c17", "c18", "d17"],
    "territory": { "black": ["a1","a2","b1"], "white": ["s19","t19"] },
    "score": {
      "black": 42.0, "white": 45.5, "winner": "white",
      "detail": { "black": { "territory": 38, "captures": 4, "komi": 0 },
                  "white": { "territory": 33, "captures": 6, "komi": 6.5 } }
    },
    "black_accepted": false,
    "white_accepted": false
}}
```

```json
// Client → Server: bỏ đánh dấu chết cho đám quân chứa c17
{ "type": "mark_dead", "payload": { "game_id": "018f...", "point": "c17", "dead": false } }
```

Server áp dụng cho **toàn bộ đám quân** chứa `c17`, không chỉ một giao điểm, rồi phát lại
`scoring_state` mới cho cả hai với `black_accepted`/`white_accepted` reset về `false`
([02 §6.1](02-go-rules-spec.md#61-quy-trình)).

### 3.8 Kết thúc ván

```json
{ "type": "game_over", "payload": {
    "game_id": "018f...",
    "result": {
      "winner": "white",
      "reason": "counting",
      "score": { "black": 42.0, "white": 45.5, "detail": { } },
      "rating_change": null
    },
    "final_board_hash": "0x71bc0e...",
    "sgf_url": "/v1/games/018f.../sgf"
}}
```

### 3.9 Nhịp tim và đồng bộ đồng hồ

```
Client mỗi 20s:  { "type": "ping", "payload": { "client_time": "...T09:14:03.221Z" } }
Server trả:      { "type": "pong", "payload": { "client_time": "...", "server_time": "...T09:14:03.259Z" } }
```

```
rtt    = now() - client_time
offset = server_time + rtt/2 - now()          // ước lượng lệch đồng hồ
hiển thị còn lại = move_deadline - (now() + offset)
```

Client giữ trung vị của 5 mẫu offset gần nhất để chống nhiễu. Server đóng kết nối với mã
`4006` nếu không nhận được message nào trong 45 giây.

### 3.10 Mã đóng kết nối

| Mã | Ý nghĩa | Client làm gì |
|----|---------|---------------|
| `1000` | Đóng bình thường | Không reconnect |
| `4000` | `unauthorized` — token hết hạn/không hợp lệ | Refresh token rồi reconnect |
| `4001` | `server_restarting` | Reconnect **ngay**, không backoff ([04 §5.2](04-architecture.md#52-deploy-realtime-service-không-làm-đứt-ván)) |
| `4002` | `protocol_unsupported` | Hiện màn hình yêu cầu cập nhật app |
| `4003` | `replaced` — cùng user mở kết nối mới | Không reconnect (thiết bị khác đã tiếp quản) |
| `4004` | `rate_limited` | Reconnect sau `Retry-After` |
| `4005` | `banned` | Hiện thông báo, không reconnect |
| `4006` | `heartbeat_timeout` | Reconnect với backoff |

### 3.11 Chính sách reconnect của client

```
Lần 1: ngay lập tức (0ms)
Lần 2: 1s
Lần 3: 2s → 4s → 8s → 16s → 30s (trần)
Mỗi lần cộng jitter ngẫu nhiên ±25% để tránh dồn cục sau sự cố diện rộng.
Mã 4001 bỏ qua backoff hoàn toàn.
Mã 4000: refresh token TRƯỚC, nếu refresh thất bại thì đăng xuất, không reconnect vô hạn.
```

### 3.12 Backpressure

Server giữ buffer tối đa **64 message** cho mỗi kết nối. Đầy buffer nghĩa là client không
đọc kịp (mạng quá kém hoặc app bị treo):

1. Bỏ các message có thể bỏ được: `clock_update`, `presence`.
2. Nếu vẫn đầy → đóng kết nối với mã `4006`. Client reconnect và `resume` sẽ nhận lại toàn bộ
   trạng thái đúng.

Không bao giờ bỏ `move_made`, `game_over`, `move_ack` — thà đóng kết nối còn hơn mất chúng.

## 4. Ma trận yêu cầu ↔ endpoint

| Yêu cầu | Được phục vụ bởi |
|---------|------------------|
| [FR-A1](01-requirements.md#41-tài-khoản--danh-tính) tài khoản khách | `POST /v1/auth/guest` |
| [FR-A2](01-requirements.md#41-tài-khoản--danh-tính) Sign in with Apple | `POST /v1/auth/apple` |
| [FR-A5](01-requirements.md#41-tài-khoản--danh-tính) xóa tài khoản | `DELETE /v1/me` |
| [FR-M1..M3](01-requirements.md#42-tạo-và-ghép-ván) mời bạn | `/v1/challenges/*`, `/v1/invites/pending` |
| [FR-G1..G5](01-requirements.md#43-chơi-ván-core) đi nước | WS `move` / `move_ack` / `move_made` |
| [FR-G6..G8](01-requirements.md#43-chơi-ván-core) đếm điểm | WS `scoring_state` / `mark_dead` / `scoring_accept` / `scoring_resume` |
| [FR-G9](01-requirements.md#43-chơi-ván-core) đồng hồ | WS `clock_update`, `move_deadline` trong mọi payload có clock |
| [FR-G10](01-requirements.md#43-chơi-ván-core) xin hoãn | WS `undo_request` / `undo_response` / `undo_result` |
| [FR-G11](01-requirements.md#43-chơi-ván-core) chat | WS `chat`, `GET /v1/games/{id}/chat` |
| [FR-R2..R4](01-requirements.md#44-sau-ván-đấu) lịch sử, replay, SGF | `GET /v1/games`, `/moves`, `/sgf` |
| [FR-N1](01-requirements.md#45-thông-báo--vòng-đời-app) push | `POST /v1/devices` + worker |
| [FR-S1](01-requirements.md#46-an-toàn--kiểm-duyệt) báo cáo/chặn | `POST /v1/reports`, `/v1/blocks` |
| [J3](01-requirements.md#j3--mất-mạng-giữa-ván-realtime-p0) khôi phục | WS `resume` → `sync_delta` / `game_state`, `client_move_id` |
